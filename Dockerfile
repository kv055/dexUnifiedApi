# Stage 1: Build Go binary
FROM golang:1.25-alpine AS builder
ARG GOPROXY=https://goproxy.io,https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}

WORKDIR /build/dexUnifiedApi
COPY commons/ ../commons/
COPY dexUnifiedApi/go.mod dexUnifiedApi/go.sum ./
RUN go mod download
COPY dexUnifiedApi/cmd/server/ ./cmd/server/
COPY dexUnifiedApi/internal/ ./internal/
COPY dexUnifiedApi/db/ ./db/
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o server ./cmd/server

# Stage 2: Minimal runtime
FROM alpine:3.21

RUN apk add --no-cache curl

WORKDIR /app

COPY --from=builder /build/dexUnifiedApi/server ./

RUN adduser -D appuser && chown -R appuser:appuser /app

USER appuser

EXPOSE 5010

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:5010/health || exit 1

CMD ["./server"]
