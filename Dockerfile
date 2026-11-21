# Stage 1: Build Go binary
FROM golang:1.27-alpine AS builder
ARG GOPROXY=https://proxy.golang.org|https://goproxy.io|direct
ENV GOPROXY=${GOPROXY}
RUN apk add --no-cache git

WORKDIR /build/dexUnifiedApi
COPY commons/ ../commons/
COPY dexUnifiedApi/go.mod dexUnifiedApi/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    sh -ec 'for attempt in 1 2 3 4 5; do go mod download && exit 0; echo "go mod download attempt $attempt failed, retrying..."; sleep $((attempt * 10)); done; exit 1'
COPY dexUnifiedApi/cmd/server/ ./cmd/server/
COPY dexUnifiedApi/internal/ ./internal/
COPY dexUnifiedApi/db/ ./db/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -ldflags="-s -w" -o server ./cmd/server

# Stage 2: Minimal runtime
FROM alpine:3.24

RUN apk add --no-cache curl

WORKDIR /app

COPY --from=builder /build/dexUnifiedApi/server ./

RUN adduser -D appuser && chown -R appuser:appuser /app

USER appuser

EXPOSE 5010

# Deploy and local tooling run `./server migrate` only on images carrying this
# label: older binaries ignore the argument and start a second API server.
LABEL dexarbtrading.unified-api.migrate="1"

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:5010/health || exit 1

CMD ["./server"]
