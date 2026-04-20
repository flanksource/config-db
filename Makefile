NAME=config-db
OS   = $(shell uname -s | tr '[:upper:]' '[:lower:]')
ARCH = $(shell uname -m | sed 's/x86_64/amd64/')

ifeq ($(VERSION),)
  VERSION_TAG=$(shell git describe --abbrev=0 --tags --exact-match 2>/dev/null || echo latest)
else
  VERSION_TAG=$(VERSION)
endif

GIT_COMMIT=$(shell git rev-parse HEAD 2>/dev/null || echo none)
BUILD_DATE=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS = -X "github.com/flanksource/config-db/cmd.version=$(VERSION_TAG)" \
          -X "github.com/flanksource/config-db/cmd.commit=$(GIT_COMMIT)" \
          -X "github.com/flanksource/config-db/cmd.date=$(BUILD_DATE)" \
          -X "github.com/flanksource/clicky.Version=$(VERSION_TAG)"

# Image URL to use all building/pushing image targets
IMG ?= docker.io/flanksource/$(NAME):${VERSION_TAG}

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/.bin
export PATH := $(LOCALBIN):$(PATH)
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

.PHONY: install-deps
install-deps: $(LOCALBIN) ## Install the deps CLI if not present
	@test -x $(LOCALBIN)/deps || curl -sSL https://github.com/flanksource/deps/releases/latest/download/deps-$(OS)-$(ARCH).tar.gz | tar -xz -C $(LOCALBIN)

.PHONY: deps
deps: install-deps ginkgo controller-gen golangci-lint kustomize $(TAILWIND_JS) ## Install all tool dependencies


.PHONY: tidy
tidy:
	go mod tidy
	git add go.mod go.sum

.PHONY: scrapeui-build
scrapeui-build:
	cd cmd/scrapeui/frontend && npm ci && npm run build

# Generate OpenAPI schema
.PHONY: gen-schemas
gen-schemas:
	cp go.mod hack/generate-schemas && \
	cd hack/generate-schemas && \
	go mod edit -module=github.com/flanksource/config-db/hack/generate-schemas && \
	go mod edit -require=github.com/flanksource/config-db@v1.0.0 && \
	go mod edit -replace=github.com/flanksource/config-db=../../ && \
	if grep -v "^//" ../../go.mod | grep -q "replace.*github.com/flanksource/duty.*=>"; then \
		go mod edit -replace=github.com/flanksource/duty=../../../duty; \
	fi && \
	go mod tidy && \
	go run .

docker:
	docker build . -f build/Dockerfile -t ${IMG}

# Push the docker image
docker-push:
	docker push ${IMG}


.PHONY: manifests
manifests: generate gen-schemas ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	#$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases
	$(CONTROLLER_GEN) crd paths="./api/..." output:crd:artifacts:config=chart/crds


.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./api/..."

.PHONY: resources
resources: fmt manifests

test: manifests generate fmt vet envtest  ## Run tests.
	$(MAKE) gotest

test-prod: manifests generate fmt vet envtest  ## Run tests.
	$(MAKE) gotest-prod

test-load:
	kubectl delete deployments --all -n testns
	kubectl delete pods --all -n testns
	kubectl delete events --all -n testns
	$(MAKE) gotest-load

