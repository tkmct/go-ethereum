# Geth with UBT Support - Docker Build
# This Dockerfile builds Geth from source with UBT (Universal Binary Trie) support
#
# Build context should be the go-ethereum root directory:
#   docker build -f docker-ubt-test/geth/Dockerfile -t geth-ubt .

ARG COMMIT=""
ARG VERSION=""
ARG BUILDNUM=""

# Build Geth in a stock Go builder container
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache gcc musl-dev linux-headers git make

# Get dependencies first (cached if go.mod/go.sum unchanged)
COPY go.mod /go-ethereum/
COPY go.sum /go-ethereum/
RUN cd /go-ethereum && go mod download

# Copy source and build
COPY . /go-ethereum
WORKDIR /go-ethereum

# Build geth (sidecar-enabled, pure Go)
RUN CGO_ENABLED=0 go build -tags urfave_cli_no_docs -o /go-ethereum/build/bin/geth ./cmd/geth

# Final image
FROM alpine:latest

RUN apk add --no-cache ca-certificates wget curl jq

# Copy geth binary
COPY --from=builder /go-ethereum/build/bin/geth /usr/local/bin/

# Create data directories
RUN mkdir -p /root/.ethereum /config

# Expose ports
# 8545  - HTTP RPC
# 8546  - WebSocket
# 8551  - Engine API (Auth RPC)
# 30303 - P2P
# 6060  - Metrics/pprof
EXPOSE 8545 8546 8551 30303 30303/udp 6060

ENTRYPOINT ["geth"]

# Metadata labels
ARG COMMIT=""
ARG VERSION=""
ARG BUILDNUM=""

LABEL commit="$COMMIT" version="$VERSION" buildnum="$BUILDNUM"
LABEL description="Geth with UBT (Universal Binary Trie) support for testing"
