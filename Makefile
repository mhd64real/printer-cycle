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

.PHONY: all build build-all check vet fmt test test-integration measure clean web web-install dev-up dev-down dev-logs dev-shell dev-printers

all: build

# The interface is compiled into the dashboard, so building the binaries builds
# it first. A dashboard without its interface is not a dashboard.
web-install:
	cd web && pnpm install --frozen-lockfile

web:
	cd web && pnpm install --silent && pnpm build

build: web
	@mkdir -p bin
	CGO_ENABLED=0 $(GO) build -ldflags '$(LDFLAGS)' -o bin/ ./cmd/...
	@ls -1 bin/

build-all: web
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

# Tests that need the development CUPS. Skipped by a bare `make test`, and by CI,
# which has no container to talk to.
DEV_CUPS ?= http://127.0.0.1:6631

# -p 1 runs one package at a time.
#
# The integration tests share a single CUPS container, and Go runs packages in
# parallel by default. Two packages pushing documents through the same cupsd
# starve each other and fail on timeouts that look like bugs in the code.
test-integration:
	PRINTER_CYCLE_TEST_CUPS=$(DEV_CUPS) $(GO) test ./... -count=1 -p 1 -v

# What the event loop costs while nothing is happening. Idles for a minute on
# purpose, so it is not part of the ordinary test run.
measure:
	PRINTER_CYCLE_MEASURE=1 PRINTER_CYCLE_TEST_CUPS=$(DEV_CUPS) \
		$(GO) test ./internal/ipp/ -count=1 -v -timeout 10m -run TestMeasureIdleCost

clean:
	rm -rf bin dist

# Development environment: CUPS in a container, reachable on 127.0.0.1:6631.
# 6631 rather than 631 because macOS runs its own cupsd on 631.

# Brings up the containers AND creates the virtual queues.
#
# The two used to be separate, which meant a rebuild silently left an
# environment with no queues in it and a test run that failed for reasons
# looking nothing like the cause.
dev-up:
	docker compose -f dev/compose.yml up -d --build
	@printf 'waiting for cupsd'
	@for i in $$(seq 1 60); do \
		if curl -sf -o /dev/null http://127.0.0.1:6631/; then echo " ready"; break; fi; \
		printf '.'; sleep 1; \
	done
	@$(MAKE) --no-print-directory dev-printers

dev-printers:
	@docker exec printer-cycle-cups /usr/local/bin/create-printers.sh

dev-down:
	docker compose -f dev/compose.yml down

dev-logs:
	docker compose -f dev/compose.yml logs -f

dev-shell:
	docker exec -it printer-cycle-cups bash
