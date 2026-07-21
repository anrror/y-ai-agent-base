# Multi-stage Docker build for y-ai-agent-base server.
# Stage 1: Build the Go binary.
FROM golang:1.25-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -trimpath" -o /bin/server ./cmd/server/

# Stage 2: Minimal runtime image.
FROM alpine:3.21

LABEL org.opencontainers.image.source="https://github.com/anrror/y-ai-agent-base"
LABEL org.opencontainers.image.description="Extensible AI agent framework for building composable, safe, and memory-aware agents"

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /bin/server /usr/local/bin/server
COPY config /etc/yai/config

ENV YAI_SERVER_PORT=8080
ENV YAI_SERVER_MODE=production
ENV YAI_LOGGING_LEVEL=info
ENV YAI_LOGGING_FORMAT=json

EXPOSE 8080

RUN addgroup -S app && adduser -S app -G app
USER app

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

ENTRYPOINT ["server"]
