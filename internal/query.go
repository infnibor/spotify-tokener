package internal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

/* =========================
   STRUCTS
   ========================= */

type QueryResult struct {
	Hash              string `json:"hash"`
	SpotifyAppVersion string `json:"spotifyAppVersion"`
	PayloadVersion    string `json:"payloadVersion"`
	OperationName     string `json:"operationName"`
}

type PersistedQueryInfo struct {
	Version    int
	Sha256Hash string
}

type QueryPayloadResult struct {
	OperationName  string
	PersistedQuery *PersistedQueryInfo
}

/* =========================
   GLOBAL CORRELATION
   ========================= */

type queryState struct {
	Hash          string
	OperationName string
	Version       int
}

var (
	stateMu sync.Mutex
	states  = map[string]*queryState{} // key = sha256Hash
)

/* =========================
   DEEP OPERATION SCAN
   ========================= */

func findOperationName(v interface{}) string {
	switch val := v.(type) {
	case map[string]interface{}:
		for k, v2 := range val {
			if k == "operationName" {
				if s, ok := v2.(string); ok && s != "" {
					return s
				}
			}
			if r := findOperationName(v2); r != "" {
				return r
			}
		}
	case []interface{}:
		for _, v2 := range val {
			if r := findOperationName(v2); r != "" {
				return r
			}
		}
	}
	return ""
}

/* =========================
   POST DATA PROCESSOR
   ========================= */

func processPostData(postData string) {
	var raw map[string]interface{}
	if json.Unmarshal([]byte(postData), &raw) != nil {
		return
	}

	var (
		hash    string
		version int
		opName  string
	)

	// operationName (ANYWHERE)
	if v, ok := raw["operationName"].(string); ok {
		opName = v
	}
	if opName == "" {
		opName = findOperationName(raw)
	}

	// persistedQuery
	if ext, ok := raw["extensions"].(map[string]interface{}); ok {
		if pq, ok := ext["persistedQuery"].(map[string]interface{}); ok {
			if h, ok := pq["sha256Hash"].(string); ok {
				hash = h
			}
			if v, ok := pq["version"].(float64); ok {
				version = int(v)
			}
		}
	}

	if hash == "" {
		return
	}

	stateMu.Lock()
	defer stateMu.Unlock()

	s, ok := states[hash]
	if !ok {
		s = &queryState{Hash: hash}
		states[hash] = s
	}

	// freeze mapping: nie nadpisuj pustym
	if opName != "" && s.OperationName == "" {
		s.OperationName = opName
	}
	if version != 0 && s.Version == 0 {
		s.Version = version
	}
}

/* =========================
   SCRAPER
   ========================= */

func GetSpotifyQueryResults(ctx context.Context, uri string) ([]*QueryPayloadResult, error) {
	if uri == "" {
		return nil, errors.New("empty uri")
	}

	var pageURL string
	switch {
	case strings.Contains(uri, "spotify:"):
		parts := strings.Split(uri, ":")
		pageURL = "https://open.spotify.com/" + parts[1] + "/" + parts[2]
	case strings.Contains(uri, "open.spotify.com"):
		pageURL = strings.Split(uri, "?")[0]
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


	_ = chromedp.Run(cctx, network.Enable())
	_ = chromedp.Run(cctx, fetch.Enable().WithPatterns([]*fetch.RequestPattern{
		{URLPattern: "*pathfinder/v2/query*", RequestStage: fetch.RequestStageRequest},
	}))

	chromedp.ListenTarget(cctx, func(ev interface{}) {
		if e, ok := ev.(*fetch.EventRequestPaused); ok {
			if strings.Contains(e.Request.URL, "pathfinder/v2/query") {
				pd, _ := network.GetRequestPostData(
					network.RequestID(e.RequestID.String()),
				).Do(cctx)
				if pd != "" {
					processPostData(pd)
				}
			}
			_ = fetch.ContinueRequest(e.RequestID).Do(cctx)
		}
	})

	if err := chromedp.Run(
		cctx,
		chromedp.Navigate(pageURL),
		chromedp.Sleep(5*time.Second),
		chromedp.Reload(),
		chromedp.Sleep(4*time.Second),
	); err != nil {
		return nil, err
	}

	stateMu.Lock()
	defer stateMu.Unlock()

	var out []*QueryPayloadResult
	for _, s := range states {
		if s.Hash != "" && s.OperationName != "" {
			out = append(out, &QueryPayloadResult{
				OperationName: s.OperationName,
				PersistedQuery: &PersistedQueryInfo{
					Version:    s.Version,
					Sha256Hash: s.Hash,
				},
			})
		}
	}

	if len(out) == 0 {
		return nil, errors.New("no queries with operationName")
	}

	return out, nil
}

/* =========================
   FINAL API
   ========================= */

func GetSpotifyQueryResult(ctx context.Context, uri string) (*QueryResult, error) {
	results, err := GetSpotifyQueryResults(ctx, uri)
	if err != nil {
		return nil, err
	}

	r := results[0]
	return &QueryResult{
		Hash:              r.PersistedQuery.Sha256Hash,
		PayloadVersion:    strconv.Itoa(r.PersistedQuery.Version),
		OperationName:     r.OperationName,
		SpotifyAppVersion: "",
	}, nil
}

/* =========================
   HTTP COMPAT
   ========================= */

func GetSpotifyQueryResultFromRequestWithBrowser(
	ctx context.Context,
	b *Browser,
	r *http.Request,
) (*QueryResult, error) {
	uri := r.URL.Query().Get("uri")
	if uri == "" {
		return nil, errors.New("missing uri")
	}
	return GetSpotifyQueryResult(ctx, uri)
}
