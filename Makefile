# WellBoard Makefile.
#
# The build host has no Go toolchain: every Go command runs inside the
# golang:1.23-alpine Docker image. GOCACHE/GOPATH stay inside the container
# (/tmp) so the working tree is never polluted. See docs/DECISIONS.md (D3).

GO_IMAGE ?= golang:1.23-alpine

GO_DOCKER := docker run --rm -v $(CURDIR):/app -w /app \
  -e GOFLAGS=-mod=mod \
  -e CGO_ENABLED=0 \
  -e GOCACHE=/tmp/gocache \
  -e GOPATH=/tmp/gopath \
  $(GO_IMAGE)

.PHONY: build test vet fmt lint run clean

build:
	$(GO_DOCKER) go build -o wellboard ./cmd/wellboard

test:
	$(GO_DOCKER) go test ./...

vet:
	$(GO_DOCKER) go vet ./...

fmt:
	$(GO_DOCKER) gofmt -l -w .

# lint: fail if any .go file is not gofmt-clean, then go vet.
# golangci-lint is deferred (needs a custom image); see docs/DECISIONS.md (D4).
lint:
	$(GO_DOCKER) sh -c 'files=$$(gofmt -l .); if [ -n "$$files" ]; then echo "gofmt: files need formatting (run: make fmt)"; echo "$$files"; exit 1; fi'
	$(GO_DOCKER) go vet ./...

# Dev run: starts the daemon in dev mode with the UI port published.
run:
	docker run --rm -p 8090:8090 -v $(CURDIR):/app -w /app \
	  -e GOFLAGS=-mod=mod -e CGO_ENABLED=0 \
	  -e GOCACHE=/tmp/gocache -e GOPATH=/tmp/gopath \
	  $(GO_IMAGE) go run ./cmd/wellboard --dev

clean:
	rm -f wellboard cmd/wellboard/wellboard
