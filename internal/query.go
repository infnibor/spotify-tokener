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

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

type QueryResult struct {
	Hash              string `json:"hash"`
	SpotifyAppVersion string `json:"spotifyAppVersion"`
	PayloadVersion    string `json:"payloadVersion"`
}

func GetSpotifyQueryResultFromRequest(ctx context.Context, r interface{}) (*QueryResult, error) {
	if httpReq, ok := r.(*http.Request); ok {
		playlist := httpReq.URL.Query().Get("playlist")
		return GetSpotifyQueryResult(ctx, playlist)
	}
	return nil, errors.New("invalid request type")
}

// GetSpotifyQueryResult returns all info for the /api/query endpoint
func GetSpotifyQueryResult(ctx context.Context, playlistURI string) (*QueryResult, error) {
	if playlistURI == "" {
		playlistURI = "spotify:playlist:37i9dQZF1DXcBWIGoYBM5M"
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
	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	var hash string
	var appVersion string
	var payloadVersion string
	var found bool
	var mu sync.Mutex
	var targetRequestID network.RequestID

	chromedp.ListenTarget(ctx, func(ev interface{}) {
		if found {
			return
		}
		if e, ok := ev.(*network.EventRequestWillBeSent); ok {
			if strings.Contains(e.Request.URL, "/pathfinder/v2/query") && e.Request.Method == "POST" {
				mu.Lock()
				targetRequestID = e.RequestID
				for k, v := range e.Request.Headers {
					if strings.ToLower(k) == "spotify-app-version" {
						if vs, ok := v.(string); ok {
							appVersion = vs
						}
					}
				}
				mu.Unlock()
			}
		}
	})

	playlistURL := "https://open.spotify.com/playlist/" + strings.TrimPrefix(playlistURI, "spotify:playlist:")

	tasks := []chromedp.Action{
		chromedp.Navigate(playlistURL),
		chromedp.Sleep(500 * time.Millisecond),
		chromedp.Click("button[data-testid='play-button']", chromedp.NodeVisible),
		chromedp.Sleep(500 * time.Millisecond),
	}
	err := chromedp.Run(ctx, tasks...)
	if err != nil {
		return nil, err
	}

	mu.Lock()
	reqID := targetRequestID
	mu.Unlock()
	if reqID != "" {
		var postData string
		err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			pd, err := network.GetRequestPostData(reqID).Do(ctx)
			if err != nil {
				return err
			}
			postData = pd
			return nil
		}))

		if err == nil && postData != "" && strings.Contains(postData, "sha256Hash") {
			var payload map[string]interface{}
			if err := json.Unmarshal([]byte(postData), &payload); err == nil {
				if ext, ok := payload["extensions"].(map[string]interface{}); ok {
					if pq, ok := ext["persistedQuery"].(map[string]interface{}); ok {
						if h, ok := pq["sha256Hash"].(string); ok {
							hash = h
							found = true
						}
						if v, ok := pq["version"].(float64); ok {
							payloadVersion = strconv.Itoa(int(v))
						}
					}
				}
			}
		}
	}

	if !found {
		return nil, errors.New("hash not found")
	}
	return &QueryResult{
		Hash:              hash,
		SpotifyAppVersion: appVersion,
		PayloadVersion:    payloadVersion,
	}, nil
}
