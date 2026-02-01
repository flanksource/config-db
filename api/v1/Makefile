LOCALBIN ?= $(shell pwd)/.bin
GOLANGCI_LINT ?= $(LOCALBIN)/golangci-lint
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen

GOLANGCI_LINT_VERSION ?= v2.7.2
CONTROLLER_TOOLS_VERSION ?= v0.19.0

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

.PHONY: generate
generate: controller-gen
	$(CONTROLLER_GEN) object:headerFile="../../hack/boilerplate.go.txt" paths="./..."

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT)
$(GOLANGCI_LINT): $(LOCALBIN)
	test -s $(LOCALBIN)/golangci-lint || GOBIN=$(LOCALBIN) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN)
$(CONTROLLER_GEN): $(LOCALBIN)
	test -s $(LOCALBIN)/controller-gen && $(LOCALBIN)/controller-gen --version | grep -q $(CONTROLLER_TOOLS_VERSION) || \
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)
