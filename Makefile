VERSION ?= dev
# A build from a tree with uncommitted work stamps -dirty through Go's own VCS
# info, which says "not this commit" but not "which uncommitted work": every
# build from the same commit then carries an identical label. The pending count
# makes two dirty builds distinguishable.
PENDING := $(shell git status --porcelain 2>/dev/null | wc -l | tr -d ' ')
LDFLAGS := -s -w -X github.com/mdpsec/huntdeck/internal/version.Version=$(VERSION) \
           -X github.com/mdpsec/huntdeck/internal/version.Pending=$(PENDING)
GOLANGCI_LINT ?= golangci-lint
# e.g. DOCKER_BUILD_FLAGS=--network=host on hosts without a docker0 bridge
DOCKER_BUILD_FLAGS ?=

.PHONY: build test lint vuln check gates docker release-image run fmt tidy clean i18n

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/huntdeck ./cmd/huntdeck
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/huntdeck-mcp ./cmd/huntdeck-mcp

fmtcheck: ## fail when a file is not gofmt-clean (make fmt fixes it)
	@out=$$(gofmt -l ./cmd ./internal); if [ -n "$$out" ]; then echo "gofmt would change:"; echo "$$out"; exit 1; fi

test:
	go test -race -timeout 30m ./...

lint:
	go vet ./...
	@if command -v $(GOLANGCI_LINT) >/dev/null 2>&1; then $(GOLANGCI_LINT) run; \
	else echo "golangci-lint not installed: running it through go run (needs the module proxy)"; \
	     go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run --timeout=5m ./...; fi

vuln:
	@if command -v govulncheck >/dev/null 2>&1; then govulncheck ./...; \
	else echo "govulncheck not installed: running it through go run (needs the module proxy)"; \
	     go run golang.org/x/vuln/cmd/govulncheck@latest ./...; fi

# Every check a change must pass, run locally (there's no hosted CI).
i18n: ## report interface copy the templates still write in English
	python3 scripts/i18n-lint.py --count

# Whether a running instance is healthy: make smoke URL=https://huntdeck.example
smoke: ## check pages, API, login and MCP of a running instance
	scripts/smoke.sh --url $(or $(URL),http://127.0.0.1:8080) --mcp bin/huntdeck-mcp

# What a host is serving: make verify-prod HOST=prod-1 [EXPECT=deploy-202611181920]
verify-prod: ## ask a host which build it runs (see docs/deploy.md)
	@test -n "$(HOST)" || { echo 'usage: make verify-prod HOST=<ssh-host> [EXPECT=<version>]' >&2; exit 2; }
	scripts/deploy.sh --host $(HOST) --check $(if $(EXPECT),--expect $(EXPECT))

check: fmtcheck lint test vuln i18n gates

docker:
	docker build $(DOCKER_BUILD_FLAGS) --build-arg VERSION=$(VERSION) -t huntdeck:dev .

# The release image, with an SBOM and provenance attached, pushed to IMAGE
# as VERSION and latest. Run from a clean checkout of the tag being
# released; needs `docker login ghcr.io`. linux/amd64 by default: arm64
# (PLATFORMS=linux/amd64,linux/arm64) uses Chromium instead of Google
# Chrome for the login browser, and building it on an amd64 host needs
# arm64 emulation once per host:
#   docker run --privileged --rm tonistiigi/binfmt --install arm64
# e.g. BUILDER_FLAGS="--driver-opt network=host" on hosts without a docker0 bridge
BUILDER_FLAGS ?=
IMAGE ?= ghcr.io/mdpsec/huntdeck
PLATFORMS ?= linux/amd64
release-image:
	@case "$(VERSION)" in v[0-9]*) ;; *) echo "VERSION=vX.Y.Z, the tag being released" >&2; exit 1;; esac
	@test -z "$$(git status --porcelain)" || { echo "commit or stash first: the image is built from the checkout" >&2; exit 1; }
	docker buildx inspect huntdeck-release >/dev/null 2>&1 || docker buildx create --name huntdeck-release --driver docker-container $(BUILDER_FLAGS)
	docker buildx build --builder huntdeck-release $(DOCKER_BUILD_FLAGS) --platform $(PLATFORMS) \
		--build-arg VERSION=$(VERSION) --sbom=true --provenance=mode=max \
		--label org.opencontainers.image.source=https://github.com/mdpsec/huntdeck \
		--label org.opencontainers.image.version=$(VERSION) \
		--label org.opencontainers.image.licenses=FSL-1.1-ALv2 \
		-t $(IMAGE):$(VERSION) -t $(IMAGE):latest --push .

run: build
	HUNTDECK_DATA_DIR=./data HUNTDECK_LISTEN=127.0.0.1:8080 ./bin/huntdeck serve

fmt:
	gofmt -w .

tidy:
	go mod tidy

clean:
	rm -rf bin

gates: ## prove the shell gates fail when they should (no host, no data)
	@scripts/gates.sh
