FROM rust:bookworm@sha256:29fe4376919e25b7587a1063d7b521d9db735fc137d3cf30ae41eb326d209471 AS rust-builder

WORKDIR /app
COPY Makefile /app
COPY external/diffgen /app/external/diffgen
RUN make rust-diffgen

FROM golang:1.22.5@sha256:86a3c48a61915a8c62c0e1d7594730399caa3feb73655dfe96c7bc17710e96cf AS builder
WORKDIR /app

ARG VERSION

COPY go.mod /app/go.mod
COPY go.sum /app/go.sum
RUN go mod download

COPY ./ ./

COPY --from=rust-builder /app/external/diffgen/target ./external/diffgen/target

RUN make build

FROM flanksource/base-image:v0.0.7@sha256:c3cda640ca7033a89e52c7f27776edfc95f825ece4b49de3b9c5af981d34a44e
WORKDIR /app

COPY --from=builder /app/.bin/config-db /app
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

RUN mkdir /opt/database && groupadd --gid 1000 catalog && \
  useradd catalog --uid 1000 -g catalog -m -d /var/lib/catalog && \
  chown -R 1000:1000 /opt/database && chown -R 1000:1000 /app

USER catalog:catalog

RUN /app/config-db go-offline
ENTRYPOINT ["/app/config-db"]
