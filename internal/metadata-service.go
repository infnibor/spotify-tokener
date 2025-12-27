package internal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

type metadataConfig struct {
	operationName string
	url           string
	action        string
	selector      string
}

var metadataConfigs = map[string]metadataConfig{
	"playlist": {
		operationName: "fetchPlaylist",
		url:           "https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M",
		action:        "click",
		selector:      "button[data-testid='play-button']",
	},
	"playlistMetadata": {
		operationName: "fetchPlaylistMetadata",
		url:           "https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M",
		action:        "navigate",
		selector:      "",
	},
	"track": {
		operationName: "getTrack",
		url:           "https://open.spotify.com/track/3n3Ppam7vgaVa1iaRUc9Lp",
		action:        "click",
		selector:      "button[data-testid='play-button']",
	},
	"album": {
		operationName: "getAlbum",
		url:           "https://open.spotify.com/album/1DFixLWuPkv3KT3TnV35m3",
		action:        "click",
		selector:      "button[data-testid='play-button']",
	},
	"trackRecommender": {
		operationName: "internalLinkRecommenderTrack",
		url:           "https://open.spotify.com/track/3n3Ppam7vgaVa1iaRUc9Lp",
		action:        "scroll",
		selector:      "div[data-testid='track-page']",
	},
}

type MetadataService struct {
	logger    *Logger
	mu        sync.RWMutex
	cache     map[string]*MetadataResult
	cacheTTL  time.Duration
	cacheTime map[string]time.Time
}

func NewMetadataService(logger *Logger) *MetadataService {
	return &MetadataService{
		logger:    logger,
		cache:     make(map[string]*MetadataResult),
		cacheTime: make(map[string]time.Time),
		cacheTTL:  30 * time.Minute,
	}
}

func (ms *MetadataService) GetMetadata(ctx context.Context, metadataType string) (*MetadataResult, error) {
	ms.mu.RLock()
	cached, cacheTime := ms.cache[metadataType], ms.cacheTime[metadataType]
	ms.mu.RUnlock()

	if cached != nil && time.Since(cacheTime) < ms.cacheTTL {
		ms.logger.Debugf("Returning cached metadata for type: %s", metadataType)
		return cached, nil
	}

	config, exists := metadataConfigs[metadataType]
	if !exists {
		return nil, fmt.Errorf("unsupported metadata type: %s (supported: playlist, playlistMetadata, track, album, trackRecommender)", metadataType)
	}

	ms.logger.Infof("Fetching fresh metadata for type: %s", metadataType)
	result, err := ms.fetchMetadata(ctx, config)
	if err != nil {
		return nil, err
	}

	ms.mu.Lock()
	ms.cache[metadataType] = result
	ms.cacheTime[metadataType] = time.Now()
	ms.mu.Unlock()

	return result, nil
}

