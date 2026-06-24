GO_PACKAGES := ./...
GOFMT_DIRS  := cmd internal pkg sources

# The module roots in the workspace. A root-only `./...` does not descend into
# the nested sources/dmhy module (go.work is not traversed by `./...`), so the
# per-module targets below loop over both roots.
MODULES := . sources/dmhy

# VERSION stamps the binary via -ldflags="-X main.version=...".
# Defaults to `git describe` so dev builds carry a meaningful identifier
# without a manual override. CI/release builds pass VERSION=vX.Y.Z explicitly.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GO_LDFLAGS := -s -w -X main.version=$(VERSION)

# golangci-lint resolution order: PATH → $GOBIN → $GOPATH/bin.
GOLANGCI_LINT ?= $(shell if command -v golangci-lint >/dev/null 2>&1; then \
		command -v golangci-lint; \
	else \
		GOBIN=$$(go env GOBIN); GOPATH=$$(go env GOPATH); \
		if [ -n "$$GOBIN" ] && [ -x "$$GOBIN/golangci-lint" ]; then \
			printf "%s/golangci-lint" "$$GOBIN"; \
		elif [ -x "$$GOPATH/bin/golangci-lint" ]; then \
			printf "%s/bin/golangci-lint" "$$GOPATH"; \
		fi; \
	fi)

.PHONY: build check devserver e2e fmt lint test vet smoke hooks

build:
	go build -trimpath -ldflags='$(GO_LDFLAGS)' -o bin/takuhai ./cmd/takuhai
	cd sources/dmhy && go build -trimpath -ldflags='$(GO_LDFLAGS)' -o ../../bin/takuhai-dmhy ./cmd/takuhai-dmhy

fmt:
	gofmt -w $(GOFMT_DIRS)

vet:
	@for m in $(MODULES); do \
		echo "==> go vet ($$m)"; \
		(cd "$$m" && go vet $(GO_PACKAGES)) || exit 1; \
	done

lint:
	@if [ -z "$(GOLANGCI_LINT)" ]; then \
		echo "golangci-lint not found. Install it with: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest"; \
		exit 127; \
	fi
	@for m in $(MODULES); do \
		echo "==> golangci-lint ($$m)"; \
		(cd "$$m" && $(GOLANGCI_LINT) run $(GO_PACKAGES)) || exit 1; \
	done

test:
	@for m in $(MODULES); do \
		echo "==> go test ($$m)"; \
		(cd "$$m" && go test $(GO_PACKAGES)) || exit 1; \
	done
	$(MAKE) e2e

e2e:
	go test -tags=e2e -run TestEndToEndWorkflow -count=1 ./e2e

check: fmt vet lint test build

devserver:
	docker compose -f tools/devserver/compose.yaml up --build

# Real-binary smoke (cmd/takuhai/smoke_test.go, //go:build smoke): builds + runs the
# actual binary against a testcontainers Postgres and asserts the wired deploy shape
# (migrations -> /healthz -> /ingest -> /queue -> /mcp -> fail-fast bind -> bounded
# drain). Needs Docker; kept OUT of `check` so the default loop stays container-free.
# CI runs this same command in the dedicated `smoke` job.
smoke:
	go test -tags=smoke -run TestSmoke -count=1 ./cmd/takuhai

# Point this checkout's Git at the tracked hooks in .githooks/.
hooks:
	./scripts/install-githooks.sh
