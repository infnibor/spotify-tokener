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
	waitTime      time.Duration // How long to wait after navigation
}

var metadataConfigs = map[string]metadataConfig{
	"playlist": {
		operationName: "fetchPlaylist",
		url:           "https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M",
		waitTime:      2 * time.Second,
	},
	"playlistMetadata": {
		operationName: "fetchPlaylistMetadata",
		url:           "https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M",
		waitTime:      1 * time.Second,
	},
	"track": {
		operationName: "getTrack",
		url:           "https://open.spotify.com/track/3n3Ppam7vgaVa1iaRUc9Lp",
		waitTime:      2 * time.Second,
	},
	"album": {
		operationName: "getAlbum",
		url:           "https://open.spotify.com/album/1DFixLWuPkv3KT3TnV35m3",
		waitTime:      2 * time.Second,
	},
	"trackRecommender": {
		operationName: "internalLinkRecommenderTrack",
		url:           "https://open.spotify.com/track/3n3Ppam7vgaVa1iaRUc9Lp",
		waitTime:      3 * time.Second, // Slightly longer for recommendations to load
	},
	"similarAlbums": {
		operationName: "similarAlbumsBasedOnThisTrack",
		url:           "https://open.spotify.com/track/3n3Ppam7vgaVa1iaRUc9Lp",
		waitTime:      3 * time.Second,
	},
	"artistOverview": {
		operationName: "queryArtistOverview",
		url:           "https://open.spotify.com/artist/0C0XlULifJtAgn6ZNCW2eu", // The Killers
		waitTime:      3 * time.Second,
	},
}

type capturedRequest struct {
	requestID         network.RequestID
	spotifyAppVersion string
}

type MetadataService struct {
	logger      *Logger
	mu          sync.RWMutex
	cache       map[string]*MetadataResult
	cacheTTL    time.Duration
	cacheTime   map[string]time.Time
	allocCtx    context.Context
	allocCancel context.CancelFunc
}

func NewMetadataService(logger *Logger) *MetadataService {
	ms := &MetadataService{
		logger:    logger,
		cache:     make(map[string]*MetadataResult),
		cacheTime: make(map[string]time.Time),
		cacheTTL:  30 * time.Minute,
	}

	// Initialize browser allocator once for reuse
	ms.initializeBrowser()

	return ms
}

