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

	       tasks := chromedp.Tasks{
		       network.Enable(),
		       chromedp.Navigate("https://open.spotify.com/track/5tCxCYuFA57AhVtHqxP7kr"),
		       chromedp.Sleep(2000 * time.Millisecond),
		       chromedp.Reload(),
		       chromedp.Sleep(3000 * time.Millisecond),
	       }
			       log.Println("[chromedp] SetValue: login-username")
			       return nil
		       }),
		       chromedp.SetValue(`input#login-username`, login, chromedp.ByQuery),
		       chromedp.ActionFunc(func(ctx context.Context) error {
			       log.Println("[chromedp] Check for tertiary button (Podaj hasło)")
			       return nil
		       }),
		       // Jeśli pojawi się przycisk 'Podaj hasło, aby się zalogować', kliknij go
		       chromedp.ActionFunc(func(ctx context.Context) error {
			       var exists bool
			       err := chromedp.Evaluate(`!!document.querySelector('button[data-encore-id="buttonTertiary"]')`, &exists).Do(ctx)
			       if err == nil && exists {
				       log.Println("[chromedp] Klikam: Podaj hasło, aby się zalogować")
				       return chromedp.Click(`button[data-encore-id='buttonTertiary']`, chromedp.ByQuery).Do(ctx)
			       }
			       log.Println("[chromedp] Brak przycisku Podaj hasło, przechodzę dalej")
			       return nil
		       }),
		       chromedp.ActionFunc(func(ctx context.Context) error {
			       log.Println("[chromedp] WaitVisible: login-password")
			       return nil
		       }),
		       chromedp.WaitVisible(`input[data-testid='login-password']`, chromedp.ByQuery),
		       chromedp.ActionFunc(func(ctx context.Context) error {
			       log.Println("[chromedp] SetValue: login-password")
			       return nil
		       }),
		       chromedp.SetValue(`input[data-testid='login-password']`, password, chromedp.ByQuery),
		       chromedp.ActionFunc(func(ctx context.Context) error {
			       log.Println("[chromedp] Click: login-button")
			       return nil
		       }),
		       chromedp.Click(`button[data-testid='login-button']`, chromedp.ByQuery),
		       chromedp.ActionFunc(func(ctx context.Context) error {
			       log.Println("[chromedp] Sleep: 5s na zalogowanie")
			       return nil
		       }),
		       chromedp.Sleep(5000 * time.Millisecond), // poczekaj na zalogowanie

		       chromedp.ActionFunc(func(ctx context.Context) error {
			       log.Println("[chromedp] Navigate: popularny track")
			       return nil
		       }),
		       // przejdź do popularnego tracka
		       chromedp.Navigate("https://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT"),
		       chromedp.ActionFunc(func(ctx context.Context) error {
			       log.Println("[chromedp] Sleep: 1s po tracku")
			       return nil
		       }),
		       chromedp.Sleep(1000 * time.Millisecond),

		       chromedp.ActionFunc(func(ctx context.Context) error {
			       log.Println("[chromedp] Klikam Play")
			       return nil
		       }),
		       // klik Play
		       chromedp.Evaluate(
			       `document.querySelector("button[data-testid='play-button']")?.click()`,
			       nil,
		       ),

		       chromedp.ActionFunc(func(ctx context.Context) error {
			       log.Println("[chromedp] Sleep: 3s na requesty po Play")
			       return nil
		       }),
		       // czekamy aż Spotify wyśle requesty
		       chromedp.Sleep(3000 * time.Millisecond),

		       chromedp.ActionFunc(func(ctx context.Context) error {
			       log.Println("[chromedp] Reload strony")
			       return nil
			       var (
				       results []map[string]interface{}
			       )

			       for _, reqID := range requestIDs {
				       var postData string
				       err := chromedp.Run(timeoutCtx, chromedp.ActionFunc(func(ctx context.Context) error {
					       pd, err := network.GetRequestPostData(reqID).Do(ctx)
					       if err != nil {
						       log.Printf("[WARN] Nie udało się pobrać postData dla requestID %v: %v\n", reqID, err)
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
