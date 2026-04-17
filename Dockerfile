# Stage 1: Build Go binary
FROM golang:1.25-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/server/ ./cmd/server/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o server ./cmd/server

# Stage 2: Minimal runtime
FROM alpine:3.21

RUN apk add --no-cache curl

WORKDIR /app

COPY --from=builder /build/server ./

RUN adduser -D appuser && chown -R appuser:appuser /app

USER appuser

EXPOSE 5010

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:5010/health || exit 1

CMD ["./server"]
