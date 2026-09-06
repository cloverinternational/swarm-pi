# Building the Findings Server
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Install dependencies
RUN apk add --no-cache git postgresql-dev gcc musl-dev

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build
RUN go build -o /findings-server ./cmd/findings-server

# Runtime
FROM alpine:latest

RUN apk add --no-cache ca-certificates

WORKDIR /

COPY --from=builder /findings-server /findings-server

EXPOSE 8080

ENTRYPOINT ["/findings-server"]
