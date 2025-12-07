package internal

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

const (
	browserTimeout = 30 * time.Second
	tokenWaitTime  = 15 * time.Second
)

type Browser struct {
	allocCtx    context.Context
	allocCancel context.CancelFunc
	logger      *Logger
	mu          sync.Mutex
	isHealthy   bool
}

func NewBrowser(logger *Logger) *Browser {
	b := &Browser{
		logger:    logger,
		isHealthy: true,
	}
	b.initialize()
	return b
}

func (b *Browser) initialize() {
	b.mu.Lock()
	defer b.mu.Unlock()

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("no-zygote", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-background-timer-throttling", true),
		chromedp.Flag("disable-backgrounding-occluded-windows", true),
		chromedp.Flag("disable-renderer-backgrounding", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"),
		chromedp.WindowSize(1920, 1080),
	)

	if os.Getenv("HEADLESS") != "false" {
		opts = append(opts, chromedp.Flag("headless", true))
	}

	b.allocCtx, b.allocCancel = chromedp.NewExecAllocator(context.Background(), opts...)
}

func (b *Browser) GetToken(cookies []Cookie) (*SpotifyToken, error) {
	b.mu.Lock()
	if !b.isHealthy {
		b.mu.Unlock()
		return nil, errors.New("browser not healthy")
	}
	b.mu.Unlock()

	ctx, cancel := chromedp.NewContext(b.allocCtx)
	defer cancel()

	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, browserTimeout)
	defer timeoutCancel()

	tokenChan := make(chan *SpotifyToken, 1)
	errChan := make(chan error, 1)

	chromedp.ListenTarget(timeoutCtx, func(ev interface{}) {
		if resp, ok := ev.(*network.EventResponseReceived); ok {
			if contains(resp.Response.URL, "/api/token") {
				go b.extractToken(ctx, resp.RequestID, tokenChan, errChan)
			}
		}
	})

	tasks := chromedp.Tasks{}

	if len(cookies) > 0 {
		for _, c := range cookies {
			tasks = append(tasks, network.SetCookie(c.Name, c.Value).
				WithDomain(".spotify.com").
				WithPath("/").
				WithSecure(true).
				WithHTTPOnly(false).
				WithSameSite(network.CookieSameSiteLax))
		}
	}

	tasks = append(tasks, chromedp.Navigate("https://open.spotify.com/"))

	if err := chromedp.Run(timeoutCtx, tasks); err != nil {
		return nil, err
	}

	select {
	case token := <-tokenChan:
		return token, nil
	case err := <-errChan:
		return nil, err
	case <-time.After(tokenWaitTime):
		return nil, errors.New("token response timeout")
	case <-timeoutCtx.Done():
		return nil, timeoutCtx.Err()
	}
}

func (b *Browser) extractToken(ctx context.Context, requestID network.RequestID, tokenChan chan *SpotifyToken, errChan chan error) {
	var body string
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		bodyBytes, err := network.GetResponseBody(requestID).Do(ctx)
		if err != nil {
			return err
		}
		body = string(bodyBytes)
		return nil
	}))

	if err != nil {
		select {
		case errChan <- err:
		default:
		}
		return
	}

	var token SpotifyToken
	if err := json.Unmarshal([]byte(body), &token); err != nil {
		select {
		case errChan <- err:
		default:
		}
		return
	}

	select {
	case tokenChan <- &token:
	default:
	}
}

func (b *Browser) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.isHealthy = false
	if b.allocCancel != nil {
		b.allocCancel()
	}
}

func contains(s, substr string) bool {
	if len(substr) > len(s) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
