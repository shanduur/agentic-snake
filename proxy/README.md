# MCP tools gateway

The Go service is an MCP server for agents and an MCP client of several trusted
Streamable HTTP upstreams. It exposes the full aggregated tool catalog through
standard `tools/list` and routes `tools/call` to the original server and tool.
It is not a transparent HTTP forwarder.

## Run

From the repository root:

```sh
make build
bin/mcp-proxy \
  --upstream alpha=http://127.0.0.1:9001/mcp \
  --upstream beta=http://127.0.0.1:9002/mcp \
  --refresh-interval 30s
```

Agents connect to `http://127.0.0.1:8080/mcp`. `/healthz` is process liveness,
not a guarantee that every upstream is reachable.

| Flag | Default | Meaning |
| --- | --- | --- |
| `--upstream NAME=URL` | required | Repeat for each trusted server; names must be unique. |
| `--listen` | `127.0.0.1:8080` | HTTP listener; keep behind a trusted boundary. |
| `--refresh-interval` | `30s` | Interval between periodic complete discovery sweeps. |
| `--discovery-timeout` | `10s` | Deadline for each upstream's discovery operation. |
| `--call-timeout` | `30s` | Deadline for each upstream tool call. |

CLI durations must be positive. The Go `proxy.Config` API accepts zero durations
to select defaults. Endpoints must be canonical HTTP(S) URLs with an explicit
path; credentials, query strings, fragments, and redirects are not accepted.
This first slice does not configure upstream credentials.

## Discovery and routing

- Startup and periodic sweeps collect each server's complete paginated inventory.
- Successful discovery replaces that server's inventory, removing absent tools.
  An empty result removes all its tools.
- Failed, malformed, or incomplete discovery withdraws that server's tools from
  both listing and routing. A later successful sweep restores its inventory.
  Other servers retain their own inventories.
- Upstream `notifications/tools/list_changed` requests coalesce and trigger an
  earlier sweep. Polling remains active when notifications are absent or lost.
- Tool names include a stable upstream namespace. For example, the original
  `same` tool from `alpha` is exported as `mcp_5_alpha_same`. Names that would
  exceed the protocol limit use a separate `mcp_h_` namespace and a full hash of
  the upstream identity and original name. Duplicate exports fail discovery
  instead of silently replacing a tool. Calls use an explicit routing table;
  arbitrary names are never parsed into upstream addresses.
- Tool definitions retain their original schemas and metadata. Ordinary tool
  results retain content, structured content, and execution-error status.
- Input and output schemas must have an object root and pass JSON Schema
  compilation. Local references are supported. Schemas requiring external
  documents are rejected; discovery never fetches peer-controlled URLs or files.
- Calls are not automatically replayed, including after an upstream session is
  lost. A timed-out mutation may already have taken effect.

Each discovery operation publishes a complete server inventory. Pagination
between separate downstream requests is not a pinned transaction across catalog
changes; clients should rediscover when notified. The configured interval is
between sweeps, not an instantaneous revocation deadline: discovery time also
contributes to how long a silent change takes to observe.

## Sessions and boundaries

Upstream sessions are service-owned and shared by trusted callers. Downstream
session IDs, authorization headers, and cookies are not used as upstream identity.
Do not treat this as per-agent upstream-session isolation or tenant authorization.

The gateway rejects browser-origin requests and bounds HTTP request bodies and
concurrency. Upstream JSON/error bodies and individual SSE events are capped at
8 MiB; each server inventory is capped at 1,024 tools, 32 pages, and 2 MiB of
encoded descriptors. At most 32 upstreams are configured. Downstream sessions
expire after five minutes of inactivity, but there is no session-count cap;
restrict connection rates at the trusted ingress boundary. Shutdown cancels
active calls, stops discovery, closes protocol sessions, and drains HTTP handlers.

Only tool aggregation over Streamable HTTP is in scope. There is no stdio process
supervision, Kubernetes endpoint discovery, resource/prompt aggregation, sampling,
elicitation, task execution, or multi-round-trip input negotiation. Task-required
tools are not available as synchronous calls.

**Do not expose the gateway to untrusted clients.** Authentication, per-agent
access policy, credential brokering, and stronger upstream-state isolation remain
separate work. Tool metadata is untrusted data, not a permission grant.

## Development

```sh
make check build
```

Tests use the official MCP SDK's clients and HTTP servers rather than a mocked
forwarding interface. They cover discovery, collisions, pagination, routing,
periodic removal without notifications, failure/recovery, call deadlines,
notifications, and session shutdown. The CLI test builds and runs the executable
and checks graceful shutdown with an active downstream session.

See [the gateway decision](../docs/mcp-gateway.md) and
[the security boundary](../SECURITY.md) for ownership and failure semantics.
