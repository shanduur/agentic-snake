# Architecture

Status: discussion

## Decision

Separate Kubernetes reconciliation from agent execution and tool traffic.
The controller uses Python and kr8s. The MCP proxy uses Go. uv owns Python
dependency resolution, Ruff formatting and lint, and ty type checking.
Make owns reusable build and verification commands; CI stays thin.

## Authority boundaries

Kubernetes owns deployment configuration and active compute, not one object per
message or retained conversation. A future orchestration service will own durable
task state outside etcd. Shared memory needs an authorized service and explicit
provenance rather than a shared writable directory.

Skill configuration comes from namespaced ConfigMaps and Secrets. Resolving and
validating configuration is distinct from runtime staging and activation. Source
revision metadata must not reveal secret values or hashes of secret content.
Multi-object coordinated updates should use versioned inputs and one reference
update. Permission revocation must not fall back to old credentials.

The intended runtime protocol stages a validated revision, acknowledges it, and
activates it at a turn boundary. Engines without reload support require draining
and replacement. The initial controller does not implement this protocol.

## Bootstrap boundary

The controller records validated SkillSet source revisions. The Go MCP gateway
owns upstream protocol sessions, tool discovery, aggregation, and call routing;
[its decision](mcp-gateway.md) defines periodic sweeps and stale-tool withdrawal.
It does not create Agent or MemorySpace CRDs without implementations. This version
does not offer high-availability controller leadership, authenticated task
execution, or memory. The gateway is not an authorization boundary.

## Packaging

The Python runtime is the requested unsuffixed distroless/python3 image, pinned
by digest. Upstream documents that this alias is Debian-based. Builder Python
minor version must match the runtime, including native extension compatibility.
Copy only installed production packages; do not copy a builder virtualenv with
interpreter symlinks. Image smoke tests check imports and absence of dev tools.

## Failure model

Invalid skill sources retain the last accepted revision but report current
validation failure. Accepted does not mean active. Secret deletion or credential
revocation will require fail-closed enforcement in the future runtime/gateway.
The MCP gateway must not automatically replay tool calls, regardless of tool
annotations. An interrupted external mutation can have an uncertain outcome;
durable orchestration must reconcile that uncertainty. Failed discovery withdraws
the affected server's catalog until a later sweep succeeds.

## References

- https://github.com/kr8s-org/kr8s
- https://github.com/astral-sh/uv
- https://github.com/astral-sh/ty
- https://github.com/astral-sh/ruff
- https://github.com/GoogleContainerTools/distroless
- https://modelcontextprotocol.io/specification/2025-11-25
