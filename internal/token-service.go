package internal

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	proactiveRefreshBuffer = 3 * time.Minute // Refresh 3 minutes before expiry
	checkInterval          = 45 * time.Second // Check less frequently to avoid stacking
	maxFailures            = 5
	tokenTimeout           = 25 * time.Second
	minTokenLifetime       = 30 * time.Second 
)

type TokenService struct {
	browser              *Browser
	anonymousToken       *SpotifyToken
	authenticatedToken   *SpotifyToken
	logger               *Logger
	mu                   sync.RWMutex
	refreshMu            sync.Mutex
	isRefreshing         bool
	lastRefreshAttempt   time.Time
	consecutiveFailures  int
	proactiveRefreshStop chan struct{}
	initialized          bool
}

func NewTokenService(logger *Logger) *TokenService {
	ts := &TokenService{
		browser:              NewBrowser(logger),
		logger:               logger,
		proactiveRefreshStop: make(chan struct{}),
		lastRefreshAttempt:   time.Now().Add(-1 * time.Minute),
	}

	ts.initializeProactiveRefresh()

	initCtx, initCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer initCancel()

	maxRetries := 3
	for i := range maxRetries {
		select {
		case <-initCtx.Done():
			logger.Warn("Initialization cancelled or timed out")
		default:
		}
		if token, err := ts.refreshAnonymousToken(); err == nil && token != nil {
			ts.initialized = true
			logger.Info("Service initialized with token")
			break
		} else {
			logger.Warnf("Initial token fetch attempt %d/%d failed: %v", i+1, maxRetries, err)
			if i < maxRetries-1 {
				select {
				case <-initCtx.Done():
					logger.Warn("Initialization cancelled during backoff")
					break
				case <-time.After(time.Duration(i+1) * 2 * time.Second):
				}
			}
		}
	}

	if !ts.initialized {
		logger.Warn("Service started without initial token, will retry in background")
	}

	return ts
}

func (ts *TokenService) GetToken(cookies []Cookie) (*SpotifyToken, error) {
	hasSpDc := false
	for _, c := range cookies {
		if c.Name == "sp_dc" {
			hasSpDc = true
			break
		}
	}

	if hasSpDc {
		return ts.getAuthenticatedToken(cookies)
	}
	return ts.getAnonymousToken()
}

func (ts *TokenService) getAuthenticatedToken(cookies []Cookie) (*SpotifyToken, error) {
	token, err := ts.fetchTokenWithTimeout(cookies)
	if err != nil {
		ts.mu.Lock()
		ts.consecutiveFailures++
		failures := ts.consecutiveFailures
		ts.mu.Unlock()

		ts.logger.Warnf("Authenticated token fetch failed (failures: %d): %v", failures, err)

		if failures >= maxFailures {
			ts.reinitializeBrowser()
		}
		return nil, err
	}

	if token != nil && !token.IsAnonymous {
		ts.mu.Lock()
		ts.authenticatedToken = token
		ts.consecutiveFailures = 0
		ts.mu.Unlock()
		return token, nil
	}

	return token, nil
}

func (ts *TokenService) getAnonymousToken() (*SpotifyToken, error) {
	ts.mu.RLock()
	cachedToken := ts.anonymousToken
	ts.mu.RUnlock()

	if cachedToken != nil && ts.hasMinimumLifetime(cachedToken) {
		return cachedToken, nil
	}

	newToken, err := ts.refreshAnonymousToken()
	if err != nil {
		ts.logger.Warnf("Token refresh failed: %v", err)
		if cachedToken != nil && ts.isTokenValid(cachedToken) {
			ts.logger.Info("Using cached token as fallback")
			return cachedToken, nil
		}
		return nil, err
	}

	return newToken, nil
}

func (ts *TokenService) refreshAnonymousToken() (*SpotifyToken, error) {
	ts.refreshMu.Lock()
	defer ts.refreshMu.Unlock()

	ts.mu.RLock()
	cachedToken := ts.anonymousToken
	isRefreshing := ts.isRefreshing
	lastAttempt := ts.lastRefreshAttempt
	ts.mu.RUnlock()

	if cachedToken != nil && ts.hasMinimumLifetime(cachedToken) {
		return cachedToken, nil
	}

	if isRefreshing {
		ts.logger.Debug("Another refresh in progress, waiting...")
		time.Sleep(500 * time.Millisecond) 
		ts.mu.RLock()
		token := ts.anonymousToken
		ts.mu.RUnlock()
		if token != nil && ts.isTokenValid(token) {
			return token, nil
		}
	}

	if time.Since(lastAttempt) < 5*time.Second {
		ts.logger.Debug("Refresh attempted too soon, using cached token")
		if cachedToken != nil && ts.isTokenValid(cachedToken) {
			return cachedToken, nil
		}
		return nil, errors.New("refresh rate limited")
	}

	ts.mu.Lock()
	ts.isRefreshing = true
	ts.lastRefreshAttempt = time.Now()
	ts.mu.Unlock()

	defer func() {
		ts.mu.Lock()
		ts.isRefreshing = false
		ts.mu.Unlock()
	}()

	ts.logger.Debug("Starting token refresh...")
	token, err := ts.fetchTokenWithTimeout(nil)
	if err != nil {
		ts.mu.Lock()
		ts.consecutiveFailures++
		failures := ts.consecutiveFailures
		ts.mu.Unlock()

		ts.logger.Errorf("Token fetch failed (failures: %d): %v", failures, err)

		if failures >= maxFailures {
			go ts.reinitializeBrowser()
		}
		
		return nil, err
	}

	if token != nil && token.IsAnonymous {
		ts.mu.Lock()
		ts.anonymousToken = token
		ts.consecutiveFailures = 0
		ts.mu.Unlock()
		
		expiry := time.UnixMilli(token.AccessTokenExpirationTimestampMs)
		ts.logger.Infof("Token refreshed successfully (expires in %.1f minutes)", time.Until(expiry).Minutes())
		return token, nil
	}

	return nil, errors.New("invalid token received")
}

