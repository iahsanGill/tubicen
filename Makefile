GO ?= go
PROMTOOL ?= promtool
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf none)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(BUILD_DATE)

.PHONY: all build test race vet fmt-check check e2e demo reports clean

all: check build

build:
	mkdir -p bin
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/tubicen ./cmd/tubicen

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

fmt-check:
	@test -z "$$(gofmt -l .)" || (echo "Go files are not formatted" && gofmt -l . && exit 1)

check: fmt-check vet test race

e2e: build
	./bin/tubicen run \
		--rules examples/rules/alerts.yml \
		--tests examples/tests/strong.yml \
		--promtool $(PROMTOOL) \
		--threshold 100 \
		--quiet

demo:
	./demo/verify.sh

reports: build
	mkdir -p dist
	./bin/tubicen run \
		--rules examples/rules/alerts.yml \
		--tests examples/tests/strong.yml \
		--promtool $(PROMTOOL) \
		--threshold 100 \
		--quiet \
		--json dist/report.json \
		--junit dist/report.xml \
		--sarif dist/report.sarif \
		--html dist/report.html

clean:
	$(GO) clean
	@if [ -d bin ]; then find bin -type f -delete; rmdir bin; fi
	@if [ -d dist ]; then find dist -type f -delete; rmdir dist; fi
