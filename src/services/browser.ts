import playwright from "playwright";
import type { Browser, LaunchOptions, BrowserContext, Page } from "playwright";
import type { SpotifyToken, Cookie } from "../types/types";
import { logs } from "../utils/logger";

export class SpotifyBrowser {
    private browser: Browser | undefined;
    private context: BrowserContext | undefined;
    private initPromise: Promise<{ browser: Browser; context: BrowserContext }> | null = null;
    private isInitializing = false;

    private async launch(): Promise<{ browser: Browser; context: BrowserContext }> {
        // If already initializing, wait for that
        if (this.isInitializing && this.initPromise) {
            return await this.initPromise;
        }

        // If browser exists and is healthy, return it
        if (this.browser && this.context) {
            try {
                if (this.browser.isConnected()) {
                    // Test if context is still valid
                    this.context.pages();
                    return { browser: this.browser, context: this.context };
                }
            } catch (error) {
                logs('warn', 'Browser/context validation failed, reinitializing', error);
                await this.forceClose();
            }
        }

        // Launch new browser
        this.isInitializing = true;
        this.initPromise = this.doLaunch();

        try {
            const result = await this.initPromise;
            this.isInitializing = false;
            return result;
        } catch (error) {
            this.isInitializing = false;
            this.initPromise = null;
            throw error;
        }
    }

    private async doLaunch(): Promise<{ browser: Browser; context: BrowserContext }> {
        try {
            const executablePath =
                process.env.BROWSER_PATH?.trim() || undefined;

            const launchOptions: LaunchOptions = {
                headless: process.env.HEADLESS !== 'false',
                args: [
                    "--disable-gpu",
                    "--disable-dev-shm-usage",
                    "--disable-setuid-sandbox",
                    "--no-sandbox",
                    "--no-zygote",
                    "--disable-extensions",
                    "--disable-background-timer-throttling",
                    "--disable-backgrounding-occluded-windows",
                    "--disable-renderer-backgrounding",
                    "--disable-software-rasterizer",
                    "--disable-blink-features=AutomationControlled",
                ],
            };

            if (executablePath) {
                launchOptions.executablePath = executablePath;
            }

            logs('info', 'Launching browser...');
            this.browser = await playwright.chromium.launch(launchOptions);

            logs('info', 'Creating browser context...');
            this.context = await this.browser.newContext({
                userAgent:
                    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
                viewport: { width: 1920, height: 1080 },
                locale: 'en-US',
            });

            // Warm up the context
            const initPage = await this.context.newPage();
            await initPage.goto("https://open.spotify.com/", {
                waitUntil: 'domcontentloaded',
                timeout: 10000,
            });
            await initPage.close();

            logs('info', 'Browser launched successfully');
            return { browser: this.browser, context: this.context };
        } catch (error) {
            await this.forceClose();
            logs('error', 'Failed to launch browser', error);
            throw error;
        }
    }

    public async getToken(cookies?: Cookie[]): Promise<SpotifyToken> {
        const { context } = await this.launch();
        let page: Page | undefined;

        try {
            page = await context.newPage();

            // Set up request interception first
            await page.route("**/*", (route) => {
                const url = route.request().url();
                const type = route.request().resourceType();

                const blockedTypes = new Set([
                    "image",
                    "stylesheet",
                    "font",
                    "media",
                    "websocket",
                    "other",
                ]);

                const blockedPatterns = [
                    "google-analytics",
                    "doubleclick.net",
                    "googletagmanager.com",
                    "https://open.spotifycdn.com/cdn/images/",
                    "https://encore.scdn.co/fonts/",
                    "facebook.com",
                    "analytics",
                ];

                if (blockedTypes.has(type) || blockedPatterns.some(p => url.includes(p))) {
                    route.abort().catch(() => { });
                    return;
                }

                route.continue().catch(() => { });
            });

            // Clear cookies and set new ones if provided
            await context.clearCookies();

            if (cookies && cookies.length > 0) {
                const cookieObjects = cookies.map((cookie) => ({
                    name: cookie.name,
                    value: cookie.value,
                    domain: ".spotify.com",
                    path: "/",
                    httpOnly: false,
                    secure: true,
                    sameSite: "Lax" as const,
                }));
                await context.addCookies(cookieObjects);
                logs('debug', `Set ${cookieObjects.length} cookie(s)`);
            }

            // Set up response listener
            const tokenPromise = new Promise<SpotifyToken>((resolve, reject) => {
                let resolved = false;

                const timeout = setTimeout(() => {
                    if (!resolved) {
                        resolved = true;
                        reject(new Error("Token fetch timeout after 15s"));
                    }
                }, 15000);

                page!.on("response", async (response) => {
                    if (resolved) return;
                    if (!response.url().includes("/api/token")) return;

                    try {
                        if (!response.ok()) {
                            throw new Error(`HTTP ${response.status()}`);
                        }

                        const text = await response.text();
                        const json = JSON.parse(text);

                        // Remove _notes field if present
                        if (json && typeof json === "object" && "_notes" in json) {
                            delete json._notes;
                        }

                        resolved = true;
                        clearTimeout(timeout);
                        resolve(json as SpotifyToken);
                    } catch (error) {
                        if (!resolved) {
                            resolved = true;
                            clearTimeout(timeout);
                            reject(error);
                        }
                    }
                });
            });

            // Navigate to Spotify
            await page.goto("https://open.spotify.com/", {
                waitUntil: 'domcontentloaded',
                timeout: 10000,
            });

            // Wait for token response
            const token = await tokenPromise;

            await page.close();
            return token;

        } catch (error) {
            if (page) {
                await page.close().catch(() => { });
            }
            logs('error', 'Token fetch error', error);
            throw error;
        }
    }

    private async forceClose(): Promise<void> {
        try {
            if (this.context) {
                await this.context.close().catch(() => { });
                this.context = undefined;
            }
            if (this.browser) {
                await this.browser.close().catch(() => { });
                this.browser = undefined;
            }
        } catch (error) {
            logs('warn', 'Error during force close', error);
        }
    }

    public async close(): Promise<void> {
        logs('info', 'Closing browser...');
        await this.forceClose();
        this.initPromise = null;
        this.isInitializing = false;
        logs('info', 'Browser closed');
    }
}