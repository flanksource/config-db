## Builder stages run on the build platform and cross-compile to TARGETARCH,
## so multi-arch builds don't pay for QEMU emulation of cargo/rustc/go.
FROM --platform=$BUILDPLATFORM rust:bookworm@sha256:29fe4376919e25b7587a1063d7b521d9db735fc137d3cf30ae41eb326d209471 AS rust-builder-base
WORKDIR /app/external/diffgen
COPY external/diffgen/Cargo.toml external/diffgen/Cargo.lock ./
## cargo fetch only needs the manifests; a stub lib.rs keeps this layer
## cached across source-only changes
RUN mkdir -p src && touch src/lib.rs && cargo fetch

FROM rust-builder-base AS rust-builder
ARG BUILDARCH
ARG TARGETARCH
ENV CARGO_TARGET_X86_64_UNKNOWN_LINUX_GNU_LINKER=x86_64-linux-gnu-gcc \
    CARGO_TARGET_AARCH64_UNKNOWN_LINUX_GNU_LINKER=aarch64-linux-gnu-gcc
RUN case "$TARGETARCH" in \
    amd64) echo x86_64-unknown-linux-gnu > /rust-target ;; \
    arm64) echo aarch64-unknown-linux-gnu > /rust-target ;; \
    *) echo "unsupported TARGETARCH: $TARGETARCH" >&2; exit 1 ;; \
    esac && \
    rustup target add "$(cat /rust-target)" && \
    if [ "$BUILDARCH" != "$TARGETARCH" ]; then \
    apt-get update && \
    case "$TARGETARCH" in \
    amd64) apt-get install -y --no-install-recommends gcc-x86-64-linux-gnu libc6-dev-amd64-cross ;; \
    arm64) apt-get install -y --no-install-recommends gcc-aarch64-linux-gnu libc6-dev-arm64-cross ;; \
    esac && \
    rm -rf /var/lib/apt/lists/*; \
    fi
COPY external/diffgen/src ./src
## cgo links target/release/libdiffgen.a by fixed path, so place the
## cross-compiled staticlib where a native build would put it
RUN cargo build --release --target "$(cat /rust-target)" && \
    mkdir -p target/release && \
    cp "target/$(cat /rust-target)/release/libdiffgen.a" target/release/libdiffgen.a

FROM --platform=$BUILDPLATFORM golang:1.26-bookworm@sha256:4f4ab2c90005e7e63cb631f0b4427f05422f241622ee3ec4727cc5febbf83e34 AS builder-base
WORKDIR /app

COPY go.mod /app/go.mod
COPY go.sum /app/go.sum
RUN go mod download

FROM builder-base AS builder

ARG VERSION
ARG BUILDARCH
ARG TARGETARCH
RUN if [ "$BUILDARCH" != "$TARGETARCH" ]; then \
    apt-get update && \
    case "$TARGETARCH" in \
    amd64) apt-get install -y --no-install-recommends gcc-x86-64-linux-gnu libc6-dev-amd64-cross ;; \
    arm64) apt-get install -y --no-install-recommends gcc-aarch64-linux-gnu libc6-dev-arm64-cross ;; \
    *) echo "unsupported TARGETARCH: $TARGETARCH" >&2; exit 1 ;; \
    esac && \
    rm -rf /var/lib/apt/lists/*; \
    fi

COPY ./ ./

COPY --from=rust-builder /app/external/diffgen/target/release ./external/diffgen/target/release

RUN if [ "$BUILDARCH" != "$TARGETARCH" ]; then \
    case "$TARGETARCH" in \
    amd64) export CC=x86_64-linux-gnu-gcc ;; \
    arm64) export CC=aarch64-linux-gnu-gcc ;; \
    esac; \
    fi && \
    CGO_ENABLED=1 GOOS=linux GOARCH="$TARGETARCH" make build-prod

FROM flanksource/base-image:0.7.1@sha256:5e819e83199855d20ba5b988e8a6f369c6f4332fa6f07c184c99838d06a77cc8

WORKDIR /app

COPY --from=builder /app/.bin/config-db /app
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/


RUN mkdir -p /opt/database && \
  (groupmod -n catalog ubuntu 2>/dev/null || groupadd --gid 1000 catalog) && \
  (usermod -l catalog -d /var/lib/catalog -m ubuntu 2>/dev/null || useradd catalog --uid 1000 -g catalog -m -d /var/lib/catalog) && \
  chown -R 1000:1000 /opt/database && chown -R 1000:1000 /app


USER catalog:catalog

RUN /app/config-db go-offline
ENTRYPOINT ["/app/config-db"]
