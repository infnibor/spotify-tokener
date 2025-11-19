import { SpotifyBrowser } from './browser';
import type { SpotifyToken, Cookie } from '../types/types';
import { logs } from '../utils/logger';

export class Spotify {
    private browser: SpotifyBrowser;
    private anonymousToken: SpotifyToken | null = null;
    private authenticatedToken: SpotifyToken | null = null;
    private proactiveRefreshTimer: NodeJS.Timeout | null = null;
    private isRefreshing = false;
    private refreshPromise: Promise<SpotifyToken | null> | null = null;
    private consecutiveFailures = 0;
    private readonly MAX_FAILURES = 3;

    private readonly PROACTIVE_REFRESH_BUFFER = 5 * 60 * 1000;
    private readonly CHECK_INTERVAL = 60 * 1000;
    private readonly RETRY_DELAY = 5000;

    constructor() {
        this.browser = new SpotifyBrowser();
        this.initializeProactiveRefresh();
        // Don't await - let it initialize in background
        this.getAnonymousToken().catch(err => {
            logs('error', 'Initial token fetch failed', err);
        });
        logs('info', 'Spotify Token Service initialized');
    }

    public async getToken(cookies?: Cookie[]): Promise<SpotifyToken | null> {
        try {
            const hasSpDcCookie = this.hasSpDcCookie(cookies);

            if (hasSpDcCookie) {
                return await this.getAuthenticatedToken(cookies!);
            } else {
                return await this.getAnonymousToken();
            }
        } catch (error) {
            logs('error', 'Failed to get token', error);
            return null;
        }
    }

    private async getAuthenticatedToken(cookies: Cookie[]): Promise<SpotifyToken | null> {
        logs('info', 'Fetching fresh authenticated token');

        try {
            const token = await this.fetchTokenWithTimeout(cookies);

            if (token && !token.isAnonymous) {
                this.authenticatedToken = token;
                this.consecutiveFailures = 0;
                logs('info', 'Successfully obtained authenticated token');
                return token;
            }

            logs('warn', 'Expected authenticated token but got anonymous');
            return token;
        } catch (error) {
            this.consecutiveFailures++;
            logs('error', `Authenticated token fetch failed (${this.consecutiveFailures}/${this.MAX_FAILURES})`, error);

            if (this.consecutiveFailures >= this.MAX_FAILURES) {
                logs('error', 'Max failures reached, reinitializing browser');
                await this.reinitializeBrowser();
            }

            return null;
        }
    }

    private async getAnonymousToken(): Promise<SpotifyToken | null> {
        // Return cached if valid
        if (this.anonymousToken && this.isTokenValid(this.anonymousToken)) {
            logs('debug', 'Returning cached anonymous token');
            return this.anonymousToken;
        }

        // If already refreshing, wait for that promise
        if (this.isRefreshing && this.refreshPromise) {
            logs('info', 'Waiting for ongoing refresh');
            return await this.refreshPromise;
        }

        logs('info', 'Fetching fresh anonymous token');
        return await this.refreshAnonymousToken();
    }

    private async refreshAnonymousToken(): Promise<SpotifyToken | null> {
        // Double-check pattern to prevent race conditions
        if (this.isRefreshing && this.refreshPromise) {
            return await this.refreshPromise;
        }

        this.isRefreshing = true;

        // Store the promise so concurrent calls can await it
        this.refreshPromise = (async () => {
            try {
                const token = await this.fetchTokenWithTimeout();

                if (token?.isAnonymous) {
                    this.anonymousToken = token;
                    this.consecutiveFailures = 0;
                    logs('info', 'Anonymous token refreshed successfully');
                    return token;
                } else {
                    logs('warn', 'Expected anonymous token but got authenticated');
                    return token;
                }
            } catch (error) {
                this.consecutiveFailures++;
                logs('error', `Anonymous token refresh failed (${this.consecutiveFailures}/${this.MAX_FAILURES})`, error);

                if (this.consecutiveFailures >= this.MAX_FAILURES) {
                    logs('error', 'Max failures reached, reinitializing browser');
                    await this.reinitializeBrowser();
                }

                return null;
            } finally {
                this.isRefreshing = false;
                this.refreshPromise = null;
            }
        })();

        return await this.refreshPromise;
    }

