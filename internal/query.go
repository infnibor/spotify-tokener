
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
	"log"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/fetch"
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
	// fallback to first result
	first := results[0]
	var hv string
	var pv string
	if first.PersistedQuery != nil {
		hv = first.PersistedQuery.Sha256Hash
		pv = strconv.Itoa(first.PersistedQuery.Version)
	}
	return &QueryResult{
		Hash:              hv,
		SpotifyAppVersion: first.SpotifyAppVersion,
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

	// enable network domain so we can retrieve request post data
	_ = chromedp.Run
	_ = network.Enable
	if err := chromedp.Run(cctx, network.Enable()); err != nil {
		log.Printf("[query] network.Enable failed: %v", err)
	}
	// enable Fetch interception for pathfinder requests to reliably access request bodies
	if err := chromedp.Run(cctx, fetch.Enable().WithPatterns([]*fetch.RequestPattern{{URLPattern: "*pathfinder/v2/query*", RequestStage: fetch.RequestStageRequest}})); err != nil {
		log.Printf("[query] fetch.Enable failed: %v", err)
	}

	chromedp.ListenTarget(cctx, func(ev interface{}) {
		// fetch interception handler
		if fev, ok := ev.(*fetch.EventRequestPaused); ok {
			if strings.Contains(fev.Request.URL, "pathfinder/v2/query") {
				ctxWithTimeout, cancelGet := context.WithTimeout(cctx, 2*time.Second)
				pd, err := network.GetRequestPostData(network.RequestID(fev.RequestID.String())).Do(ctxWithTimeout)
				cancelGet()
				if err != nil {
					log.Printf("[query] fetch.GetRequestPostData error id=%s url=%s err=%v headers=%v", fev.RequestID, fev.Request.URL, err, fev.Request.Headers)
				} else if pd != "" {
					processPostData(fev.RequestID.String(), pd, &mu, &results, seen, fev.Request.Headers)
				}
			}
			// continue the request so page can proceed
			_ = fetch.ContinueRequest(fev.RequestID).Do(cctx)
			return
		}

		if e, ok := ev.(*network.EventRequestWillBeSent); ok {
			if strings.ToUpper(e.Request.Method) == "POST" {
				log.Printf("[query] POST observed url=%s id=%s", e.Request.URL, e.RequestID)
				// Only attempt fetching bodies for pathfinder queries to reduce noise/errors
				if !strings.Contains(e.Request.URL, "pathfinder/v2/query") {
					return
				}
				// fetch post data asynchronously using the request ID with retry/backoff
				go func(reqID network.RequestID, headers network.Headers, url string) {
					var pd string
					var err error
					// retry several times — CDP can be slow to expose postData
					for i := 0; i < 6; i++ {
						ctxWithTimeout, cancelGet := context.WithTimeout(cctx, 2500*time.Millisecond)
						pd, err = network.GetRequestPostData(reqID).Do(ctxWithTimeout)
						cancelGet()
						if err == nil && pd != "" {
							processPostData(reqID.String(), pd, &mu, &results, seen, headers)
							return
						}
						// backoff between attempts
						time.Sleep(time.Duration(150+150*i) * time.Millisecond)
					}
					if err != nil {
						log.Printf("[query] GetRequestPostData error id=%s url=%s err=%v headers=%v", reqID, url, err, headers)
					} else {
						log.Printf("[query] empty postData id=%s url=%s headers=%v", reqID, url, headers)
					}
				}(e.RequestID, e.Request.Headers, e.Request.URL)
			}
		}
	})

	// navigate and trigger reload to ensure requests fire
	actions := []chromedp.Action{
		chromedp.Navigate(pageURL),
		chromedp.Sleep(4000 * time.Millisecond),
		chromedp.Reload(),
		chromedp.Sleep(4500 * time.Millisecond),
	}
	if err := chromedp.Run(cctx, actions...); err != nil {
		return nil, err
	}

	// wait a little more to allow background handlers to record requests
	time.Sleep(3500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(results) == 0 {
		return nil, errors.New("no matching pathfinder queries found")
	}
	return results, nil
}

func processPostData(requestID string, postData string, mu *sync.Mutex, results *[]*QueryPayloadResult, seen map[string]bool, headers network.Headers) {
	// dedupe by request id
	key := requestID
	mu.Lock()
	if seen[key] {
		mu.Unlock()
		return
	}
	seen[key] = true
	mu.Unlock()

	// use the shared extractor helper to parse the payload and headers
	qr, err := ExtractQueryResultFromPayload(postData, headers)
	if err != nil {
		log.Printf("[query] payload extractor error id=%s err=%v", requestID, err)
		return
	}

	// attempt to also capture operationName and raw payload for richer results
	var rawPayload map[string]interface{}
	var op string
	if err := json.Unmarshal([]byte(postData), &rawPayload); err == nil {
		if on, ok := rawPayload["operationName"].(string); ok {
			op = on
		} else if og, ok := rawPayload["operation"].(string); ok {
			op = og
		}
	}

	var ver int
	if qr.PayloadVersion != "" {
		if v, err := strconv.Atoi(qr.PayloadVersion); err == nil {
			ver = v
		}
	}

	pq := &PersistedQueryInfo{Version: ver, Sha256Hash: qr.Hash}

	qp := &QueryPayloadResult{
		OperationName:     op,
		PersistedQuery:    pq,
		SpotifyAppVersion: qr.SpotifyAppVersion,
		RequestID:         requestID,
		RawPayload:        rawPayload,
	}

	mu.Lock()
	*results = append(*results, qp)
	mu.Unlock()
}

// GetSpotifyQueryResultFromRequestWithBrowser behaves like GetSpotifyQueryResultFromRequest
// but reuses an existing Browser instance (to preserve login/session) when capturing requests.
func GetSpotifyQueryResultFromRequestWithBrowser(ctx context.Context, b *Browser, r *http.Request) (*QueryResult, error) {
	if r == nil {
		return nil, errors.New("invalid request")
	}
	q := r.URL.Query()
	val := q.Get("playlist")
	if val == "" {
		val = q.Get("uri")
	}
	if val == "" {
		val = q.Get("track")
	}
	if val == "" {
		val = q.Get("url")
	}
	if val == "" {
		val = q.Get("q")
	}
	if val == "" {
		return nil, errors.New("empty uri")
	}

	results, err := GetSpotifyQueryResultsWithBrowser(ctx, b, val)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, errors.New("hash not found")
	}
	for _, res := range results {
		if res.PersistedQuery != nil && res.PersistedQuery.Sha256Hash != "" {
			return &QueryResult{
				Hash:              res.PersistedQuery.Sha256Hash,
				SpotifyAppVersion: res.SpotifyAppVersion,
				PayloadVersion:    strconv.Itoa(res.PersistedQuery.Version),
			}, nil
		}
	}
	first := results[0]
	var hv string
	var pv string
	if first.PersistedQuery != nil {
		hv = first.PersistedQuery.Sha256Hash
		pv = strconv.Itoa(first.PersistedQuery.Version)
	}
	return &QueryResult{
		Hash:              hv,
		SpotifyAppVersion: first.SpotifyAppVersion,
		PayloadVersion:    pv,
	}, nil
}

