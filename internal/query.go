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
	Hashes            []OperationHash `json:"hashes"`
	SpotifyAppVersion string          `json:"spotifyAppVersion"`
	PayloadVersion    string          `json:"payloadVersion"`
}

func GetSpotifyQueryResultFromRequest(ctx context.Context, r interface{}) (*QueryResultV2, error) {
	if httpReq, ok := r.(*http.Request); ok {
		  playlist := httpReq.URL.Query().Get("playlist")
		  return GetSpotifyQueryResult(ctx, playlist)
	}
	return nil, errors.New("invalid request type")
}

// GetSpotifyQueryResult returns all info for the /api/query endpoint
func GetSpotifyQueryResult(ctx context.Context, playlistURI string) (*QueryResultV2, error) {
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
					       var allHashes []OperationHash
					       if arr, ok := payload["batch"].([]interface{}); ok {
						       for _, item := range arr {
							       if obj, ok := item.(map[string]interface{}); ok {
								       op := "getTrack"
								       if o, ok := obj["operationName"].(string); ok {
									       op = o
								       }
								       if ext, ok := obj["extensions"].(map[string]interface{}); ok {
									       if pq, ok := ext["persistedQuery"].(map[string]interface{}); ok {
										       if h, ok := pq["sha256Hash"].(string); ok {
											       allHashes = append(allHashes, OperationHash{
												       Operation: op,
												       Hash:      h,
											       })
										       }
										       if v, ok := pq["version"].(float64); ok {
											       payloadVersion = strconv.Itoa(int(v))
										       }
									       }
								       }
							       }
						       }
					       } else {
						       op := "getTrack"
						       if o, ok := payload["operationName"].(string); ok {
							       op = o
						       }
						       if ext, ok := payload["extensions"].(map[string]interface{}); ok {
							       if pq, ok := ext["persistedQuery"].(map[string]interface{}); ok {
								       if h, ok := pq["sha256Hash"].(string); ok {
									       allHashes = append(allHashes, OperationHash{
										       Operation: op,
										       Hash:      h,
									       })
								       }
								       if v, ok := pq["version"].(float64); ok {
									       payloadVersion = strconv.Itoa(int(v))
								       }
							       }
						       }
					       }
					       var ordered []OperationHash
					       for _, opName := range []string{"getTrack", "getPlaylist"} {
						       for _, h := range allHashes {
							       if h.Operation == opName {
								       ordered = append(ordered, h)
							       }
						       }
					       }
					       for _, h := range allHashes {
						       if h.Operation != "getTrack" && h.Operation != "getPlaylist" {
							       ordered = append(ordered, h)
						       }
					       }
					       hashes = ordered
				       }
			       }
		       }

		       if len(hashes) == 0 {
			       return nil, errors.New("hash not found")
		       }
		       return &QueryResultV2{
			       Hashes:            hashes,
			       SpotifyAppVersion: appVersion,
			       PayloadVersion:    payloadVersion,
		       }, nil
							       }
						       }
					       }
				       } else {
					       op := "getTrack"
					       if o, ok := payload["operationName"].(string); ok {
						       op = o
					       }
					       if ext, ok := payload["extensions"].(map[string]interface{}); ok {
						       if pq, ok := ext["persistedQuery"].(map[string]interface{}); ok {
							       if h, ok := pq["sha256Hash"].(string); ok {
								       hashes = append(hashes, OperationHash{
									       Operation: op,
									       Hash:      h,
								       })
							       }
							       if v, ok := pq["version"].(float64); ok {
								       payloadVersion = strconv.Itoa(int(v))
							       }
						       }
					       }
				       }
			       }
		       }
	       }

	       if len(hashes) == 0 {
		       return nil, errors.New("hash not found")
	       }
	       return &QueryResultV2{
		       Hashes:            hashes,
		       SpotifyAppVersion: appVersion,
		       PayloadVersion:    payloadVersion,
	       }, nil
}
