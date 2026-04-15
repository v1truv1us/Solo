.PHONY: build test test-race vet staticcheck vulncheck clean all

BINARY := solo
VERSION ?= dev
BUILD_TAGS := -tags "fts5"
LDFLAGS := -s -w -X solo/internal/version.Version=$(VERSION)
GOFLAGS := $(BUILD_TAGS)

all: build test vet

build:
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/solo

test:
	go test $(GOFLAGS) ./...

test-race:
	go test $(GOFLAGS) -race ./...

vet:
	go vet $(GOFLAGS) ./...

staticcheck:
	staticcheck $(GOFLAGS) ./...

vulncheck:
	govulncheck $(GOFLAGS) ./...

clean:
	rm -f $(BINARY)
	rm -rf .solo/
