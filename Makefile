.PHONY: build test lint clean tidy fmt vet docker-build docker-up coverage ci

# Default target
.DEFAULT_GOAL := build

# Build all packages
build:
	go build ./...

# Run all tests with race detector
test:
	go test -race -shuffle=on -count=1 ./...

# Run tests with coverage output
coverage:
	go test -race -count=1 -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -func=coverage.out

# Run linter (requires golangci-lint installed)
lint:
	golangci-lint run ./...

# Format code
fmt:
	goimports -local github.com/anrror/y-ai-agent-base -w .

# Run go vet
vet:
	go vet ./...

# Tidy dependencies
tidy:
	go mod tidy

# Clean build artifacts
clean:
	rm -rf bin/ dist/ coverage.out

# Build Docker image
docker-build:
	docker build -t y-ai-agent-base:latest .

# Start services with Docker Compose
docker-up:
	docker compose up --build -d

# Full CI check — everything CI runs
ci: lint test build vet
