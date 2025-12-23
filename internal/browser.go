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
	browserTimeout = 35 * time.Second
	tokenWaitTime  = 20 * time.Second
)

type Browser struct {
	allocCtx    context.Context
	allocCancel context.CancelFunc
	logger      *Logger
	mu          sync.Mutex
	isHealthy   bool
	lastUsed    time.Time
}

func NewBrowser(logger *Logger) *Browser {
	b := &Browser{
		logger:    logger,
		isHealthy: true,
		lastUsed:  time.Now(),
	}
	b.initialize()
	return b
}

func (b *Browser) initialize() {
	b.mu.Lock()
	defer b.mu.Unlock()

	browserPath, err := findBrowser()
	if err != nil {
		b.logger.Errorf("Browser detection failed: %v", err)
		b.isHealthy = false
		return
	}

	b.logger.Infof("Using browser: %s", browserPath)

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(browserPath),
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
		chromedp.Flag("disable-web-security", true),
		chromedp.Flag("disable-features", "IsolateOrigins,site-per-process"),
		chromedp.UserAgent("Mozilla/5.0 (Linux; Android 6.0; Nexus 5 Build/MRA58N) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Mobile Safari/537.36"),
		chromedp.WindowSize(1920, 1080),
	)

	if os.Getenv("HEADLESS") != "false" {
		opts = append(opts, chromedp.Flag("headless", true))
	}

	b.allocCtx, b.allocCancel = chromedp.NewExecAllocator(context.Background(), opts...)
	b.logger.Debug("Browser allocator initialized")
}

func (b *Browser) GetToken(cookies []Cookie) (*SpotifyToken, error) {
	b.mu.Lock()
	if !b.isHealthy {
		b.mu.Unlock()
		return nil, errors.New("browser not healthy")
	}
	b.lastUsed = time.Now()
	b.mu.Unlock()

	ctx, cancel := chromedp.NewContext(b.allocCtx)
	defer cancel()

	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, browserTimeout)
	defer timeoutCancel()

	tokenChan := make(chan *SpotifyToken, 1)
	errChan := make(chan error, 1)
	tokenReceived := false
	tokenMu := sync.Mutex{}

	chromedp.ListenTarget(timeoutCtx, func(ev interface{}) {
		if resp, ok := ev.(*network.EventResponseReceived); ok {
			if contains(resp.Response.URL, "/api/token") {
				tokenMu.Lock()
				if !tokenReceived {
					tokenReceived = true
					tokenMu.Unlock()
					go b.extractToken(ctx, resp.RequestID, tokenChan, errChan)
				} else {
					tokenMu.Unlock()
				}
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

	// Navigate to Spotify
	tasks = append(tasks, chromedp.Navigate("https://open.spotify.com/"))

	if err := chromedp.Run(timeoutCtx, tasks); err != nil {
		b.logger.Errorf("Browser navigation failed: %v", err)
		return nil, err
	}

	select {
	case token := <-tokenChan:
		if token == nil {
			return nil, errors.New("received nil token")
		}
		b.logger.Debug("Token received successfully")
		return token, nil
	case err := <-errChan:
		b.logger.Errorf("Token extraction failed: %v", err)
		return nil, err
	case <-time.After(tokenWaitTime):
		b.logger.Error("Token response timeout")
		return nil, errors.New("token response timeout")
	case <-timeoutCtx.Done():
		b.logger.Errorf("Browser context timeout: %v", timeoutCtx.Err())
		return nil, timeoutCtx.Err()
	}
}

func (b *Browser) extractToken(ctx context.Context, requestID network.RequestID, tokenChan chan *SpotifyToken, errChan chan error) {
	defer func() {
		if r := recover(); r != nil {
			b.logger.Errorf("Panic in extractToken: %v", r)
			select {
			case errChan <- errors.New("token extraction panicked"):
			default:
			}
		}
	}()

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
		b.logger.Debugf("GetResponseBody error: %v", err)
		select {
		case errChan <- err:
		default:
		}
		return
	}

	if body == "" {
		b.logger.Error("Empty token response body")
		select {
		case errChan <- errors.New("empty token response"):
		default:
		}
		return
	}

	var token SpotifyToken
	if err := json.Unmarshal([]byte(body), &token); err != nil {
		b.logger.Errorf("Token JSON unmarshal error: %v, body: %s", err, body)
		select {
		case errChan <- err:
		default:
		}
		return
	}

	if token.AccessToken == "" {
		b.logger.Error("Token missing accessToken field")
		select {
		case errChan <- errors.New("invalid token: missing accessToken"):
		default:
		}
		return
	}

	select {
	case tokenChan <- &token:
	default:
		b.logger.Warn("Token channel full, dropping token")
	}
}

func (b *Browser) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.isHealthy = false
	if b.allocCancel != nil {
		b.allocCancel()
		b.allocCancel = nil
	}
	b.logger.Debug("Browser closed")
}

func (b *Browser) IsHealthy() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.isHealthy
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

func findBrowser() (string, error) {
	if p := os.Getenv("BROWSER_BIN"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	candidates := []string{
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,

		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,

		"/headless-shell/headless-shell",
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
		"/usr/bin/microsoft-edge",
		"/opt/microsoft/msedge/msedge",

		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	return "", errors.New("no supported browser found (tried Chrome and Edge; set BROWSER_BIN to override)")
}
