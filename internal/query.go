
package internal

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"net/http"
	"strings"
	"sync"
	"time"
	"strconv"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/cdproto/network"
)

type QueryResult struct {
	Hash              string `json:"hash"`
	SpotifyAppVersion string `json:"spotifyAppVersion"`
	PayloadVersion    string `json:"payloadVersion"`
}

type PersistedQueryInfo struct {
	Version    int    `json:"version"`
	Sha256Hash string `json:"sha256Hash"`
}

type QueryPayloadResult struct {
	OperationName      string                 `json:"operationName"`
	Variables          map[string]interface{} `json:"variables"`
	Extensions         map[string]interface{} `json:"extensions"`
	PersistedQuery     *PersistedQueryInfo    `json:"persistedQuery,omitempty"`
	SpotifyAppVersion  string                 `json:"spotifyAppVersion,omitempty"`
	RequestID          string                 `json:"requestId,omitempty"`
	RawPayload         map[string]interface{} `json:"rawPayload,omitempty"`
}

func GetSpotifyQueryResultFromRequest(ctx context.Context, r interface{}) (*QueryResult, error) {
	if httpReq, ok := r.(*http.Request); ok {
		q := httpReq.URL.Query()
		val := q.Get("playlist")
		if val == "" {
			val = q.Get("uri")
		}
		if val == "" {
			val = q.Get("track")
		}
		if val == "" {
			// accept full URL via url or q
			val = q.Get("url")
		}
		if val == "" {
			val = q.Get("q")
		}
		if val == "" {
			return nil, errors.New("empty uri")
		}
		return GetSpotifyQueryResult(ctx, val)
	}
	return nil, errors.New("invalid request type")
}

// GetSpotifyQueryResult returns the first found info for the /api/query endpoint (backwards compatible)
func GetSpotifyQueryResult(ctx context.Context, playlistURI string) (*QueryResult, error) {
	results, err := GetSpotifyQueryResults(ctx, playlistURI)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, errors.New("hash not found")
	}
	// prefer persistedQuery if present
	for _, r := range results {
		if r.PersistedQuery != nil && r.PersistedQuery.Sha256Hash != "" {
			return &QueryResult{
				Hash:              r.PersistedQuery.Sha256Hash,
				SpotifyAppVersion: r.SpotifyAppVersion,
				PayloadVersion:    strconv.Itoa(r.PersistedQuery.Version),
			}, nil
		}
	}
	// fallback: return first
	r := results[0]
	var hv string
	var pv string
	if r.PersistedQuery != nil {
		hv = r.PersistedQuery.Sha256Hash
		pv = strconv.Itoa(r.PersistedQuery.Version)
	}
	return &QueryResult{
		Hash:              hv,
		SpotifyAppVersion: r.SpotifyAppVersion,
		PayloadVersion:    pv,
	}, nil
}

// GetSpotifyQueryResults visits the provided Spotify URI (track or playlist), reloads, and
// captures all POST requests to /pathfinder/v2/query whose payload contains
// operationName "getTrack" or "fetchPlaylistMetadata". Returns parsed payloads.
func GetSpotifyQueryResults(ctx context.Context, spotifyURI string) ([]*QueryPayloadResult, error) {
	if spotifyURI == "" {
		return nil, errors.New("empty uri")
	}
	// normalize to path
	var pageURL string
	if strings.HasPrefix(spotifyURI, "spotify:track:") {
		id := strings.TrimPrefix(spotifyURI, "spotify:track:")
		pageURL = "https://open.spotify.com/track/" + id
	} else if strings.HasPrefix(spotifyURI, "spotify:playlist:") {
		id := strings.TrimPrefix(spotifyURI, "spotify:playlist:")
		pageURL = "https://open.spotify.com/playlist/" + id
	} else if strings.Contains(spotifyURI, "open.spotify.com/track/") || strings.Contains(spotifyURI, "open.spotify.com/playlist/") {
		pageURL = spotifyURI
	} else {
		return nil, errors.New("unsupported uri format")
	}

	browserBin := os.Getenv("HASH_BROWSER_BIN")
	var allocCtx context.Context
	var allocCancel context.CancelFunc
	if browserBin != "" {
		opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(browserBin))
		allocCtx, allocCancel = chromedp.NewExecAllocator(ctx, opts...)
	} else {
		allocCtx, allocCancel = chromedp.NewExecAllocator(ctx, chromedp.DefaultExecAllocatorOptions[:]...)
	}
	defer allocCancel()
	cctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	var mu sync.Mutex
	var results []*QueryPayloadResult
	seen := map[string]bool{}

	chromedp.ListenTarget(cctx, func(ev interface{}) {
		if e, ok := ev.(*network.EventRequestWillBeSent); ok {
			if strings.Contains(e.Request.URL, "/pathfinder/v2/query") && strings.ToUpper(e.Request.Method) == "POST" {
				// fetch post data asynchronously using the request ID
				go func(reqID network.RequestID, headers network.Headers) {
					pd, err := network.GetRequestPostData(reqID).Do(cctx)
					if err != nil || pd == "" {
						return
					}
					processPostData(reqID.String(), pd, &mu, &results, seen, headers)
				}(e.RequestID, e.Request.Headers)
			}
		}
	})

	// navigate and trigger reload to ensure requests fire
	actions := []chromedp.Action{
		chromedp.Navigate(pageURL),
		chromedp.Sleep(700 * time.Millisecond),
		chromedp.Reload(),
		chromedp.Sleep(1500 * time.Millisecond),
	}
	if err := chromedp.Run(cctx, actions...); err != nil {
		return nil, err
	}

	// wait a little more to allow background handlers to record requests
	time.Sleep(1200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(results) == 0 {
		return nil, errors.New("no matching pathfinder queries found")
	}
	return results, nil
}

func processPostData(requestID string, postData string, mu *sync.Mutex, results *[]*QueryPayloadResult, seen map[string]bool, headers network.Headers) {
	// attempt to parse JSON body
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(postData), &payload); err != nil {
		// cannot parse; ignore
		return
	}
	op, _ := payload["operationName"].(string)
	if op == "" {
		return
	}
	if op != "getTrack" && op != "fetchPlaylistMetadata" {
		return
	}

	// dedupe by request id or sha
	key := requestID
	mu.Lock()
	if seen[key] {
		mu.Unlock()
		return
	}
	seen[key] = true
	mu.Unlock()

	var vars map[string]interface{}
	if v, ok := payload["variables"].(map[string]interface{}); ok {
		vars = v
	}

	var ext map[string]interface{}
	if e, ok := payload["extensions"].(map[string]interface{}); ok {
		ext = e
	}

	var pq *PersistedQueryInfo
	if ext != nil {
		if pqiRaw, ok := ext["persistedQuery"].(map[string]interface{}); ok {
			var p PersistedQueryInfo
			if ver, ok := pqiRaw["version"].(float64); ok {
				p.Version = int(ver)
			}
			if h, ok := pqiRaw["sha256Hash"].(string); ok {
				p.Sha256Hash = h
			}
			pq = &p
		}
	}

	var appVersion string
	for k, v := range headers {
		if strings.ToLower(k) == "spotify-app-version" {
			if vs, ok := v.(string); ok {
				appVersion = vs
			}
		}
	}

	qp := &QueryPayloadResult{
		OperationName:     op,
		Variables:         vars,
		Extensions:        ext,
		PersistedQuery:    pq,
		SpotifyAppVersion: appVersion,
		RequestID:         requestID,
		RawPayload:        payload,
	}

	mu.Lock()
	*results = append(*results, qp)
	mu.Unlock()
}