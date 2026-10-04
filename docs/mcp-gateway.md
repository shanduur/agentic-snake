# MCP tool aggregation and discovery

Status: published

## Decision

The Go proxy is an MCP gateway: an MCP server toward agents and an MCP client
of each configured upstream. A transparent HTTP reverse proxy cannot initialize
multiple peers, combine their tool inventories, or route calls to the correct
server. Use the official Go MCP SDK for transport and protocol lifecycle rather
than implementing a second JSON-RPC stack.

Agents discover the complete aggregated catalog through standard `tools/list`.
This iteration does not introduce a search tool or a generic invocation wrapper.
Upstream endpoints are explicitly configured; discovering their tools is distinct
from discovering server addresses through Kubernetes.

## Catalog ownership

Each upstream has a stable configured name. Exported tool names are deterministic,
collision-safe, and within MCP's naming constraints. Routing retains the original
server and tool name explicitly; it does not infer ownership by splitting an
untrusted tool name. Preserve tool schemas and metadata rather than synthesizing
replacement argument contracts.

A discovery sweep initializes or reuses an upstream connection and retrieves its
complete paginated inventory. Bound discovery time, page count, and catalog size.
Duplicate names, repeated pagination cursors, invalid schemas, or incomplete
retrieval must not publish a partially refreshed server inventory.

Successful discovery replaces that server's previous inventory. Absent tools are
removed from both discovery and routing. An empty successful inventory removes
all of that server's tools. A failed discovery also withdraws that server's tools;
last-known definitions are not an availability or authorization fallback. Other
servers retain their inventories. A later successful sweep restores recovered
tools. Publication is atomic for each downstream list operation, not a pinned
snapshot across separate pagination requests. Discovery remains serialized;
a slow peer can delay subsequent peers until its discovery deadline.

Periodic sweeps are required even when an upstream advertises change notifications.
Notifications can accelerate rediscovery but do not replace polling. Coalesce
refresh requests and serialize refresh work so a noisy upstream cannot create an
unbounded queue or publish older results after newer ones.

Removal becomes effective when the new catalog is published. An invocation
already admitted before publication may finish. Polling is not instantaneous
credential revocation and does not establish an authorization boundary.

## Calls and sessions

Resolve each call against the current catalog and send it only to its recorded
upstream. Preserve structured and unstructured results and the distinction between
tool execution errors and protocol errors. Bound call duration and concurrency,
propagate cancellation where the upstream transport permits it, and never
replay a `tools/call`, including when a session disappears. Local deadlines and
shutdown do not prove that a remote handler stopped or that an external mutation
was rolled back. The caller may need to reconcile an uncertain external side
effect after interruption.

Downstream session IDs are not upstream session IDs. The gateway owns upstream
connections and must not forward downstream authorization, cookies, or session
headers as upstream identity. A configured peer must not silently redirect the
connection to another endpoint.

The first gateway uses service-owned upstream sessions and trusted endpoints
without upstream credential configuration. It does not claim per-agent upstream
state isolation, credential brokering, or multi-tenant authorization. Downstream
sessions and refresh workers have bounded lifetimes; shutdown closes connections
and joins owned workers.

## Boundaries

This slice aggregates tools over Streamable HTTP. It does not implement stdio
process supervision, Kubernetes server discovery, prompt/resource aggregation,
sampling, elicitation, or task-augmented execution. Advertise only capabilities
actually implemented and do not advertise task-required tools as synchronously
callable.

The listener defaults to loopback, rejects browser-origin access, and retains
HTTP request and concurrency bounds. `/healthz` describes process availability,
not that every upstream is reachable. Downstream authentication and authorization
remain prerequisites for exposure to untrusted clients.

## Validation

Exercise the gateway with stock MCP clients and multiple real HTTP test servers.
The acceptance boundary includes colliding original names, pagination, schema and
result preservation, timer-driven removal without notifications, an empty list,
failed discovery and recovery, independent healthy servers, cancellation,
non-replayed calls, notification refresh, invalid configuration, and shutdown.
Run the repository's canonical checks and build against the integrated tree.

## Alternatives

- **Transparent forwarding:** simpler, but cannot own multiple MCP sessions or
  aggregate tool discovery and call routing.
- **Search-first discovery:** reduces model context size for large catalogs, but
  changes the requested agent-facing contract. Revisit if complete catalogs
  become a measured context or latency problem.
- **Notification-only refresh:** lower polling traffic, but misses changes from
  silent servers and lost notifications. Periodic sweeps are the recovery path.
- **Retaining stale tools after discovery failure:** improves apparent
  availability but advertises routes that can no longer be verified. This gateway
  instead withdraws the failed server's inventory.

## References

- [Repository architecture](architecture.md)
- [MCP tools](https://modelcontextprotocol.io/specification/2025-11-25/server/tools)
- [MCP lifecycle](https://modelcontextprotocol.io/specification/2025-11-25/basic/lifecycle)
- [MCP Streamable HTTP](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports)
- [Official Go SDK v1.8.0](https://github.com/modelcontextprotocol/go-sdk/tree/v1.8.0)
