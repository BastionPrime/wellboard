# WellBoard Makefile.
#
# The build host has no Go toolchain: every Go command runs inside the
# golang:1.23-alpine Docker image. GOCACHE/GOPATH stay inside the container
# (/tmp) so the working tree is never polluted. See docs/DECISIONS.md (D3).

GO_IMAGE ?= golang:1.23-alpine

# Single source of truth for the release version (scripts/cross-compile.sh,
# the packaging scripts and packaging/openwrt/Makefile read the same file).
VERSION := $(shell cat VERSION)

GO_DOCKER := docker run --rm -v $(CURDIR):/app -w /app \
  -e GOFLAGS=-mod=mod \
  -e CGO_ENABLED=0 \
  -e GOCACHE=/tmp/gocache \
  -e GOPATH=/tmp/gopath \
  $(GO_IMAGE)

# Tools are pinned versions fetched on demand inside the container via
# `go install` (nothing is installed on the host); GOTOOLCHAIN=local keeps
# the pinned golang:1.23-alpine image from trying to download another
# toolchain. staticcheck must stay compatible with the container's Go.
STATICCHECK_VERSION ?= 2024.1
GOSEC_VERSION ?= v2.21.4

GO_TOOLS := sh -c 'go install \
  honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) \
  github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION) \
  && staticcheck ./... && gosec ./...'

.PHONY: build test vet fmt lint lint-staticcheck lint-gosec run clean cross

build:
	$(GO_DOCKER) go build -ldflags "-X main.version=$(VERSION)" -o wellboard ./cmd/wellboard

test:
	$(GO_DOCKER) go test ./...

vet:
	$(GO_DOCKER) go vet ./...

fmt:
	$(GO_DOCKER) gofmt -l -w .

# lint: gofmt check, go vet, then staticcheck and gosec (same docker
# pattern as build/test; tools are installed at pinned versions inside
# the throwaway container). Superseded D4 (docs/DECISIONS.md): staticcheck
# and gosec now run; golangci-lint stays deferred.
lint:
	$(GO_DOCKER) sh -c 'files=$$(gofmt -l .); if [ -n "$$files" ]; then echo "gofmt: files need formatting (run: make fmt)"; echo "$$files"; exit 1; fi'
	$(GO_DOCKER) go vet ./...
	$(GO_DOCKER) $(GO_TOOLS)

# Individual tool targets for faster triage loops.
lint-staticcheck:
	$(GO_DOCKER) sh -c 'go install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) && staticcheck ./...'

lint-gosec:
	$(GO_DOCKER) sh -c 'go install github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION) && gosec ./...'

# Dev run: starts the daemon in dev mode with the UI port published.
run:
	docker run --rm -p 8090:8090 -v $(CURDIR):/app -w /app \
	  -e GOFLAGS=-mod=mod -e CGO_ENABLED=0 \
	  -e GOCACHE=/tmp/gocache -e GOPATH=/tmp/gopath \
	  $(GO_IMAGE) go run ./cmd/wellboard --dev

# Stage prebuilt binaries for the OpenWrt package Makefile (same
# layout scripts/package-ipk.sh consumes; a buildroot checkout would
# run make in packaging/openwrt instead).
cross:
	./scripts/cross-compile.sh

clean:
	rm -f wellboard cmd/wellboard/wellboard
	rm -rf .build dist
