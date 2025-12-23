.PHONY: help build run test docker-build docker-run docker-stop docker-logs clean

BINARY_NAME=spotokn
DOCKER_IMAGE=ghcr.io/botxlab/spotokn:latest
DOCKERFILE=Dockerfile

help:
	@echo "Available commands:"
	@echo "  make build         - Build the Go binary"
	@echo "  make run           - Run the service locally"
	@echo "  make test          - Run tests"
	@echo "  make docker-build  - Build Docker image"
	@echo "  make docker-push   - Push Docker image to GHCR"
	@echo "  make docker-run    - Run with Docker Compose"
	@echo "  make docker-stop   - Stop Docker containers"
	@echo "  make docker-logs   - View container logs"
	@echo "  make clean         - Clean build artifacts & stop containers"

# Build Go binary from cmd/spotokn
build:
	go build -o $(BINARY_NAME) ./cmd/spotokn

# Run locally (non-Docker)
run:
	go run ./cmd/spotokn

# Run tests
test:
	go test -v ./...

# Build Docker image using docker/Dockerfile
docker-build:
	docker build -t $(DOCKER_IMAGE) -f $(DOCKERFILE) .

# Push image to GHCR
docker-push:
	docker push $(DOCKER_IMAGE)

# Run Docker Compose
docker-run:
	docker compose -f docker-compose.yml up -d

# Stop containers
docker-stop:
	docker compose -f docker-compose.yml down

# Logs
docker-logs:
	docker compose -f docker-compose.yml logs -f

# Clean build artifacts
clean:
	rm -f $(BINARY_NAME)
	docker compose -f docker-compose.yml down -v || true
	docker rmi $(DOCKER_IMAGE) 2>/dev/null || true
