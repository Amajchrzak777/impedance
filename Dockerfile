# Build stage
FROM golang:1.23-alpine AS builder

# Install git (required for go mod download with git dependencies)
RUN apk add --no-cache git

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies and clean up go.sum
RUN go mod tidy && go mod download

# Copy source code
COPY . .

# Fix go.sum with all dependencies and build
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -o goimpsolver ./cmd/goimpsolver

# Runtime stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy the binary from builder stage
COPY --from=builder /app/goimpsolver .

# Expose port
EXPOSE 8080

# Default command
CMD ["./goimpsolver"]