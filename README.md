# Spotokn

A simple Spotify Token Service that gives you access tokens for the Spotify Web API. Works with anonymous (guest) and authenticated tokens using cookies.

## Features

- Automatically fetches Spotify access tokens using a headless browser
- Supports both anonymous and authenticated tokens
- Fast token refresh before they expire
- Built-in caching for better performance
- Uses only 15-20 MB of memory
- Health check endpoint to monitor service status
- Easy setup with Docker containerization

## Quick Start

1. **Run with Docker** (Easiest way):
   ```bash
   docker run -p 8080:8080 ghcr.io/botxlab/spotokn:latest
   ```

2. **Or use Docker Compose**:
   ```bash
   docker compose -f docker/docker-compose.yml up -d
   ```

3. **Get your token**:
   ```bash
   curl http://localhost:8080/api/token
   ```

That's it! Your service is running on port 8080.

## API Endpoints

- `GET /api/token` - Get an access token
  - For authenticated tokens: include the `sp_dc` cookie from spotify.com in your request
  - Returns JSON with token details and expiration time
- `GET /api/token?debug=true` - Get detailed service status and debug information
- `GET /health` - Simple health check (returns "OK" if service is healthy)

## Hosting and Deployment

Spotokn is lightweight and uses caching to store tokens in memory, keeping memory usage at 15-20 MB. No database needed - everything runs in memory!

### Option 1: Docker (Recommended)

#### Pull and Run Pre-built Image

```bash
docker run -p 8080:8080 -d ghcr.io/botxlab/spotokn:latest
```

#### Or with Docker Compose
```bash
docker compose -f docker/docker-compose.yml up -d
```

#### To Stop
```bash
docker compose -f docker/docker-compose.yml down
```

#### View Logs
```bash
docker compose -f docker/docker-compose.yml logs -f
```

### Option 2: Build and Run from Source

#### Prerequisites
- Go 1.25 or higher
- Chrome or Chromium browser installed

#### Local Development
```bash
make run
```
Service starts on port 8080.

#### Build Image Manually
```bash
make docker-build
```

#### Push to Registry
```bash
make docker-push
```

## Configuration

### Environment Variables

- `PORT` - Port to run the service on (default: 8080)
- `HEADLESS` - Run browser in headless mode (default: true in Docker)

### Caching Info

- Tokens are cached in memory for faster responses
- Anonymous tokens are refreshed automatically before expiration
- Invalid tokens are automatically replaced
- Memory usage stays low at 15-20 MB total

## Usage Examples

### Get Anonymous Token
```bash
curl http://localhost:8080/api/token
```

### Get Authenticated Token
First, get the `sp_dc` cookie from [spotify.com](https://spotify.com) (log in, then check browser cookies).

Then use it:
```bash
curl --cookie "sp_dc=YOUR_SP_DC_COOKIE_HERE" http://localhost:8080/api/token
```

### Check Service Status
```bash
curl "http://localhost:8080/api/token?debug=true"
```

### Health Check
```bash
curl http://localhost:8080/health
```

**Response Examples:**

Token Response:
```json
{
    "accessToken":"BQD..",
    "accessTokenExpirationTimestampMs":1765117919348,
    "clientId":"d8..",
    "isAnonymous":true
}
```

Debug Response shows service status like token validity and expiration times.
