LOCALBIN ?= $(shell pwd)/.bin
GOLANGCI_LINT ?= $(LOCALBIN)/golangci-lint

GOLANGCI_LINT_VERSION ?= v2.7.2

$(LOCALBIN):
	mkdir -p $(LOCALBIN)

.PHONY: tidy
tidy:
	go mod tidy

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: test
test:
	go test -v ./...

.PHONY: build
build:
	go build ./...

.PHONY: lint
lint: golangci-lint
	$(GOLANGCI_LINT) run -v ./...

.PHONY: proto
proto:
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		proto/scraper.proto

.PHONY: generate
generate: proto

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT)
$(GOLANGCI_LINT): $(LOCALBIN)
	test -s $(LOCALBIN)/golangci-lint || GOBIN=$(LOCALBIN) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
