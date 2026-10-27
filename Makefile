GO ?= go

.PHONY: build test sign

build:
	$(GO) build -trimpath -o bin/hako ./cmd/hako

test:
	$(GO) test -race ./...

sign:
	codesign --force --sign "$${SIGN_IDENTITY:?Set SIGN_IDENTITY to a local signing identity}" --identifier moe.uwucocoa.hako --options runtime --timestamp=none bin/hako
	codesign --verify --strict --verbose=2 bin/hako
