FROM golang:1.25-alpine@sha256:1ae0735f00daffa3aaf1363a5184c0d2dc55c78e3db4ec70241cdac97bf84b59 AS builder

WORKDIR /app

# Copy go mod files first for caching
COPY go.mod go.sum* ./
RUN go mod download

# Copy source and build
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o hyperlight-device-plugin .

# Final image
FROM alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6

RUN apk --no-cache add ca-certificates

COPY --from=builder /app/hyperlight-device-plugin /usr/local/bin/

# Note: Runs as root (runAsUser: 0) in Kubernetes - required for
# write access to /var/lib/kubelet/device-plugins and /var/run/cdi

ENTRYPOINT ["hyperlight-device-plugin"]
