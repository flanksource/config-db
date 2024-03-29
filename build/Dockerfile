FROM golang:1.20@sha256:bc5f0b5e43282627279fe5262ae275fecb3d2eae3b33977a7fd200c7a760d6f1 as builder
WORKDIR /app

ARG VERSION

COPY go.mod /app/go.mod
COPY go.sum /app/go.sum
RUN go mod download

COPY ./ ./
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
