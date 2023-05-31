FROM golang:1.19@sha256:8cefba2710250b21a8b8e32281788c5b53dc561ba0c51ea7de92b9a350663b7d as builder
WORKDIR /app
COPY ./ ./

ARG VERSION
COPY go.mod /app/go.mod
COPY go.sum /app/go.sum
RUN go mod download
WORKDIR /app
RUN go version
RUN make build

FROM ubuntu:bionic@sha256:14f1045816502e16fcbfc0b2a76747e9f5e40bc3899f8cfe20745abaafeaeab3
WORKDIR /app

# install CA certificates
RUN apt-get update && \
  apt-get install -y ca-certificates && \
  rm -Rf /var/lib/apt/lists/*  && \
  rm -Rf /usr/share/doc && rm -Rf /usr/share/man  && \
  apt-get clean

COPY --from=builder /app/.bin/config-db /app

RUN /app/config-db go-offline

ENTRYPOINT ["/app/config-db"]
