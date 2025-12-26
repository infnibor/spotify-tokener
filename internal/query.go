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

type OperationHash struct {
	Operation string `json:"operation"`
	Hash      string `json:"hash"`
}

type QueryResult struct {
	Operations        []OperationHash `json:"operations"`
	SpotifyAppVersion string          `json:"spotifyAppVersion"`
	PayloadVersion    string          `json:"payloadVersion"`
}

func GetSpotifyQueryResultFromRequest(ctx context.Context, r interface{}) (*QueryResult, error) {
	if httpReq, ok := r.(*http.Request); ok {
		playlist := httpReq.URL.Query().Get("playlist")
		return GetSpotifyQueryResult(ctx, playlist)
	}
	return nil, errors.New("invalid request type")
}

func GetSpotifyQueryResult(ctx context.Context, playlistURI string) (*QueryResult, error) {
	if playlistURI == "" {
		playlistURI = "spotify:playlist:37i9dQZF1DXcBWIGoYBM5M"
	}

	browserBin := os.Getenv("HASH_BROWSER_BIN")

	var allocCtx context.Context
	var allocCancel context.CancelFunc

	if browserBin != "" {
		opts := append(
			chromedp.DefaultExecAllocatorOptions[:],
			chromedp.ExecPath(browserBin),
		)
		allocCtx, allocCancel = chromedp.NewExecAllocator(ctx, opts...)
	} else {
		allocCtx, allocCancel = chromedp.NewExecAllocator(
			ctx,
			chromedp.DefaultExecAllocatorOptions[:]...,
		)
	}
	defer allocCancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	var (
		mu             sync.Mutex
		requestIDs     []network.RequestID
		appVersion     string
		payloadVersion string
	)

	chromedp.ListenTarget(ctx, func(ev interface{}) {
		e, ok := ev.(*network.EventRequestWillBeSent)
		if !ok {
			return
		}

		if e.Request.Method != "POST" {
			return
		}

		if !strings.Contains(e.Request.URL, "/pathfinder/v2/query") {
			return
		}

		mu.Lock()
		requestIDs = append(requestIDs, e.RequestID)

		for k, v := range e.Request.Headers {
			if strings.ToLower(k) == "spotify-app-version" {
				if vs, ok := v.(string); ok {
					appVersion = vs
				}
			}
		}
		mu.Unlock()
	})

	playlistURL := "https://open.spotify.com/playlist/" +
		strings.TrimPrefix(playlistURI, "spotify:playlist:")

	tasks := []chromedp.Action{
		network.Enable(),
		chromedp.Navigate(playlistURL),
		chromedp.Sleep(800 * time.Millisecond),
		chromedp.Click("button[data-testid='play-button']", chromedp.NodeVisible),
		chromedp.Sleep(1500 * time.Millisecond),
	}

	if err := chromedp.Run(ctx, tasks...); err != nil {
		return nil, err
	}

	var operations []OperationHash
	seen := make(map[string]struct{})

	for _, reqID := range requestIDs {
		var postData string

		err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			pd, err := network.GetRequestPostData(reqID).Do(ctx)
			if err != nil {
				return err
			}
			postData = pd
			return nil
		}))
		if err != nil || postData == "" {
			continue
		}

		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(postData), &payload); err != nil {
			continue
		}

		opName, _ := payload["operationName"].(string)

		ext, ok := payload["extensions"].(map[string]interface{})
		if !ok {
			continue
		}

		pq, ok := ext["persistedQuery"].(map[string]interface{})
		if !ok {
			continue
		}

		hash, _ := pq["sha256Hash"].(string)

		if v, ok := pq["version"].(float64); ok && payloadVersion == "" {
			payloadVersion = strconv.Itoa(int(v))
		}

		if opName == "" || hash == "" {
			continue
		}

		key := opName + ":" + hash
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}

		operations = append(operations, OperationHash{
			Operation: opName,
			Hash:      hash,
		})
	}

	if len(operations) == 0 {
		return nil, errors.New("no hashes found")
	}

	return &QueryResult{
		Operations:        operations,
		SpotifyAppVersion: appVersion,
		PayloadVersion:    payloadVersion,
	}, nil
}
