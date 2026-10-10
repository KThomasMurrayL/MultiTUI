GO ?= go
VERSION ?= dev
PREFIX ?= /usr/local

.PHONY: build test integration check fmt install release clean

build:
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/mt ./cmd/mt

test:
	$(GO) test -race ./...

integration:
	MT_INTEGRATION=1 $(GO) test -race -count=1 ./internal/tmux

check:
	$(GO) vet ./...
	$(GO) test -race ./...

fmt:
	gofmt -w cmd internal

install: build
	install -d "$(DESTDIR)$(PREFIX)/bin"
	install -m 755 bin/mt "$(DESTDIR)$(PREFIX)/bin/mt"

release:
	GO="$(GO)" bash scripts/release.sh "$(VERSION)"

clean:
	rm -rf bin dist
