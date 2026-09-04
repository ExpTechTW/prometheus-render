BIN     := bin/prometheus-render
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: all build install test vet fmt examples audit clean

all: build

build:
	go build -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/prometheus-render

install:
	go install -ldflags '$(LDFLAGS)' ./cmd/prometheus-render

test:
	go test ./...
	cd examples/gallery && go build ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

# Redraws out/ from testdata/sample.db. Offline: no data source needed.
examples:
	cd examples/gallery && go run . -db ../../testdata/sample.db -out ../../out

# Reads the rendered PNGs back and checks each legend names its series once.
audit:
	python3 hack/legend_audit.py

clean:
	rm -rf bin
