GOBIN ?= $(shell go env GOPATH)/bin

build:
	go build -o pond ./cmd/pond

test:
	go test ./...

lint:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "unformatted:"; echo "$$out"; exit 1; fi
	go vet ./...
	$(GOBIN)/go-arch-lint check --project-path .

setup:
	go install github.com/fe3dback/go-arch-lint@v1.18.0

.PHONY: build test lint setup