func (ms *MetadataService) initializeBrowser() {
	browserBin := os.Getenv("METADATA_BROWSER_BIN")
	if browserBin == "" {
		browserBin = os.Getenv("HASH_BROWSER_BIN")
	}

	opts := append([]chromedp.ExecAllocatorOption{},
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-background-timer-throttling", true),
		chromedp.Flag("disable-backgrounding-occluded-windows", true),
		chromedp.Flag("disable-renderer-backgrounding", true),
		chromedp.Flag("headless", true),
		chromedp.WindowSize(1920, 1080),
	)

	if browserBin != "" {
		ms.logger.Infof("Using custom browser for metadata: %s", browserBin)
		opts = append(opts, chromedp.ExecPath(browserBin))
	}

	ms.allocCtx, ms.allocCancel = chromedp.NewExecAllocator(context.Background(), opts...)
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
		supported := make([]string, 0, len(metadataConfigs))
		for k := range metadataConfigs {
			supported = append(supported, k)
		}
		return nil, fmt.Errorf("unsupported metadata type: %s (supported: %v)", metadataType, supported)
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
	browserCtx, browserCancel := chromedp.NewContext(ms.allocCtx)
	defer browserCancel()

	// Set timeout for the entire operation
	timeoutCtx, timeoutCancel := context.WithTimeout(browserCtx, 30*time.Second)
	defer timeoutCancel()

	var mu sync.Mutex
	capturedRequests := make([]capturedRequest, 0)

	// Listen for network requests
	chromedp.ListenTarget(timeoutCtx, func(ev interface{}) {
		if e, ok := ev.(*network.EventRequestWillBeSent); ok {
			if strings.Contains(e.Request.URL, "/pathfinder/v2/query") && e.Request.Method == "POST" {
				mu.Lock()
				defer mu.Unlock()

				req := capturedRequest{
					requestID: e.RequestID,
				}

				// Extract Spotify app version from headers
				for k, v := range e.Request.Headers {
					if strings.ToLower(k) == "spotify-app-version" {
						if vs, ok := v.(string); ok {
							req.spotifyAppVersion = vs
						}
					}
				}

				capturedRequests = append(capturedRequests, req)
				ms.logger.Debugf("Captured pathfinder request #%d", len(capturedRequests))
			}
		}
	})

	ms.logger.Infof("Navigating to: %s", config.url)

	// Simple navigation and wait - no complex interactions needed
	tasks := []chromedp.Action{
		chromedp.Navigate(config.url),
		chromedp.Sleep(config.waitTime),
	}

	if err := chromedp.Run(timeoutCtx, tasks...); err != nil {
		ms.logger.Errorf("Browser navigation failed for %s: %v", config.operationName, err)
		return nil, fmt.Errorf("navigation failed: %w", err)
	}

	// Give a bit more time to capture all requests
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	requests := make([]capturedRequest, len(capturedRequests))
	copy(requests, capturedRequests)
	mu.Unlock()

	if len(requests) == 0 {
		ms.logger.Errorf("No pathfinder requests captured for operation: %s", config.operationName)
		return nil, errors.New("failed to capture any metadata requests")
	}

	ms.logger.Infof("Captured %d pathfinder requests, analyzing...", len(requests))

	// Try each captured request to find the matching operation
	for i, req := range requests {
		ms.logger.Debugf("Analyzing request %d/%d", i+1, len(requests))

		var postData string
		err := chromedp.Run(timeoutCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			pd, err := network.GetRequestPostData(req.requestID).Do(ctx)
			if err != nil {
				return err
			}
			postData = pd
			return nil
		}))

		if err != nil {
			ms.logger.Debugf("Failed to get POST data for request %d: %v", i+1, err)
			continue
		}

		if postData == "" || !strings.Contains(postData, "sha256Hash") {
			ms.logger.Debugf("Request %d has invalid POST data", i+1)
			continue
		}

		// Extract operation name for debugging
		var name map[string]interface{}
		if err := json.Unmarshal([]byte(postData), &name); err == nil {
			if opName, ok := name["operationName"].(string); ok {
				ms.logger.Debugf("Request %d operation: %s (looking for: %s)", i+1, opName, config.operationName)
			}
		}

		// Check if this is the operation we're looking for
		if !strings.Contains(postData, config.operationName) {
			continue
		}

		// Found the right request! Parse it
		ms.logger.Infof("Found matching request for operation: %s", config.operationName)

		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(postData), &payload); err != nil {
			ms.logger.Errorf("Failed to parse POST data for %s: %v", config.operationName, err)
			continue
		}

		ext, ok := payload["extensions"].(map[string]interface{})
		if !ok {
			ms.logger.Debug("Missing extensions in payload")
			continue
		}

		pq, ok := ext["persistedQuery"].(map[string]interface{})
		if !ok {
			ms.logger.Debug("Missing persistedQuery in payload")
			continue
		}

		hash, ok := pq["sha256Hash"].(string)
		if !ok || hash == "" {
			ms.logger.Debug("Missing or invalid sha256Hash")
			continue
		}

		result := &MetadataResult{
			OperationName:     config.operationName,
			Hash:              hash,
			SpotifyAppVersion: req.spotifyAppVersion,
		}

		if version, ok := pq["version"].(float64); ok {
			result.PayloadVersion = fmt.Sprintf("%d", int(version))
		}

		ms.logger.Infof("Successfully extracted metadata for %s: hash=%s", config.operationName, hash[:12]+"...")
		return result, nil
	}

	return nil, fmt.Errorf("operation %s not found in any captured request", config.operationName)
}

func (ms *MetadataService) Cleanup() {
	ms.logger.Info("Cleaning up metadata service...")

	if ms.allocCancel != nil {
		ms.allocCancel()
	}

	ms.mu.Lock()
	ms.cache = make(map[string]*MetadataResult)
	ms.cacheTime = make(map[string]time.Time)
	ms.mu.Unlock()
}
