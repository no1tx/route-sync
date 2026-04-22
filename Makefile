BINARY := route-sync
PREFIX ?= /usr/local

.PHONY: build test fmt vet run install

build:
	go build -trimpath -ldflags "-s -w" -o bin/$(BINARY) ./cmd/route-sync

test:
	go test ./...

fmt:
	gofmt -w cmd internal

vet:
	go vet ./...

run:
	go run ./cmd/route-sync daemon --config configs/route-sync.yaml --dry-run

install: build
	install -d $(DESTDIR)$(PREFIX)/bin
	install -m 0755 bin/$(BINARY) $(DESTDIR)$(PREFIX)/bin/$(BINARY)
