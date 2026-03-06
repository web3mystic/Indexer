# Multi-stage Dockerfile for Cosmos Indexer

# =============================================================================
# Stage 1: Builder
# =============================================================================
FROM golang:1.21-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git make gcc musl-dev

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o indexer ./cmd/indexer

# =============================================================================
# Stage 2: Runtime
# =============================================================================
FROM alpine:latest

# Install runtime dependencies
RUN apk --no-cache add ca-certificates tzdata

# Create non-root user
RUN addgroup -g 1000 indexer && \
    adduser -D -u 1000 -G indexer indexer

# Set working directory
WORKDIR /home/indexer

# Copy binary from builder
COPY --from=builder /app/indexer .

# Change ownership
RUN chown -R indexer:indexer /home/indexer

# Switch to non-root user
USER indexer

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD pidof indexer || exit 1

# Run the indexer
CMD ["./indexer"]
