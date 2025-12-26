package internal

import (
    "encoding/json"
    "errors"
    "strconv"
    "strings"

    "github.com/chromedp/cdproto/network"
)

// ExtractQueryResultFromPayload parses a pathfinder POST body (postData)
// and headers to extract persisted query sha256 hash, spotify app version
// and payload version. Returns an error if no persisted query hash is found.
func ExtractQueryResultFromPayload(postData string, headers network.Headers) (*QueryResult, error) {
    if strings.TrimSpace(postData) == "" {
        return nil, errors.New("empty postData")
    }

    var raw interface{}
    if err := json.Unmarshal([]byte(postData), &raw); err != nil {
        return nil, err
    }

    var payload map[string]interface{}
    switch v := raw.(type) {
    case []interface{}:
        if len(v) == 0 {
            return nil, errors.New("empty payload array")
        }
        if m, ok := v[0].(map[string]interface{}); ok {
            payload = m
        } else {
            return nil, errors.New("unexpected payload element type")
        }
    case map[string]interface{}:
        payload = v
    default:
        return nil, errors.New("unsupported payload type")
    }

    var hash string
    var payloadVersion string
    if ext, ok := payload["extensions"].(map[string]interface{}); ok {
        if pq, ok := ext["persistedQuery"].(map[string]interface{}); ok {
            if h, ok := pq["sha256Hash"].(string); ok {
                hash = h
            }
            if ver, ok := pq["version"].(float64); ok {
                payloadVersion = strconv.Itoa(int(ver))
            }
        }
    }

    var appVersion string
    for k, v := range headers {
        if strings.ToLower(k) == "spotify-app-version" {
            if s, ok := v.(string); ok {
                appVersion = s
            }
        }
    }

    if hash == "" {
        return nil, errors.New("sha256Hash not found in payload")
    }

    return &QueryResult{
        Hash:              hash,
        SpotifyAppVersion: appVersion,
        PayloadVersion:    payloadVersion,
    }, nil
}
