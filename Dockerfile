# ==========================================
# Stage 1: Build the Go Application Binary
# ==========================================
FROM golang:1.27-alpine AS builder

WORKDIR /build

# Install git and build essentials if required
RUN apk add --no-cache ca-certificates tzdata

# Cache Go modules
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build statically linked binary stripped of symbols for minimum size
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -extldflags '-static'" \
    -o pawsos ./cmd/server

# ==========================================
# Stage 2: Minimal Production Runtime
# ==========================================
FROM alpine:3.20

WORKDIR /app

# Install runtime dependencies for TLS and Timezones
RUN apk add --no-cache ca-certificates tzdata

# Create non-root unprivileged service user
RUN addgroup -S pawsos && adduser -S pawsos -G pawsos

# Copy compiled binary from builder
COPY --from=builder /build/pawsos /app/pawsos

# Copy static frontend assets, migrations, and default assets
COPY --from=builder /build/index.html /app/index.html
COPY --from=builder /build/report.html /app/report.html
COPY --from=builder /build/track.html /app/track.html
COPY --from=builder /build/case.html /app/case.html
COPY --from=builder /build/map.html /app/map.html
COPY --from=builder /build/dashboard.html /app/dashboard.html
COPY --from=builder /build/admin.html /app/admin.html
COPY --from=builder /build/about.html /app/about.html
COPY --from=builder /build/login.html /app/login.html
COPY --from=builder /build/css /app/css
COPY --from=builder /build/js /app/js
COPY --from=builder /build/assets /app/assets
COPY --from=builder /build/migrations /app/migrations

# Create uploads directory and grant ownership to non-root user
RUN mkdir -p /app/uploads && chown -R pawsos:pawsos /app

USER pawsos

# Default runtime configuration
ENV PORT=8080 \
    HOST=0.0.0.0 \
    ENVIRONMENT=production \
    DB_DRIVER=sqlite \
    DB_SOURCE=/app/uploads/pawsos.db \
    STORAGE_DIR=/app/uploads \
    MAX_UPLOAD_SIZE_MB=5

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://127.0.0.1:8080/health || exit 1

ENTRYPOINT ["/app/pawsos"]