func (ms *MetadataService) fetchMetadata(ctx context.Context, config metadataConfig) (*MetadataResult, error) {
	browserBin := os.Getenv("METADATA_BROWSER_BIN")
	if browserBin == "" {
		browserBin = os.Getenv("HASH_BROWSER_BIN")
	}

	var allocCtx context.Context
	var allocCancel context.CancelFunc

	if browserBin != "" {
		ms.logger.Infof("Using custom browser: %s", browserBin)
		opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(browserBin))
		allocCtx, allocCancel = chromedp.NewExecAllocator(ctx, opts...)
	} else {
		allocCtx, allocCancel = chromedp.NewExecAllocator(ctx, chromedp.DefaultExecAllocatorOptions[:]...)
	}
	defer allocCancel()

	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()

	var result MetadataResult
	var mu sync.Mutex
	var targetRequestID network.RequestID
	found := false

	chromedp.ListenTarget(browserCtx, func(ev interface{}) {
		if found {
			return
		}

		if e, ok := ev.(*network.EventRequestWillBeSent); ok {
			if strings.Contains(e.Request.URL, "/pathfinder/v2/query") && e.Request.Method == "POST" {
				mu.Lock()
				defer mu.Unlock()

				if found {
					return
				}

				for k, v := range e.Request.Headers {
					if strings.ToLower(k) == "spotify-app-version" {
						if vs, ok := v.(string); ok {
							result.SpotifyAppVersion = vs
						}
					}
				}

				targetRequestID = e.RequestID
				ms.logger.Debugf("Captured request ID for operation: %s", config.operationName)
			}
		}
	})

	ms.logger.Infof("Navigating to: %s", config.url)

	tasks := []chromedp.Action{
		chromedp.Navigate(config.url),
		chromedp.Sleep(1 * time.Second),
	}

	switch config.action {
	case "click":
		if config.selector != "" {
			tasks = append(tasks,
				chromedp.WaitVisible(config.selector, chromedp.ByQuery),
				chromedp.Sleep(500*time.Millisecond),
				chromedp.Click(config.selector, chromedp.ByQuery),
				chromedp.Sleep(1*time.Second),
			)
		}
	case "scroll":
		if config.selector != "" {
			tasks = append(tasks,
				chromedp.WaitVisible(config.selector, chromedp.ByQuery),
				chromedp.Sleep(500*time.Millisecond),
				chromedp.ScrollIntoView(config.selector, chromedp.ByQuery),
				chromedp.Sleep(2*time.Second),
			)
		}
	case "navigate":
		tasks = append(tasks, chromedp.Sleep(2*time.Second))
	}

	if err := chromedp.Run(browserCtx, tasks...); err != nil {
		ms.logger.Errorf("Browser navigation failed for %s: %v", config.operationName, err)
		return nil, fmt.Errorf("navigation failed: %w", err)
	}

	mu.Lock()
	reqID := targetRequestID
	mu.Unlock()

	if reqID == "" {
		ms.logger.Errorf("No request captured for operation: %s", config.operationName)
		return nil, errors.New("failed to capture metadata request")
	}

	var postData string
	err := chromedp.Run(browserCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		pd, err := network.GetRequestPostData(reqID).Do(ctx)
		if err != nil {
			return err
		}
		postData = pd
		return nil
	}))

	if err != nil {
		ms.logger.Errorf("Failed to get POST data for %s: %v", config.operationName, err)
		return nil, fmt.Errorf("failed to extract POST data: %w", err)
	}

	if postData == "" || !strings.Contains(postData, "sha256Hash") {
		ms.logger.Errorf("Invalid POST data for %s", config.operationName)
		return nil, errors.New("invalid or missing POST data")
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(postData), &payload); err != nil {
		ms.logger.Errorf("Failed to parse POST data for %s: %v", config.operationName, err)
		return nil, fmt.Errorf("failed to parse POST data: %w", err)
	}

	ext, ok := payload["extensions"].(map[string]interface{})
	if !ok {
		return nil, errors.New("missing extensions in payload")
	}

	pq, ok := ext["persistedQuery"].(map[string]interface{})
	if !ok {
		return nil, errors.New("missing persistedQuery in payload")
	}

	hash, ok := pq["sha256Hash"].(string)
	if !ok || hash == "" {
		return nil, errors.New("missing or invalid sha256Hash")
	}

	result.OperationName = config.operationName
	result.Hash = hash

	if version, ok := pq["version"].(float64); ok {
		result.PayloadVersion = fmt.Sprintf("%d", int(version))
	}

	ms.logger.Infof("Successfully extracted metadata for %s: hash=%s", config.operationName, hash[:12]+"...")

	return &result, nil
}

func (ms *MetadataService) Cleanup() {
	ms.logger.Info("Cleaning up metadata service...")
	ms.mu.Lock()
	ms.cache = make(map[string]*MetadataResult)
	ms.cacheTime = make(map[string]time.Time)
	ms.mu.Unlock()
}
