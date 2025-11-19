import Elysia from "elysia";
import { Logestic } from "logestic";
import { Spotify } from "./services/spotify";
import { TokenController } from "./controllers/token";
import { ErrorMiddleware } from "./middleware/error";
import { logs } from "./utils/logger";


const SERVER_PORT = parseInt(process.env.PORT || '8080', 10);

class ApplicationServer {
    private readonly app: Elysia;
    public readonly tokenService: Spotify;
    private readonly tokenController: TokenController;

    constructor() {
        this.tokenService = new Spotify();
        this.tokenController = new TokenController(this.tokenService);
        this.app = new Elysia()
            .use(Logestic.preset('common'))
            .decorate('tokenController', this.tokenController)
            .get('/api/token', async ({ query, headers, set, tokenController }: { query: { force?: string; debug?: string }, headers: { cookie?: string }, set: any, tokenController: TokenController }) => {
                const cookies = this.parseCookieHeader(headers.cookie);
                return await tokenController.handle(query, cookies, (status) => {
                    set.status = status;
                });
            })
            .get('/health', () => ({
                status: 'healthy',
                timestamp: Date.now(),
                uptime: process.uptime(),
                version: `Bun v${Bun.version}`,
                service: 'spotify-token-service'
            }))
            .onError(({ code, error, set }) => {
                return ErrorMiddleware.handle(code, error, (status) => {
                    set.status = status;
                });
            });
    }

    private parseCookieHeader(cookieHeader?: string): Record<string, string> | undefined {
        if (!cookieHeader?.trim()) return undefined;

        const cookies: Record<string, string> = {};

        cookieHeader.split(';').forEach(cookie => {
            const [name, value] = cookie.trim().split('=');
            if (name && value) {
                cookies[name] = decodeURIComponent(value);
            }
        });

        return Object.keys(cookies).length > 0 ? cookies : undefined;
    }

    public start(): void {
        this.app.listen(SERVER_PORT, () => {
            logs('info', '🚀 Spotify Token Service Started');
            logs('info', `📡 Server: http://localhost:${SERVER_PORT}`);
            logs('info', `🎯 Token API: http://localhost:${SERVER_PORT}/api/token`);
            logs('info', `💚 Health Check: http://localhost:${SERVER_PORT}/health`);
            logs('info', `🔧 Debug Info: http://localhost:${SERVER_PORT}/api/token?debug=true`);
            logs('info', '');
            logs('info', '📋 Usage:');
            logs('info', `  • Anonymous: curl http://localhost:${SERVER_PORT}/api/token`);
            logs('info', `  • Authenticated: curl -H "Cookie: sp_dc=your_cookie" http://localhost:${SERVER_PORT}/api/token`);
        });
    }
}

const server = new ApplicationServer();
server.start();

process.on('uncaughtException', async (error) => {
    logs('error', '💥 Uncaught Exception', error);
    await server.tokenService.cleanup();
    process.exit(1);
});

process.on('unhandledRejection', async (reason) => {
    logs('error', '💥 Unhandled Rejection', reason);
    await server.tokenService.cleanup();
    process.exit(1);
});


const gracefulShutdown = async (signal: string) => {
    logs('info', `🛑 Received ${signal} - Initiating graceful shutdown...`);
    await server.tokenService.cleanup();
    logs('info', '✅ Graceful shutdown completed');
    process.exit(0);
};

process.on('SIGINT', () => gracefulShutdown('SIGINT'));
process.on('SIGTERM', () => gracefulShutdown('SIGTERM'));