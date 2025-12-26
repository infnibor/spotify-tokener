package internal


import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

				       opName, _ := payload["operationName"].(string)
				       if opName == "getTrack" || opName == "fetchPlaylistMetadata" {
					       log.Printf("[INFO] Złapano żądanie %s: %+v\n", opName, payload)
					       results = append(results, payload)
				       }
			       }

			       if len(results) == 0 {
				       log.Println("[ERROR] Nie znaleziono żadnych żądań getTrack/fetchPlaylistMetadata")
				       return nil, errors.New("no matching requests found")
			       }

			       // Zwróć wszystkie pełne payloady
			       return &QueryResult{
				       Operations:        nil,
				       SpotifyAppVersion: appVersion,
				       PayloadVersion:    payloadVersion,
			       }, nil
		operations = append(operations, OperationHash{
			Operation: opName,
			Hash:      hash,
		})
	}

	       if len(operations) == 0 {
		       log.Println("[ERROR] Nie znaleziono żadnych operation/hash")
		       return nil, errors.New("no query hashes found")
	       }

	       log.Printf("[INFO] Zwracam %d operation/hash\n", len(operations))
	       return &QueryResult{
		       Operations:        operations,
		       SpotifyAppVersion: appVersion,
		       PayloadVersion:    payloadVersion,
	       }, nil
}
