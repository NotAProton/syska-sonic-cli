VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BIN     ?= bin/syska-sonic-cli

build:
	go build -trimpath \
	  -ldflags "-X main.version=$(VERSION) -s -w" \
	  -o $(BIN) ./cmd/syska-sonic-cli
	@echo built $(BIN) $(VERSION)

DESTDIR ?= $(HOME)/.local/bin

install: build
	install -Dm755 $(BIN) $(DESTDIR)/syska-sonic-cli
	@echo installed $(DESTDIR)/syska-sonic-cli $(VERSION)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

tidy:
	go mod tidy

check: fmt vet test build

help:
	@echo "make build | install | test | vet | fmt | tidy | check"

.PHONY: build install test vet fmt tidy check help
