# ---- Build stage ----
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Install build deps
RUN apk add --no-cache git

# Download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /panel ./main.go

# ---- Runtime stage ----
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# Copy binary and templates/static assets
COPY --from=builder /panel .
COPY templates/ templates/
COPY static/ static/

EXPOSE 8080

ENTRYPOINT ["/app/panel"]
