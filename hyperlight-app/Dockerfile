# Build stage for guest (requires cargo-hyperlight)
FROM rust:1.89-bookworm AS guest-builder

# Install cargo-hyperlight and clang
RUN apt-get update && apt-get install -y clang && rm -rf /var/lib/apt/lists/*
RUN cargo install --locked cargo-hyperlight

WORKDIR /build
COPY guest/Cargo.toml ./guest/
COPY guest/src ./guest/src

# Build guest with cargo-hyperlight
WORKDIR /build/guest
RUN cargo hyperlight build --release && find /build -name "hyperlight-hello-guest" -type f

# Build stage for host - static binary using musl
FROM rust:1.89-bookworm AS host-builder

# Add musl target for static linking
RUN rustup target add x86_64-unknown-linux-musl
RUN apt-get update && apt-get install -y musl-tools && rm -rf /var/lib/apt/lists/*

WORKDIR /build
COPY host/Cargo.toml ./host/
COPY host/src ./host/src

# Build host as fully static binary
WORKDIR /build/host
RUN cargo build --release --target x86_64-unknown-linux-musl

# Runtime stage - scratch (empty filesystem)
# With static musl binary, we need literally nothing else
FROM scratch

WORKDIR /app

# Copy guest binary
COPY --from=guest-builder /build/guest/target/x86_64-hyperlight-none/release/hyperlight-hello-guest /app/guest/hyperlight-hello-guest

# Copy host binary (static musl build)
COPY --from=host-builder /build/host/target/x86_64-unknown-linux-musl/release/hyperlight-hello-host /app/hyperlight-hello-host

# Default command runs the host in loop mode for k8s deployment
ENTRYPOINT ["/app/hyperlight-hello-host"]
CMD ["--loop-mode"]
