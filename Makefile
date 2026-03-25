.PHONY: build build-tack build-daemon test lint vet fmt check clean

MODULE       := github.com/syndg/tack
BIN_DIR      := bin
GOLANGCI_LINT := $(shell go env GOPATH)/bin/golangci-lint

build: build-tack build-daemon

build-tack:
	go build -o $(BIN_DIR)/tack ./cmd/tack

build-daemon:
	go build -o $(BIN_DIR)/daemon ./cmd/daemon

test:
	go test ./...

lint:
	$(GOLANGCI_LINT) run

vet:
	go vet ./...

fmt:
	@bad=$$(gofmt -s -l . 2>&1 | grep -v vendor); \
	if [ -n "$$bad" ]; then \
		echo "gofmt check failed:"; echo "$$bad"; exit 1; \
	fi

check: fmt vet lint test
	@echo "All checks passed."

clean:
	rm -rf $(BIN_DIR)
