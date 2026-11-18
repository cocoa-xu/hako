GO ?= go
NODE ?= node
GORELEASER ?= goreleaser

.PHONY: build test test-web vet audit sign release-check snapshot

build:
	$(GO) build -trimpath -buildvcs=false -o bin/hako ./cmd/hako

test: test-web
	GO="$(GO)" ./scripts/test.sh

test-web:
	$(NODE) --test internal/server/webtest/*.test.mjs

vet:
	$(GO) vet ./...

audit:
	python3 scripts/audit-source.py

sign:
	codesign --force --sign "$${SIGN_IDENTITY:?Set SIGN_IDENTITY to a local signing identity}" --identifier moe.uwucocoa.hako --options runtime --timestamp=none bin/hako
	codesign --verify --strict --verbose=2 bin/hako

release-check:
	GORELEASER="$(GORELEASER)" ./scripts/release-local.sh check

snapshot:
	GORELEASER="$(GORELEASER)" ./scripts/release-local.sh snapshot
