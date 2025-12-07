# Spotokn

Spotify Token Service that provides access tokens for the Spotify Web API. Supports both anonymous (guest) and authenticated tokens using cookies.

## Features

- Fetches Spotify access tokens automatically using headless browser
- Supports anonymous and authenticated tokens
- Proactive token refresh
- Health check endpoint
- Docker containerization

## API Endpoints

- `GET /api/token` - Get token (pass `sp_dc` cookie for authenticated access)
- `GET /api/token?debug=true` - Get service status
- `GET /health` - Health check

## Local Development

### Prerequisites

- Go 1.25+
- Chrome/Chromium browser

### Run

```bash
make run
```

Default port: 8080

## Hosting with Docker

### Build and Push Image

```bash
make docker-build
make docker-push
```

### Deploy

```bash
make docker-run
```

Or manually:

```bash
docker compose -f docker/docker-compose.yml up -d
```

### Stop

```bash
make docker-stop
```

### View Logs

```bash
make docker-logs
```

## Environment Variables

- `PORT` - Server port (default: 8080)
- `HEADLESS` - Run browser headless (Docker default)

## Usage Example

```bash
curl -X GET http://localhost:8080/api/token
```

For authenticated tokens, include the `sp_dc` cookie from spotify.com.
