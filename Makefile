# Copyright 2026 Mateusz Urbanek.
UV ?= uv
GO ?= go
DOCKER ?= docker
CONTROLLER_IMAGE ?= agentic-snake-controller:dev
PROXY_IMAGE ?= agentic-snake-proxy:dev

.PHONY: all sync fmt lint test check build images smoke
all: check build

sync:
	cd controller && $(UV) sync --locked

fmt:
	cd controller && $(UV) run --locked ruff format .
	cd controller && $(UV) run --locked ruff check --fix .
	cd proxy && $(GO) fmt ./...

lint:
	cd controller && $(UV) run --locked ruff format --check .
	cd controller && $(UV) run --locked ruff check .
	cd controller && $(UV) run --locked ty check
	cd proxy && test -z "$$(gofmt -l .)"
	cd proxy && $(GO) vet ./...

test:
	cd controller && $(UV) run --locked pytest
	cd proxy && $(GO) test -race ./...

check: lint test

build:
	cd controller && $(UV) build --no-sources
	mkdir -p bin
	cd proxy && CGO_ENABLED=0 $(GO) build -trimpath -o ../bin/mcp-proxy ./cmd/mcp-proxy

images:
	$(DOCKER) build -f controller.Dockerfile -t $(CONTROLLER_IMAGE) .
	$(DOCKER) build -f proxy.Dockerfile -t $(PROXY_IMAGE) .

smoke:
	$(DOCKER) run --rm --read-only --cap-drop=ALL --security-opt=no-new-privileges $(CONTROLLER_IMAGE) --help
	$(DOCKER) run --rm --read-only --cap-drop=ALL --security-opt=no-new-privileges $(PROXY_IMAGE) --help
	$(DOCKER) run --rm --read-only --entrypoint=/usr/bin/python3 $(CONTROLLER_IMAGE) -c 'import importlib.util, kr8s, kr8s.asyncio, box, yaml, agentic_snake; from cryptography.hazmat.primitives import hashes; h = hashes.Hash(hashes.SHA256()); h.update(b"smoke"); h.finalize(); assert all(importlib.util.find_spec(name) is None for name in ("pip", "pytest", "ruff", "ty"))'