// GetSpotifyQueryResultsWithBrowser reuses the provided Browser (shares allocator/context)
// so any existing session/cookies are available when visiting the page.
func GetSpotifyQueryResultsWithBrowser(ctx context.Context, b *Browser, spotifyURI string) ([]*QueryPayloadResult, error) {
	if spotifyURI == "" {
		return nil, errors.New("empty uri")
	}
	if b == nil || !b.IsHealthy() {
		return nil, errors.New("browser unavailable")
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

	// create a new chromedp context that shares the browser allocator
	cctx, cancel := chromedp.NewContext(b.allocCtx)
	defer cancel()

	// enable network domain on this context
	if err := chromedp.Run(cctx, network.Enable()); err != nil {
		log.Printf("[query] network.Enable failed (browser): %v", err)
	}
	// enable Fetch interception for pathfinder requests to reliably access request bodies
	if err := chromedp.Run(cctx, fetch.Enable().WithPatterns([]*fetch.RequestPattern{{URLPattern: "*pathfinder/v2/query*", RequestStage: fetch.RequestStageRequest}})); err != nil {
		log.Printf("[query] fetch.Enable failed (browser): %v", err)
	}

	var mu sync.Mutex
	var results []*QueryPayloadResult
	seen := map[string]bool{}

	chromedp.ListenTarget(cctx, func(ev interface{}) {
		// fetch interception handler
		if fev, ok := ev.(*fetch.EventRequestPaused); ok {
			if strings.Contains(fev.Request.URL, "pathfinder/v2/query") {
				ctxWithTimeout, cancelGet := context.WithTimeout(cctx, 2*time.Second)
				pd, err := network.GetRequestPostData(network.RequestID(fev.RequestID.String())).Do(ctxWithTimeout)
				cancelGet()
				if err != nil {
					log.Printf("[query] fetch.GetRequestPostData error id=%s url=%s err=%v headers=%v", fev.RequestID, fev.Request.URL, err, fev.Request.Headers)
				} else if pd != "" {
					processPostData(fev.RequestID.String(), pd, &mu, &results, seen, fev.Request.Headers)
				}
			}
			_ = fetch.ContinueRequest(fev.RequestID).Do(cctx)
			return
		}
		if e, ok := ev.(*network.EventRequestWillBeSent); ok {
			if strings.ToUpper(e.Request.Method) == "POST" {
				log.Printf("[query] POST observed url=%s id=%s", e.Request.URL, e.RequestID)
				if !strings.Contains(e.Request.URL, "pathfinder/v2/query") {
					return
				}
				go func(reqID network.RequestID, headers network.Headers, url string) {
					var pd string
					var err error
					for i := 0; i < 6; i++ {
						ctxWithTimeout, cancelGet := context.WithTimeout(cctx, 2500*time.Millisecond)
						pd, err = network.GetRequestPostData(reqID).Do(ctxWithTimeout)
						cancelGet()
						if err == nil && pd != "" {
							processPostData(reqID.String(), pd, &mu, &results, seen, headers)
							return
						}
						time.Sleep(time.Duration(150+150*i) * time.Millisecond)
					}
					if err != nil {
						log.Printf("[query] GetRequestPostData error id=%s url=%s err=%v headers=%v", reqID, url, err, headers)
					} else {
						log.Printf("[query] empty postData id=%s url=%s headers=%v", reqID, url, headers)
					}
				}(e.RequestID, e.Request.Headers, e.Request.URL)
			}
		}
	})

	actions := []chromedp.Action{
		chromedp.Navigate(pageURL),
		chromedp.Sleep(1500 * time.Millisecond),
		chromedp.Reload(),
		chromedp.Sleep(3000 * time.Millisecond),
	}
	if err := chromedp.Run(cctx, actions...); err != nil {
		return nil, err
	}

	time.Sleep(2500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(results) == 0 {
		return nil, errors.New("no matching pathfinder queries found")
	}
	return results, nil
}