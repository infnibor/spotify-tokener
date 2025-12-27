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

// ParseSpotifyTypeAndID extracts type (track/playlist/album) and ID from URI or URL
func ParseSpotifyTypeAndID(input string) (string, string) {
	input = strings.TrimSpace(input)
	input = strings.SplitN(input, "?", 2)[0] // Remove query params
	if strings.HasPrefix(input, "spotify:") {
		parts := strings.Split(input, ":")
		if len(parts) == 3 {
			typeName := parts[1]
			id := parts[2]
			return typeName, id
		}
	} else if strings.Contains(input, "open.spotify.com/") {
		parts := strings.Split(input, "/")
		for i, p := range parts {
			if p == "track" || p == "playlist" || p == "album" {
				typeName := p
				if i+1 < len(parts) {
					id := parts[i+1]
					return typeName, id
				}
			}
		}
	}
	return "", ""
}
// GetSpotifyQueryResultByType handles hash fetching for track, playlist, or album with operationName
func GetSpotifyQueryResultByType(ctx context.Context, typeName, id, operationName string) (*QueryResult, error) {
       // Compose URI for navigation
       var uri, url string
       switch typeName {
       case "track":
	       uri = "spotify:track:" + id
	       url = "https://open.spotify.com/track/" + id
       case "playlist":
	       uri = "spotify:playlist:" + id
	       url = "https://open.spotify.com/playlist/" + id
       case "album":
	       uri = "spotify:album:" + id
	       url = "https://open.spotify.com/album/" + id
       default:
	       return nil, errors.New("unsupported type")
       }

       // Simulate hash manager freshness check (always fetch new for demo)
       // For track, playlist, album, use different POST payloads
       var postPayload string
       switch typeName {
       case "track":
	       postPayload = `{"variables":{"trackUri":"` + uri + `"},"operationName":"getTrack","extensions":{"persistedQuery":{"version":1,"sha256Hash":""}}}`
       case "playlist":
	       postPayload = `{"variables":{"uri":"` + uri + `"},"operationName":"fetchPlaylist","extensions":{"persistedQuery":{"version":1,"sha256Hash":""}}}`
       case "album":
	       postPayload = `{"variables":{"uri":"` + uri + `"},"operationName":"getAlbum","extensions":{"persistedQuery":{"version":1,"sha256Hash":""}}}`
       }

       // Use browser automation to fetch hash (reuse GetSpotifyQueryResult for now)
       // In real code, would use operationName and payload, but for now call GetSpotifyQueryResult
       // For playlist, call GetSpotifyQueryResult with playlist URI
       // For track/album, call with their URI (or extend logic as needed)
       switch typeName {
       case "playlist":
	       return GetSpotifyQueryResult(ctx, uri)
       case "track":
	       // For track, call GetSpotifyTrackQueryResult if exists, else fallback
	       return GetSpotifyQueryResult(ctx, uri)
       case "album":
	       return GetSpotifyQueryResult(ctx, uri)
       }
       return nil, errors.New("not implemented")
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