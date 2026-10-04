# Agentic Snake

A Kubernetes-native agent operator, bootstrapped with Python, kr8s, uv, ty,
and Ruff. The MCP proxy is a separate Go service.

This is an early development repository, not a production agent platform.

## Scope

The initial controller validates namespace-local SkillSet sources and records
source revisions. It does not run an LLM, deliver skill bundles to agents, or
claim that a recorded revision has been activated by a runtime.

The MCP gateway discovers and aggregates tools from explicitly configured HTTP
upstreams, periodically refreshing the catalog and withdrawing stale routes.
Agent authentication, authorization, credential brokering, durable orchestration,
and shared memory are not implemented yet. Upstream sessions are service-owned,
not isolated per agent. Do not expose the gateway to untrusted clients.

See [the architecture decision](docs/architecture.md),
[the gateway decision](docs/mcp-gateway.md),
[the controller documentation](controller/README.md),
and [the proxy documentation](proxy/README.md) for the implemented contracts.

## Development

Install uv, Go matching `proxy/go.mod`, Make, and Docker for image checks.

```sh
make sync
make check
make build
make images smoke
```

Make owns the commands. CI invokes those same targets. Python production and
development dependencies are locked in `controller/uv.lock`.

## CRD generation and validation

Typed Python API models define CRD field contracts. The generator emits
`deploy/skillset-crd.yaml`; that file is a committed install artifact, not a
second schema source. Non-null model defaults are currently unsupported and
fail generation, including `False`, zero, empty strings and empty collections.
Existing `None` defaults represent optional-field absence, not API-server
defaulting. Change the models and run:

```sh
make generate
make check-generated validate-crds
make test-crds
```

`make check-generated` compares the artifact without rewriting it and fails on
drift. `make validate-crds` validates the CRD document, including its embedded
schema definitions, against Kubernetes 1.34.0's official OpenAPI specification.
Make downloads the checksum-pinned specification into `.cache/`. A verified
cached copy permits offline validation. To change the Kubernetes version,
update the URL/version, checksum, and matching Kind node image together.

The official CRD OpenAPI model describes the shape of a CRD document; it does
not prove that an embedded schema obeys Kubernetes structural-schema rules.
`make test-crds` checks that separately on a disposable Kind API server. It
installs the generated CRD, tests valid and invalid custom resources, and proves
that a deliberately nonstructural CRD is rejected. CI runs both validation
layers and the drift gate. Kind, kubectl, and Docker are required for this
API-server test. The harness uses the same explicit temporary kubeconfig for
creation, admission, and deletion. It removes private access state only after
cluster deletion succeeds; failed cleanup retains that state for recovery.

Pydantic and JSON Schema tooling are development dependencies. They do not ship
in the controller's production image.

Kubernetes 1.34 emits `unrecognized format "int64"` for the existing
`status.observedGeneration` annotation. Its [format registry and validation
postprocessor](https://github.com/kubernetes/kubernetes/blob/v1.34.0/staging/src/k8s.io/apiextensions-apiserver/pkg/apiserver/validation/formats.go)
do not enforce that format. The generator retains the annotation to preserve
the published schema; integer typing remains enforced, but the format is not an
integer-range constraint. This warning does not bypass either validation gate.

## Container packaging

The controller uses the exact `gcr.io/distroless/python3:nonroot` image name,
pinned by digest. Only installed production site-packages, including the
application, are copied from the builder. The runtime does not receive uv,
pip, Ruff, ty, pytest, the build interpreter, or the build virtual environment.
The builder and runtime use the Python 3.13 ABI. The runtime interpreter has
been checked by executing the pinned image.

The unsuffixed upstream image is still Debian-based; it is not a Debian-free
Python distribution. No `python3-debian*` image name is used in the Dockerfile.

The Go proxy is built with CGO disabled and copies only its executable into a
nonroot distroless static image. Both services support read-only root filesystems.

No license has been selected yet. Public visibility does not grant an open-source
license.
