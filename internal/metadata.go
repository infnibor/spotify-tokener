package internal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

type MetadataResult struct {
	OperationName     string `json:"operationName"`
	Hash              string `json:"hash"`
	SpotifyAppVersion string `json:"spotifyAppVersion"`
	PayloadVersion    string `json:"payloadVersion"`
}

type EntityType string

const (
	EntityTrack    EntityType = "track"
	EntityPlaylist EntityType = "playlist"
	EntityAlbum    EntityType = "album"
)

func GetMetadataFromRequest(ctx context.Context, r *http.Request) (*MetadataResult, error) {
	entityType := r.URL.Query().Get("type")
	uri := r.URL.Query().Get("uri")

	if entityType == "" {
		return nil, errors.New("missing 'type' parameter (track, playlist, or album)")
	}

	return GetSpotifyMetadata(ctx, EntityType(entityType), uri)
}

func GetSpotifyMetadata(ctx context.Context, entityType EntityType, uri string) (*MetadataResult, error) {
	url, err := buildSpotifyURL(entityType, uri)
	if err != nil {
		return nil, err
	}

	allocCtx, allocCancel := createBrowserContext(ctx)
	defer allocCancel()

	browserCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	result := &MetadataResult{}
	resultChan := make(chan *MetadataResult, 1)
	errChan := make(chan error, 1)
	var mu sync.Mutex
	captured := false

	chromedp.ListenTarget(browserCtx, func(ev interface{}) {
		mu.Lock()
		if captured {
			mu.Unlock()
			return
		}
		mu.Unlock()

		if e, ok := ev.(*network.EventRequestWillBeSent); ok {
			if isGraphQLRequest(e) {
				go extractMetadata(browserCtx, e, result, resultChan, errChan, &mu, &captured)
			}
		}
	})

	if err := chromedp.Run(browserCtx, chromedp.Navigate(url)); err != nil {
		return nil, fmt.Errorf("navigation failed: %w", err)
	}

	select {
	case res := <-resultChan:
		return res, nil
	case err := <-errChan:
		return nil, err
	case <-time.After(15 * time.Second):
		return nil, errors.New("timeout waiting for GraphQL request")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func buildSpotifyURL(entityType EntityType, uri string) (string, error) {
	var id string
	if uri != "" {
		parts := strings.Split(uri, ":")
		if len(parts) >= 3 {
			id = parts[2]
		} else {
			id = uri
		}
	}

	switch entityType {
	case EntityTrack:
		if id == "" {
			id = "3n3Ppam7vgaVa1iaRUc9Lp" // Default track
		}
		return fmt.Sprintf("https://open.spotify.com/track/%s", id), nil
	case EntityPlaylist:
		if id == "" {
			id = "37i9dQZF1DXcBWIGoYBM5M" // Default playlist
		}
		return fmt.Sprintf("https://open.spotify.com/playlist/%s", id), nil
	case EntityAlbum:
		if id == "" {
			id = "5Z9iiGl2FcIfa3BMiv6OIw" // Default album
		}
		return fmt.Sprintf("https://open.spotify.com/album/%s", id), nil
	default:
		return "", fmt.Errorf("unsupported entity type: %s", entityType)
	}
}

func createBrowserContext(ctx context.Context) (context.Context, context.CancelFunc) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
	)

	if browserPath := os.Getenv("BROWSER_BIN"); browserPath != "" {
		opts = append(opts, chromedp.ExecPath(browserPath))
	}

	if os.Getenv("HEADLESS") != "false" {
		opts = append(opts, chromedp.Flag("headless", true))
	}

	return chromedp.NewExecAllocator(ctx, opts...)
}

func isGraphQLRequest(e *network.EventRequestWillBeSent) bool {
	return strings.Contains(e.Request.URL, "/pathfinder/v2/query") &&
		e.Request.Method == "POST"
}

func extractMetadata(
	ctx context.Context,
	e *network.EventRequestWillBeSent,
	result *MetadataResult,
	resultChan chan *MetadataResult,
	errChan chan error,
	mu *sync.Mutex,
	captured *bool,
) {
	mu.Lock()
	if *captured {
		mu.Unlock()
		return
	}
	*captured = true
	mu.Unlock()

	for key, value := range e.Request.Headers {
		if strings.ToLower(key) == "spotify-app-version" {
			if version, ok := value.(string); ok {
				result.SpotifyAppVersion = version
			}
			break
		}
	}

	var postData string
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		pd, err := network.GetRequestPostData(e.RequestID).Do(ctx)
		if err != nil {
			return err
		}
		postData = pd
		return nil
	}))
	if err != nil {
		errChan <- fmt.Errorf("failed to get POST data: %w", err)
		return
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(postData), &payload); err != nil {
		errChan <- fmt.Errorf("failed to parse JSON: %w", err)
		return
	}

	if opName, ok := payload["operationName"].(string); ok {
		result.OperationName = opName
	}

	if ext, ok := payload["extensions"].(map[string]interface{}); ok {
		if pq, ok := ext["persistedQuery"].(map[string]interface{}); ok {
			if hash, ok := pq["sha256Hash"].(string); ok {
				result.Hash = hash
			}

			if version, ok := pq["version"].(float64); ok {
				result.PayloadVersion = fmt.Sprintf("%d", int(version))
			}
		}
	}

	if result.OperationName == "" || result.Hash == "" {
		errChan <- errors.New("missing required fields in GraphQL request")
		return
	}

	resultChan <- result
}
