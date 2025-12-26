package internal

import (
	"context"
	"encoding/json"
	"errors"
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

// Funkcja uproszczona – nie używamy requestu HTTP
func GetSpotifyQueryResultFromRequest(
	ctx context.Context,
	browser *Browser,
	_ interface{},
) (*QueryResult, error) {
	return GetSpotifyQueryResult(ctx, browser)
}

func GetSpotifyQueryResult(
	ctx context.Context,
	browser *Browser,
) (*QueryResult, error) {

	if !browser.IsHealthy() {
		return nil, errors.New("browser not healthy")
	}

	tabCtx, cancel := chromedp.NewContext(browser.allocCtx)
	defer cancel()

	timeoutCtx, timeoutCancel := context.WithTimeout(tabCtx, 40*time.Second)
	defer timeoutCancel()

	var (
		mu             sync.Mutex
		requestIDs     []network.RequestID
		appVersion     string
		payloadVersion string
	)

	// Zbieramy wszystkie requesty /pathfinder/v2/query
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
					if s, ok := v.(string); ok && s != "" {
						appVersion = s
						break
					}
				}
			}
		}
		mu.Unlock()
	})

	// Popularny track – generuje dużo requestów
	trackURL := "https://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT"

	tasks := chromedp.Tasks{
		network.Enable(),
		chromedp.Navigate(trackURL),
		chromedp.Sleep(1 * time.Second),

		// klik Play
		chromedp.Evaluate(
			`document.querySelector("button[data-testid='play-button']")?.click()`,
			nil,
		),

		// czekamy aż Spotify wyśle requesty
		chromedp.Sleep(3 * time.Second),

		// refresh strony
		chromedp.Reload(),
		chromedp.Sleep(3 * time.Second),
	}

	if err := chromedp.Run(timeoutCtx, tasks); err != nil {
		return nil, err
	}

	var (
		operations []OperationHash
		seen       = make(map[string]struct{})
	)

	// Przetwarzamy WSZYSTKIE zebrane requesty
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
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		operations = append(operations, OperationHash{
			Operation: opName,
			Hash:      hash,
		})
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
