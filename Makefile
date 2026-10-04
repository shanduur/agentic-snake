# Copyright 2026 Mateusz Urbanek.
UV ?= uv
GO ?= go
DOCKER ?= docker
CONTROLLER_IMAGE ?= agentic-snake-controller:dev
PROXY_IMAGE ?= agentic-snake-proxy:dev
KIND ?= kind
KUBECTL ?= kubectl
KUBERNETES_VERSION ?= 1.34.0
OPENAPI_URL ?= https://raw.githubusercontent.com/kubernetes/kubernetes/v$(KUBERNETES_VERSION)/api/openapi-spec/swagger.json
OPENAPI_SHA256 ?= d3b0cdc2fda15c753206d25ab459dc7c12df64e2fd652b6809687471ea751c37
OPENAPI_FILE ?= $(CURDIR)/.cache/kubernetes/v$(KUBERNETES_VERSION)/swagger.json
KIND_IMAGE ?= kindest/node:v1.34.0@sha256:7416a61b42b1662ca6ca89f02028ac133a309a2a30ba309614e8ec94d976dc5a

.PHONY: all sync fmt lint test check build images smoke generate check-generated openapi validate-crds test-crds
all: check build

generate:
	cd controller && $(UV) run --locked python -m agentic_snake.crd --output ../deploy/skillset-crd.yaml

check-generated:
	cd controller && $(UV) run --locked python -m agentic_snake.crd --check --output ../deploy/skillset-crd.yaml

openapi:
	$(UV) run --project controller --locked python scripts/fetch_openapi.py --url "$(OPENAPI_URL)" --sha256 "$(OPENAPI_SHA256)" --output "$(OPENAPI_FILE)"

validate-crds: check-generated openapi
	cd controller && $(UV) run --locked python -m agentic_snake.validate_crds --openapi "$(OPENAPI_FILE)" --crd ../deploy/skillset-crd.yaml

test-crds: validate-crds
	$(UV) run --project controller --locked python scripts/test_crd_admission.py --kind "$(KIND)" --kubectl "$(KUBECTL)" --image "$(KIND_IMAGE)" --crd deploy/skillset-crd.yaml

sync:
	cd controller && $(UV) sync --locked

fmt:
	cd controller && $(UV) run --locked ruff format . ../scripts
	cd controller && $(UV) run --locked ruff check --fix .
	cd proxy && $(GO) fmt ./...

lint:
	cd controller && $(UV) run --locked ruff format --check . ../scripts
	cd controller && $(UV) run --locked ruff check .
	$(UV) run --project controller --locked ruff check --isolated --line-length 100 --select E,F,I,UP,B,RUF scripts
	cd controller && $(UV) run --locked ty check
	cd proxy && test -z "$$(gofmt -l .)"
	cd proxy && $(GO) vet ./...

test: openapi
	cd controller && KUBERNETES_OPENAPI="$(OPENAPI_FILE)" $(UV) run --locked pytest
	$(UV) run --project controller --locked python -m unittest discover -s scripts/tests
	cd proxy && $(GO) test -race ./...

check: validate-crds lint test

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
	$(DOCKER) run --rm --read-only --entrypoint=/usr/bin/python3 $(CONTROLLER_IMAGE) -c 'import importlib.util, kr8s, kr8s.asyncio, box, yaml, agentic_snake; from cryptography.hazmat.primitives import hashes; h = hashes.Hash(hashes.SHA256()); h.update(b"smoke"); h.finalize(); assert all(importlib.util.find_spec(name) is None for name in ("pip", "pytest", "ruff", "ty", "pydantic", "jsonschema"))'
