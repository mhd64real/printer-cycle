# printer-cycle
#
#   make build      compile both binaries for this machine into bin/
#   make build-all  cross compile release binaries for every target into dist/
#   make check      what CI runs

GO      ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
MODULE  := github.com/mhd64real/printer-cycle
LDFLAGS := -s -w -X $(MODULE)/internal/version.Version=$(VERSION)

BINARIES  := core dashboard
PLATFORMS := linux/arm64 linux/amd64 linux/arm

.PHONY: all build build-all check vet fmt test clean

all: build

build:
	@mkdir -p bin
	CGO_ENABLED=0 $(GO) build -ldflags '$(LDFLAGS)' -o bin/ ./cmd/...
	@ls -1 bin/

build-all:
	@rm -rf dist && mkdir -p dist
	@for platform in $(PLATFORMS); do \
		os=$${platform%%/*}; arch=$${platform##*/}; \
		for bin in $(BINARIES); do \
			out=dist/printer-cycle-$$bin-$$os-$$arch; \
			if [ "$$arch" = "arm" ]; then armv=7; else armv=""; fi; \
			echo "  $$out"; \
			CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch GOARM=$$armv \
				$(GO) build -ldflags '$(LDFLAGS)' -o $$out ./cmd/$$bin || exit 1; \
		done; \
	done
	@echo
	@ls -1 dist/

check: vet build

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

test:
	$(GO) test ./...

clean:
	rm -rf bin dist
