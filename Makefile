BINARY := hypr-switch-user
GO ?= go
GOFMT ?= gofmt
PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
DESTDIR ?=

.PHONY: all build test live-test-readonly fmt-check install clean

all: build

build:
	mkdir -p bin
	$(GO) build -trimpath -o bin/$(BINARY) ./cmd/$(BINARY)

test:
	$(GO) test ./...

live-test-readonly:
	HYPR_SWITCH_USER_LIVE_TEST=1 $(GO) test ./internal/switchuser -run '^TestLive' -v

fmt-check:
	@test -z "$$($(GOFMT) -l $$(find cmd internal -name '*.go'))" || (echo "Go files need formatting" >&2; exit 1)

install: build
	install -Dm0755 bin/$(BINARY) $(DESTDIR)$(BINDIR)/$(BINARY)

clean:
	rm -f bin/$(BINARY)
