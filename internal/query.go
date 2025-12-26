package internal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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

func GetSpotifyQueryResultFromRequest(
	ctx context.Context,
	browser *Browser,
	r interface{},
) (*QueryResult, error) {

	httpReq, ok := r.(*http.Request)
	if !ok {
		return nil, errors.New("invalid request type")
	}

	playlist := httpReq.URL.Query().Get("playlist")
	return GetSpotifyQueryResult(ctx, browser, playlist)
}

func GetSpotifyQueryResult(
	ctx context.Context,
	browser *Browser,
	playlistURI string,
) (*QueryResult, error) {

	if !browser.IsHealthy() {
		return nil, errors.New("browser not healthy")
	}

	if playlistURI == "" {
		playlistURI = "spotify:playlist:37i9dQZF1DXcBWIGoYBM5M"
	}

	tabCtx, cancel := chromedp.NewContext(browser.allocCtx)
	defer cancel()

	timeoutCtx, timeoutCancel := context.WithTimeout(tabCtx, 30*time.Second)
	defer timeoutCancel()

	var (
		mu             sync.Mutex
		requestIDs     []network.RequestID
		appVersion     string
		payloadVersion string
	)

	// Rejestracja wszystkich requestów /pathfinder/v2/query
	chromedp.ListenTarget(timeoutCtx, func(ev interface{}) {
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

		if appVersion == "" {
			for k, v := range e.Request.Headers {
				if strings.ToLower(k) == "spotify-app-version" {
					if vs, ok := v.(string); ok && vs != "" {
						appVersion = vs
						break
					}
				}
			}
		}

		mu.Unlock()
	})

	playlistURL := "https://open.spotify.com/playlist/" +
		strings.TrimPrefix(playlistURI, "spotify:playlist:")

	// Symulacja odtwarzania kilku utworów
	tasks := chromedp.Tasks{
		network.Enable(),
		chromedp.Navigate(playlistURL),
		chromedp.Sleep(500 * time.Millisecond),
	}

	for i := 0; i < 5; i++ {
		tasks = append(tasks,
			chromedp.Evaluate(`document.querySelector("button[data-testid='play-button']")?.click()`, nil),
			chromedp.Sleep(50*time.Millisecond),
		)
	}

	if err := chromedp.Run(timeoutCtx, tasks); err != nil {
		return nil, err
	}

	// Poczekaj chwilę, aby wszystkie requesty zdążyły się wygenerować
	time.Sleep(2 * time.Second)

	var operations []OperationHash
	seen := make(map[string]struct{})

	// Dopiero teraz przetwarzamy wszystkie zebrane requesty
	for _, reqID := range requestIDs {
		var postData string

		err := chromedp.Run(timeoutCtx, chromedp.ActionFunc(func(ctx context.Context) error {
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

		// PayloadVersion z payloadu
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

		// SpotifyAppVersion z payloadu jeśli jeszcze nie mamy wartości
		if appVersion == "" {
			if v, ok := payload["extensions"].(map[string]interface{})["spotifyAppVersion"].(string); ok && v != "" {
				appVersion = v
			}
		}
	}

	if len(operations) == 0 {
		return nil, errors.New("no query hashes found")
	}

	return &QueryResult{
		Operations:        operations,
		SpotifyAppVersion: appVersion,
		PayloadVersion:    payloadVersion,
	}, nil
}