func (ts *TokenService) fetchTokenWithTimeout(cookies []Cookie) (*SpotifyToken, error) {
	resultChan := make(chan *SpotifyToken, 1)
	errChan := make(chan error, 1)
	done := make(chan struct{})

	go func() {
		defer func() {
			if r := recover(); r != nil {
				ts.logger.Errorf("Panic in token fetch: %v", r)
				select {
				case errChan <- errors.New("token fetch panicked"):
				case <-done:
				}
			}
		}()

		token, err := ts.browser.GetToken(cookies)
		if err != nil {
			select {
			case errChan <- err:
			case <-done:
			}
			return
		}
		select {
		case resultChan <- token:
		case <-done:
		}
	}()

	select {
	case token := <-resultChan:
		close(done)
		return token, nil
	case err := <-errChan:
		close(done)
		return nil, err
	case <-time.After(tokenTimeout):
		close(done)
		ts.logger.Error("Token fetch timeout, reinitializing browser")
		go ts.reinitializeBrowser()
		return nil, errors.New("token fetch timeout")
	}
}

func (ts *TokenService) reinitializeBrowser() {
	ts.logger.Warn("Reinitializing browser...")

	if ts.browser != nil {
		ts.browser.Close()
	}

	time.Sleep(500 * time.Millisecond)

	ts.browser = NewBrowser(ts.logger)

	ts.mu.Lock()
	ts.consecutiveFailures = 0
	ts.mu.Unlock()

	ts.logger.Info("Browser reinitialized")
}

func (ts *TokenService) initializeProactiveRefresh() {
	ticker := time.NewTicker(checkInterval)
	go func() {
		for {
			select {
			case <-ticker.C:
				ts.checkAndRefresh()
			case <-ts.proactiveRefreshStop:
				ticker.Stop()
				return
			}
		}
	}()
}

func (ts *TokenService) checkAndRefresh() {
	ts.mu.RLock()
	token := ts.anonymousToken
	isRefreshing := ts.isRefreshing
	lastAttempt := ts.lastRefreshAttempt
	ts.mu.RUnlock()

	if isRefreshing || time.Since(lastAttempt) < 10*time.Second {
		return
	}

	if token != nil {
		timeUntilExpiry := time.Until(time.UnixMilli(token.AccessTokenExpirationTimestampMs))
		
		if timeUntilExpiry > 0 && timeUntilExpiry <= proactiveRefreshBuffer {
			minutes := timeUntilExpiry.Minutes()
			ts.logger.Infof("Token expiring soon (%.1f minutes), refreshing...", minutes)
			
			go func() {
				if _, err := ts.refreshAnonymousToken(); err != nil {
					ts.logger.Warnf("Proactive refresh failed: %v", err)
				}
			}()
		}
	}
}

func (ts *TokenService) isTokenValid(token *SpotifyToken) bool {
	return token != nil && token.AccessTokenExpirationTimestampMs > time.Now().UnixMilli()
}

func (ts *TokenService) hasMinimumLifetime(token *SpotifyToken) bool {
	if token == nil {
		return false
	}
	expiryTime := time.UnixMilli(token.AccessTokenExpirationTimestampMs)
	return time.Until(expiryTime) > minTokenLifetime
}

func (ts *TokenService) Cleanup() {
	ts.logger.Info("Cleaning up token service...")
	close(ts.proactiveRefreshStop)
	time.Sleep(200 * time.Millisecond)

	if ts.browser != nil {
		ts.browser.Close()
	}

	ts.mu.Lock()
	ts.anonymousToken = nil
	ts.authenticatedToken = nil
	ts.mu.Unlock()
}

func (ts *TokenService) GetStatus() map[string]interface{} {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	status := map[string]interface{}{
		"initialized":            ts.initialized,
		"hasAnonymousToken":      ts.anonymousToken != nil,
		"hasAuthenticatedToken":  ts.authenticatedToken != nil,
		"isRefreshing":           ts.isRefreshing,
		"consecutiveFailures":    ts.consecutiveFailures,
		"anonymousTokenValid":    ts.isTokenValid(ts.anonymousToken),
		"authenticatedTokenValid": ts.isTokenValid(ts.authenticatedToken),
	}

	if ts.anonymousToken != nil {
		expiryMs := ts.anonymousToken.AccessTokenExpirationTimestampMs
		status["anonymousTokenExpiry"] = expiryMs
		timeUntil := time.Until(time.UnixMilli(expiryMs))
		status["timeUntilAnonymousExpiry"] = int(timeUntil.Seconds())
		status["timeUntilAnonymousExpiryMinutes"] = timeUntil.Minutes()
	} else {
		status["timeUntilAnonymousExpiry"] = 0
		status["timeUntilAnonymousExpiryMinutes"] = 0.0
	}

	if ts.authenticatedToken != nil {
		status["authenticatedTokenExpiry"] = ts.authenticatedToken.AccessTokenExpirationTimestampMs
		timeUntil := time.Until(time.UnixMilli(ts.authenticatedToken.AccessTokenExpirationTimestampMs))
		status["timeUntilAuthenticatedExpiry"] = int(timeUntil.Seconds())
	}

	status["lastRefreshAttempt"] = ts.lastRefreshAttempt.Format(time.RFC3339)

	return status
}