define validate-envtest-assets
	@if [ -x "$(ENVTEST_ASSETS_DIR)/etcd" ]; then \
		ASSETS="$(ENVTEST_ASSETS_DIR)"; \
	else \
		ASSETS=$$($(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path) || \
			{ echo "ERROR: setup-envtest failed. Run 'make envtest' to install via flanksource/deps"; exit 1; }; \
		[ -n "$$ASSETS" ] || \
			{ echo "ERROR: setup-envtest returned empty path. Run 'make envtest' instead"; exit 1; }; \
		[ -x "$$ASSETS/etcd" ] || \
			{ echo "ERROR: etcd not found at $$ASSETS/etcd — try 'make envtest'"; exit 1; }; \
	fi;
endef

.PHONY: gotest
gotest: ginkgo
	$(validate-envtest-assets) \
	KUBEBUILDER_ASSETS="$$ASSETS" \
		ginkgo -r -v --skip-package=tests/e2e  ./...

.PHONY: test-fast
test-fast: ginkgo
		ginkgo --tags slim --nodes=4   --label-filter "!slow" -r -v --skip-package=tests/e2e  ./...


.PHONY: gotest-prod
gotest-prod:
	$(validate-envtest-assets) \
	KUBEBUILDER_ASSETS="$$ASSETS" go test -tags rustdiffgen -skip ^TestE2E$$ ./... -coverprofile cover.out

.PHONY: gotest-load
gotest-load:
	make -C fixtures/load k6
	$(validate-envtest-assets) \
	LOAD_TEST=1 KUBEBUILDER_ASSETS="$$ASSETS" go test -v ./tests -skip ^TestE2E$$ -coverprofile cover.out

.PHONY: env
env: envtest ## Run tests.
	$(validate-envtest-assets) \
	KUBEBUILDER_ASSETS="$$ASSETS" \
		ginkgo -r -v --skip-package=tests/e2e -coverprofile cover.out

.PHONY: ginkgo
ginkgo:
	go install github.com/onsi/ginkgo/v2/ginkgo

.PHONY: test-e2e
test-e2e: ginkgo
	cd tests/e2e && docker-compose up -d && \
	echo 'Running tests' && \
	(ginkgo -v; TEST_EXIT_CODE=$$?; docker-compose down; exit $$TEST_EXIT_CODE)

.PHONY: e2e-services
e2e-services: ## Run e2e test services in foreground with automatic cleanup on exit
	cd tests/e2e && \
	trap 'docker-compose down -v && docker-compose rm -f' EXIT INT TERM && \
	docker-compose up --remove-orphans

fmt:
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: compress
compress: $(LOCALBIN)/upx
	upx -5 ./.bin/$(NAME)_linux_amd64 ./.bin/$(NAME)_linux_arm64 ./.bin/$(NAME)_darwin_amd64 ./.bin/$(NAME)_darwin_arm64 ./.bin/$(NAME).exe

.PHONY: linux
linux:
	GOOS=linux GOARCH=amd64 go build  -o ./.bin/$(NAME)_linux_amd64 -ldflags '$(LDFLAGS)'  main.go
	GOOS=linux GOARCH=arm64 go build  -o ./.bin/$(NAME)_linux_arm64 -ldflags '$(LDFLAGS)'  main.go

.PHONY: darwin
darwin:
	GOOS=darwin GOARCH=amd64 go build -o ./.bin/$(NAME)_darwin_amd64 -ldflags '$(LDFLAGS)'  main.go
	GOOS=darwin GOARCH=arm64 go build -o ./.bin/$(NAME)_darwin_arm64 -ldflags '$(LDFLAGS)'  main.go

.PHONY: windows
windows:
	GOOS=windows GOARCH=amd64 go build -o ./.bin/$(NAME).exe -ldflags '$(LDFLAGS)'  main.go

.PHONY: binaries
binaries: linux darwin windows compress

.PHONY: release
release: binaries
	mkdir -p .release
	cp .bin/$(NAME)* .release/

.PHONY: lint
lint: golangci-lint ## Run golangci-lint against code.
	$(GOLANGCI_LINT) run -v ./...

.PHONY: build
build:
	go build -o ./.bin/$(NAME) -ldflags '$(LDFLAGS)' .

.PHONY: build-slim
build-slim:
	go build -o ./.bin/$(NAME) -ldflags '$(LDFLAGS)' -tags slim .

.PHONY: build-prod
build-prod:
	go build -v -o ./.bin/$(NAME) -ldflags '$(LDFLAGS)' -tags rustdiffgen .

.PHONY: build-debug
build-debug:
	go build -o ./.bin/$(NAME) -ldflags '$(LDFLAGS) -checklinkname=0' -tags rustdiffgen,debug .

.PHONY: install
install:
	cp ./.bin/$(NAME) /usr/local/bin/

install-crd: manifests
	kubectl apply -f chart/crds

uninstall-crd: manifests
	kubectl delete --ignore-not-found=true -f chart/crds

# produce a build that's debuggable
.PHONY: dev
dev: scrapeui-build
	go build -o ./.bin/$(NAME) -v -gcflags="all=-N -l" main.go

.PHONY: watch
watch:
	watchexec -c make build install

$(LOCALBIN)/upx: $(LOCALBIN)
	wget -nv -O upx.tar.xz https://github.com/upx/upx/releases/download/v3.96/upx-3.96-$(ARCH)_$(OS).tar.xz
	tar xf upx.tar.xz
	mv upx-3.96-$(ARCH)_$(OS)/upx $(LOCALBIN)
	rm -rf upx-3.96-$(ARCH)_$(OS)

## Tool Binaries
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT ?= $(LOCALBIN)/golangci-lint

## Tool Versions
KUSTOMIZE_VERSION ?= v3.8.7
CONTROLLER_TOOLS_VERSION ?= v0.19.0
GOLANGCI_LINT_VERSION ?= v2.11.3

KUSTOMIZE_INSTALL_SCRIPT ?= "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh"

.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary. If wrong version is installed, it will be removed before downloading.
$(KUSTOMIZE): $(LOCALBIN)
	@if test -x $(LOCALBIN)/kustomize && ! $(LOCALBIN)/kustomize version | grep -q $(KUSTOMIZE_VERSION); then \
		echo "$(LOCALBIN)/kustomize version is not expected $(KUSTOMIZE_VERSION). Removing it before installing."; \
		rm -rf $(LOCALBIN)/kustomize; \
	fi
	test -s $(LOCALBIN)/kustomize || { curl -Ss $(KUSTOMIZE_INSTALL_SCRIPT) | bash -s -- $(subst v,,$(KUSTOMIZE_VERSION)) $(LOCALBIN); }

.PHONY: chart
chart: helm-docs helm-schema
	cd chart && ../.bin/helm-schema -k additionalProperties && ../.bin/helm-docs

.PHONY: helm-docs
helm-docs:
	test -s $(LOCALBIN)/helm-docs  || \
	GOBIN=$(LOCALBIN) go install github.com/norwoodj/helm-docs/cmd/helm-docs@latest

.PHONY: helm-schema
helm-schema:
	test -s $(LOCALBIN)/helm-schema  || \
	GOBIN=$(LOCALBIN) go install github.com/dadav/helm-schema/cmd/helm-schema@latest

values.schema.json: helm-schema
	cd chart && ../.bin/helm-schema -r -k additionalProperties  -f values.yaml && cd -

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary. If wrong version is installed, it will be overwritten.
$(CONTROLLER_GEN): $(LOCALBIN)
	test -s $(LOCALBIN)/controller-gen && $(LOCALBIN)/controller-gen --version | grep -q $(CONTROLLER_TOOLS_VERSION) || \
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

ENVTEST_K8S_VERSION = 1.34.0
ENVTEST_BRANCH = release-0.22
ENVTEST_ASSETS_DIR ?= $(LOCALBIN)


.PHONY: envtest
envtest: install-deps $(LOCALBIN) ## Install envtest binaries using flanksource/deps.
	@mkdir -p $(ENVTEST_ASSETS_DIR)
	@test -x $(ENVTEST_ASSETS_DIR)/etcd || $(LOCALBIN)/deps install etcd@v3.5.23 --bin-dir $(ENVTEST_ASSETS_DIR)
	@test -x $(ENVTEST_ASSETS_DIR)/kube-apiserver || $(LOCALBIN)/deps install kube-apiserver@v$(ENVTEST_K8S_VERSION) --bin-dir $(ENVTEST_ASSETS_DIR)
	@test -x $(ENVTEST_ASSETS_DIR)/kubectl || $(LOCALBIN)/deps install kubectl@v$(ENVTEST_K8S_VERSION) --bin-dir $(ENVTEST_ASSETS_DIR)

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	test -s $(LOCALBIN)/golangci-lint && $(LOCALBIN)/golangci-lint version --short 2>/dev/null | grep -q $(subst v,,$(GOLANGCI_LINT_VERSION)) || \
	GOBIN=$(LOCALBIN) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

.PHONY: rust-diffgen
rust-diffgen:
	cd external/diffgen && cargo build --release

.PHONY: rust-generate-header
rust-generate-header:
	cargo install cbindgen
	cd external/diffgen && cbindgen . -o libdiffgen.h --lang c

.PHONY: bench
bench:
	go test ./... -bench='^Benchmark(EventProcessing|CacheMemory|Deserialization)' \
		-benchmem -run='^$$' \
		-count=3 \
		-benchtime=2s -v $(BENCH_ARGS)

.PHONY: modernize
modernize:
	go fix ./...