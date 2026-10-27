GO ?= go

.PHONY: build test

build:
	$(GO) build -trimpath -o bin/hako ./cmd/hako

test:
	$(GO) test -race ./...
