package internal

import (
	"errors"
	"sync"
	"time"
)

const (
	proactiveRefreshBuffer = 2 * time.Minute // Refresh 2-3 minutes before expiry
	checkInterval          = 30 * time.Second // Check more frequently
	maxFailures            = 3
	tokenTimeout           = 20 * time.Second
)

type TokenService struct {
	browser              *Browser
	anonymousToken       *SpotifyToken
	authenticatedToken   *SpotifyToken
	logger               *Logger
	mu                   sync.RWMutex
	refreshMu            sync.Mutex
	isRefreshing         bool
	consecutiveFailures  int
	proactiveRefreshStop chan struct{}
	initialized          bool
}

func NewTokenService(logger *Logger) *TokenService {
	ts := &TokenService{
		browser:              NewBrowser(logger),
		logger:               logger,
		proactiveRefreshStop: make(chan struct{}),
	}

	ts.initializeProactiveRefresh()
	
	if _, err := ts.getAnonymousToken(); err != nil {
		logger.Warn("Initial token fetch failed, will retry: " + err.Error())
	} else {
		ts.initialized = true
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

	if cachedToken != nil && ts.isTokenValid(cachedToken) {
		return cachedToken, nil
	}

	return ts.refreshAnonymousToken()
}

func (ts *TokenService) refreshAnonymousToken() (*SpotifyToken, error) {
	ts.refreshMu.Lock()
	defer ts.refreshMu.Unlock()

	ts.mu.RLock()
	cachedToken := ts.anonymousToken
	isRefreshing := ts.isRefreshing
	ts.mu.RUnlock()

	if cachedToken != nil && ts.isTokenValid(cachedToken) {
		return cachedToken, nil
	}

	if isRefreshing {
		time.Sleep(100 * time.Millisecond)
		ts.mu.RLock()
		token := ts.anonymousToken
		ts.mu.RUnlock()
		if token != nil {
			return token, nil
		}
	}

	ts.mu.Lock()
	ts.isRefreshing = true
	ts.mu.Unlock()

	defer func() {
		ts.mu.Lock()
		ts.isRefreshing = false
		ts.mu.Unlock()
	}()

	token, err := ts.fetchTokenWithTimeout(nil)
	if err != nil {
		ts.mu.Lock()
		ts.consecutiveFailures++
		failures := ts.consecutiveFailures
		ts.mu.Unlock()

		if failures >= maxFailures {
			ts.reinitializeBrowser()
		}
		return nil, err
	}

	if token != nil && token.IsAnonymous {
		ts.mu.Lock()
		ts.anonymousToken = token
		ts.consecutiveFailures = 0
		ts.mu.Unlock()
		ts.logger.Info("Token refreshed successfully")
		return token, nil
	}

	return token, nil
}

func (ts *TokenService) fetchTokenWithTimeout(cookies []Cookie) (*SpotifyToken, error) {
	resultChan := make(chan *SpotifyToken, 1)
	errChan := make(chan error, 1)

	go func() {
		token, err := ts.browser.GetToken(cookies)
		if err != nil {
			errChan <- err
			return
		}
		resultChan <- token
	}()

	select {
	case token := <-resultChan:
		return token, nil
	case err := <-errChan:
		return nil, err
	case <-time.After(tokenTimeout):
		ts.reinitializeBrowser()
		return nil, errors.New("token fetch timeout")
	}
}

func (ts *TokenService) reinitializeBrowser() {
	ts.logger.Warn("Reinitializing browser...")
	ts.browser.Close()
	ts.browser = NewBrowser(ts.logger)
	ts.mu.Lock()
	ts.consecutiveFailures = 0
	ts.mu.Unlock()
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
	ts.mu.RUnlock()

	if token != nil && !isRefreshing {
		timeUntilExpiry := time.Until(time.UnixMilli(token.AccessTokenExpirationTimestampMs))
		
		if timeUntilExpiry > 0 && timeUntilExpiry <= proactiveRefreshBuffer {
			minutes := int(timeUntilExpiry.Minutes())
			ts.logger.Info("Token expiring soon (" + string(rune(minutes+'0')) + "m), refreshing...")
			ts.refreshAnonymousToken()
		}
	}
}

func (ts *TokenService) isTokenValid(token *SpotifyToken) bool {
	return token.AccessTokenExpirationTimestampMs > time.Now().UnixMilli()
}

func (ts *TokenService) Cleanup() {
	close(ts.proactiveRefreshStop)
	time.Sleep(100 * time.Millisecond)
	ts.browser.Close()
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
		"anonymousTokenValid":    ts.anonymousToken != nil && ts.isTokenValid(ts.anonymousToken),
		"authenticatedTokenValid": ts.authenticatedToken != nil && ts.isTokenValid(ts.authenticatedToken),
	}

	if ts.anonymousToken != nil {
		status["anonymousTokenExpiry"] = ts.anonymousToken.AccessTokenExpirationTimestampMs
		timeUntil := time.Until(time.UnixMilli(ts.anonymousToken.AccessTokenExpirationTimestampMs))
		status["timeUntilAnonymousExpiry"] = int(timeUntil.Seconds())
	} else {
		status["timeUntilAnonymousExpiry"] = 0
	}

	if ts.authenticatedToken != nil {
		status["authenticatedTokenExpiry"] = ts.authenticatedToken.AccessTokenExpirationTimestampMs
	}

	return status
}