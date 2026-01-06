FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy go mod files first for caching
COPY go.mod go.sum* ./
RUN go mod download

# Copy source and build
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o hyperlight-device-plugin .

# Final image
FROM alpine:3.19

RUN apk --no-cache add ca-certificates

COPY --from=builder /app/hyperlight-device-plugin /usr/local/bin/

# Run as non-root
RUN adduser -D -u 1000 hyperlight
USER hyperlight

ENTRYPOINT ["hyperlight-device-plugin"]
