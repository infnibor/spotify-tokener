.PHONY: help build run test docker-build docker-run docker-stop clean

help:
	@echo "Available commands:"
	@echo "  make build        - Build the Go binary"
	@echo "  make run          - Run the service locally"
	@echo "  make test         - Run tests"
	@echo "  make docker-build - Build Docker image"
	@echo "  make docker-run   - Run with Docker Compose"
	@echo "  make docker-stop  - Stop Docker containers"
	@echo "  make clean        - Clean build artifacts"

build:
	go build -o spotokn .

run:
	go run .

test:
	go test -v ./...

docker-build:
	docker build -t spotokn:latest .

docker-run:
	docker compose up -d

docker-stop:
	docker compose down

docker-logs:
	docker compose logs -f

clean:
	rm -f spotokn
	docker compose down -v
	docker rmi spotokn:latest 2>/dev/null || true