SHELL := /bin/sh

VERSION ?= 0.1.0-m0
COMMIT  ?= 0000000
BUILD   ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X luanbird/osdiag/internal/version.Version=$(VERSION) \
	-X luanbird/osdiag/internal/version.Commit=$(COMMIT) \
	-X luanbird/osdiag/internal/version.Build=$(BUILD)

.PHONY: build linux-amd64 linux-arm64 test vet clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/osdiag ./cmd/osdiag

linux-amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/osdiag_linux_amd64 ./cmd/osdiag

linux-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/osdiag_linux_arm64 ./cmd/osdiag

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf bin
