# Spotify Tokener

Fast Spotify access token generator for LavaSrc with caching.

## Features

* 🚀 Fast Playwright-based token generation
* ⚡ High-performance Elysia API
* 🔄 Auto-refresh system
* 🛡️ Error resilience with retry logic
* 🛠️ Environment variables for configuratio
* 📦 Docker Compose configuration

## Quick Start

```bash
git clone https://github.com/appujet/spotokn.git
cd spotokn
bun install
npx playwright install
npx playwright install-deps
bun run start
```



## API Endpoints

* `GET /api/token` – Get token (`?force=1` to refresh)
* `GET /health` – Service health



## LavaSrc Config

```yaml
spotify:
  preferAnonymousToken: true
  customTokenEndpoint: "http://yourserver/api/token"
```



## Response Format

```json
{
  "clientId": "d8a5...",
  "accessToken": "BQD...",
  "accessTokenExpirationTimestampMs": 1761386102976,
  "isAnonymous": true
}
```



# 🐳 Docker (Recommended)

You can run **Spotify Tokener fully in Docker** using the public Docker Hub image:

👉 `appujet/spotokn:latest`



## ⭐ Using Docker Compose (Easiest)

Create a file named **docker-compose.yml**:

```yaml
version: "3.9"

services:
  spotokn:
    image: appujet/spotokn:latest
    container_name: spotokn
    restart: unless-stopped
    ports:
      - "3000:3000"
    environment:
      NODE_ENV: production
      # You can add custom env variables if needed
      # PORT=3000
```

### Start the service:

```bash
docker compose up -d
```

### Check logs:

```bash
docker compose logs -f
```

### Stop:

```bash
docker compose down
```



## Manual Docker Build (Optional)

If you want to build locally:

### Build Docker Image

```bash
docker build -t spotify-tokener .
```

### Run the Container

```bash
docker run -p 3000:3000 spotify-tokener
```



# 🛠 Development

### Requirements

* **Bun**
* **Playwright** (Chromium)

### Development Mode

```bash
bun run dev
```

### Production Mode

```bash
bun run start
```



# 🔍 Troubleshooting

### Common Issues

| Issue                       | Fix                                          |
| --------------------------- | -------------------------------------------- |
| Playwright fails to install | `npx playwright install chromium --force`    |
| Token slow or failing       | Check Chromium installation inside container |
| Cache not working           | Verify container memory + concurrency        |



# ⚡ Performance Tips

* Avoid using `force=1` too frequently
* Monitor `/api/token` for refresh timing
* Horizontal scaling recommended for large LavaSrc clusters


**Need help?**
Open an issue: [https://github.com/appujet/spotokn/issues](https://github.com/appujet/spotokn/issues)
Or check the Wiki for advanced setup.