    private async fetchTokenWithTimeout(cookies?: Cookie[]): Promise<SpotifyToken | null> {
        const TIMEOUT = 20000; // 20 seconds

        const timeoutPromise = new Promise<never>((_, reject) => {
            setTimeout(() => reject(new Error('Token fetch timeout')), TIMEOUT);
        });

        try {
            return await Promise.race([
                this.browser.getToken(cookies),
                timeoutPromise
            ]);
        } catch (error) {
            if (error instanceof Error && error.message === 'Token fetch timeout') {
                logs('error', 'Token fetch timed out, browser may be stuck');
                // Try to recover
                await this.reinitializeBrowser();
            }
            throw error;
        }
    }

    private async reinitializeBrowser(): Promise<void> {
        logs('warn', 'Reinitializing browser instance');
        try {
            await this.browser.close();
            this.browser = new SpotifyBrowser();
            this.consecutiveFailures = 0;
            logs('info', 'Browser reinitialized successfully');
        } catch (error) {
            logs('error', 'Failed to reinitialize browser', error);
            throw error;
        }
    }

    private initializeProactiveRefresh(): void {
        const checkAndRefresh = async () => {
            try {
                if (this.anonymousToken && !this.isRefreshing) {
                    const timeUntilExpiry = this.anonymousToken.accessTokenExpirationTimestampMs - Date.now();

                    if (timeUntilExpiry <= this.PROACTIVE_REFRESH_BUFFER) {
                        logs('info', `Anonymous token expires in ${Math.round(timeUntilExpiry / 1000 / 60)} minutes - proactively refreshing`);
                        await this.refreshAnonymousToken();
                    }
                }
            } catch (error) {
                logs('error', 'Proactive refresh check failed', error);
            }

            // Schedule next check
            this.proactiveRefreshTimer = setTimeout(checkAndRefresh, this.CHECK_INTERVAL);
        };

        // Start the refresh loop
        this.proactiveRefreshTimer = setTimeout(checkAndRefresh, this.CHECK_INTERVAL);
        logs('info', 'Proactive refresh scheduler started');
    }

    private hasSpDcCookie(cookies?: Cookie[]): boolean {
        return cookies?.some(cookie => cookie.name === 'sp_dc') || false;
    }

    private isTokenValid(token: SpotifyToken): boolean {
        return token.accessTokenExpirationTimestampMs > Date.now();
    }

    public async cleanup(): Promise<void> {
        logs('info', 'Starting cleanup...');

        if (this.proactiveRefreshTimer) {
            clearTimeout(this.proactiveRefreshTimer);
            this.proactiveRefreshTimer = null;
        }

        // Wait for any ongoing refresh to complete
        if (this.refreshPromise) {
            try {
                await Promise.race([
                    this.refreshPromise,
                    new Promise(resolve => setTimeout(resolve, 5000))
                ]);
            } catch (error) {
                logs('warn', 'Refresh promise cleanup error', error);
            }
        }

        await this.browser.close();
        this.anonymousToken = null;
        this.authenticatedToken = null;
        logs('info', 'Cleanup completed');
    }

    public getStatus() {
        return {
            hasAnonymousToken: !!this.anonymousToken,
            hasAuthenticatedToken: !!this.authenticatedToken,
            isRefreshing: this.isRefreshing,
            consecutiveFailures: this.consecutiveFailures,
            anonymousTokenExpiry: this.anonymousToken?.accessTokenExpirationTimestampMs,
            authenticatedTokenExpiry: this.authenticatedToken?.accessTokenExpirationTimestampMs,
            anonymousTokenValid: this.anonymousToken ? this.isTokenValid(this.anonymousToken) : false,
            authenticatedTokenValid: this.authenticatedToken ? this.isTokenValid(this.authenticatedToken) : false,
            timeUntilAnonymousExpiry: this.anonymousToken
                ? Math.max(0, Math.round((this.anonymousToken.accessTokenExpirationTimestampMs - Date.now()) / 1000))
                : 0,
        };
    }
}