FROM rust:bookworm@sha256:29fe4376919e25b7587a1063d7b521d9db735fc137d3cf30ae41eb326d209471 AS rust-builder-base
COPY external/diffgen/ /app/external/diffgen
RUN cd /app/external/diffgen/ && cargo fetch

FROM rust-builder-base AS rust-builder
WORKDIR /app
COPY Makefile /app
COPY external/diffgen /app/external/diffgen
RUN make rust-diffgen

FROM node:20-bookworm AS scrapeui-builder
WORKDIR /app/cmd/scrapeui/frontend
COPY cmd/scrapeui/frontend/package.json cmd/scrapeui/frontend/package-lock.json ./
RUN npm ci
COPY cmd/scrapeui/frontend ./
RUN npm run build

FROM golang:1.26-bookworm AS builder-base
WORKDIR /app

ARG VERSION

COPY go.mod /app/go.mod
COPY go.sum /app/go.sum
RUN go mod download

FROM builder-base AS builder

COPY ./ ./

COPY --from=rust-builder /app/external/diffgen/target ./external/diffgen/target
COPY --from=scrapeui-builder /app/cmd/scrapeui/frontend/dist ./cmd/scrapeui/frontend/dist

RUN make build-prod

FROM flanksource/base-image:0.7.1@sha256:6cd9b69c841cb338a301a6cc8ff0b1a8d516ab2335ba01f09d4a9a2ff2e79f93

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
