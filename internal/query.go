package internal

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

type QueryResult struct {
	Hash              string `json:"hash"`
	SpotifyAppVersion string `json:"spotifyAppVersion"`
	PayloadVersion    string `json:"payloadVersion"`
	OperationName     string `json:"operationName"`
}

type PersistedQueryInfo struct {
	Version    int    `json:"version"`
	Sha256Hash string `json:"sha256Hash"`
}

type QueryPayloadResult struct {
	OperationName     string
	PersistedQuery    *PersistedQueryInfo
	SpotifyAppVersion string
	RequestID         string
	RawPayload        map[string]interface{}
}

/* =========================
   REQUEST CORRELATION STATE
   ========================= */

type requestState struct {
	OperationName string
	Hash          string
	Version       int
	RawPayload    map[string]interface{}
}

var requestStates sync.Map // map[string]*requestState

/* =========================
   OPERATION NAME DEEP SCAN
   ========================= */

func findOperationName(v interface{}, state *requestState) {
	switch val := v.(type) {
	case map[string]interface{}:
		for k, v2 := range val {
			if k == "operationName" {
				if s, ok := v2.(string); ok && s != "" {
					state.OperationName = s
					return
				}
			}
			findOperationName(v2, state)
			if state.OperationName != "" {
				return
			}
		}
	case []interface{}:
		for _, v2 := range val {
			findOperationName(v2, state)
			if state.OperationName != "" {
				return
			}
		}
	}
}

/* =========================
   PAYLOAD PROCESSOR
   ========================= */

func processPostData(
	requestID string,
	postData string,
	mu *sync.Mutex,
	results *[]*QueryPayloadResult,
	seen map[string]bool,
	headers network.Headers,
) {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(postData), &raw); err != nil {
		return
	}

	stateAny, _ := requestStates.LoadOrStore(requestID, &requestState{})
	state := stateAny.(*requestState)

	/* ---- operationName extraction ---- */

	if op, ok := raw["operationName"].(string); ok && op != "" {
		state.OperationName = op
	}

	if state.OperationName == "" {
		if ext, ok := raw["extensions"].(map[string]interface{}); ok {
			if op, ok := ext["operationName"].(string); ok && op != "" {
				state.OperationName = op
			}
		}
	}

	if state.OperationName == "" {
		findOperationName(raw, state)
	}

	/* ---- hash extraction ---- */

	if ext, ok := raw["extensions"].(map[string]interface{}); ok {
		if pq, ok := ext["persistedQuery"].(map[string]interface{}); ok {
			if h, ok := pq["sha256Hash"].(string); ok {
				state.Hash = h
			}
			if v, ok := pq["version"].(float64); ok {
				state.Version = int(v)
			}
		}
	}

	state.RawPayload = raw

	if state.Hash == "" || state.OperationName == "" {
		return
	}

	mu.Lock()
	defer mu.Unlock()

	if seen[requestID] {
		return
	}
	seen[requestID] = true

	*results = append(*results, &QueryPayloadResult{
		OperationName: state.OperationName,
		PersistedQuery: &PersistedQueryInfo{
			Version:    state.Version,
			Sha256Hash: state.Hash,
		},
		RequestID:  requestID,
		RawPayload: state.RawPayload,
	})
}

/* =========================
   MAIN SCRAPER
   ========================= */

func GetSpotifyQueryResults(ctx context.Context, spotifyURI string) ([]*QueryPayloadResult, error) {
	if spotifyURI == "" {
		return nil, errors.New("empty uri")
	}

	var pageURL string
	switch {
	case strings.HasPrefix(spotifyURI, "spotify:track:"):
		pageURL = "https://open.spotify.com/track/" + strings.TrimPrefix(spotifyURI, "spotify:track:")
	case strings.HasPrefix(spotifyURI, "spotify:album:"):
		pageURL = "https://open.spotify.com/album/" + strings.TrimPrefix(spotifyURI, "spotify:album:")
	case strings.HasPrefix(spotifyURI, "spotify:playlist:"):
		pageURL = "https://open.spotify.com/playlist/" + strings.TrimPrefix(spotifyURI, "spotify:playlist:")
	case strings.Contains(spotifyURI, "open.spotify.com"):
		pageURL = strings.Split(spotifyURI, "?")[0]
	default:
		return nil, errors.New("unsupported uri")
	}

	opts := chromedp.DefaultExecAllocatorOptions[:]
	if bin := os.Getenv("HASH_BROWSER_BIN"); bin != "" {
		opts = append(opts, chromedp.ExecPath(bin))
	}

	allocCtx, cancel := chromedp.NewExecAllocator(ctx, opts...)
	defer cancel()

	cctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	if err := chromedp.Run(cctx, network.Enable()); err != nil {
		return nil, err
	}

	if err := chromedp.Run(cctx,
		fetch.Enable().WithPatterns([]*fetch.RequestPattern{
			{URLPattern: "*pathfinder/v2/query*", RequestStage: fetch.RequestStageRequest},
		}),
	); err != nil {
		return nil, err
	}

	var mu sync.Mutex
	var results []*QueryPayloadResult
	seen := map[string]bool{}

	chromedp.ListenTarget(cctx, func(ev interface{}) {
		if fev, ok := ev.(*fetch.EventRequestPaused); ok {
			if strings.Contains(fev.Request.URL, "pathfinder/v2/query") {
				pd, _ := network.GetRequestPostData(network.RequestID(fev.RequestID.String())).
					Do(cctx)
				if pd != "" {
					processPostData(fev.RequestID.String(), pd, &mu, &results, seen, fev.Request.Headers)
				}
			}
			_ = fetch.ContinueRequest(fev.RequestID).Do(cctx)
		}
	})

	err := chromedp.Run(cctx,
		chromedp.Navigate(pageURL),
		chromedp.Sleep(4*time.Second),
		chromedp.Reload(),
		chromedp.Sleep(5*time.Second),
	)

	if err != nil {
		return nil, err
	}

	time.Sleep(3 * time.Second)

	if len(results) == 0 {
		return nil, errors.New("no queries captured")
	}

	return results, nil
}
