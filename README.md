# Agentic Snake

A Kubernetes-native agent operator, bootstrapped with Python, kr8s, uv, ty,
and Ruff. The MCP proxy is a separate Go service.

This is an early development repository, not a production agent platform.

## Scope

The initial controller validates namespace-local SkillSet sources and records
source revisions. It does not run an LLM, deliver skill bundles to agents, or
claim that a recorded revision has been activated by a runtime.

The initial MCP proxy forwards a single configured HTTP upstream. Multi-upstream
aggregation, agent authentication, authorization, credential brokering, durable
orchestration, and shared memory are not implemented yet. Do not expose the proxy
to untrusted clients.

See [the architecture decision](docs/architecture.md),
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
