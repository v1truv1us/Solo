.PHONY: build test test-race vet staticcheck vulncheck clean all

BINARY := solo
BUILD_TAGS := -tags "fts5"
GOFLAGS := $(BUILD_TAGS)

all: build test vet

build:
	go build $(GOFLAGS) -o $(BINARY) ./cmd/solo

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
