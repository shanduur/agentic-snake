# MCP HTTP proxy

A **single trusted upstream** Streamable HTTP MCP endpoint is exposed as `GET`, `POST`, and `DELETE /mcp`. This is a transparent reverse proxy, **not** an MCP aggregator: it does not discover tools, merge servers, translate transports, replay requests, retry mutations, or terminate MCP sessions. `GET /healthz` checks only that this process is serving; it does not probe the upstream.

```sh
cd proxy
go run ./cmd/mcp-proxy --upstream http://127.0.0.1:3000/mcp
# Optionally: --listen 127.0.0.1:8081
```

`--upstream` is required: an HTTP(S) endpoint URL with a canonical, non-root path; credentials, queries, fragments, escaped paths, and dot segments are rejected. The proxy routes `/mcp` to that **exact** endpoint path, preserving the request method/body and MCP session/protocol headers. GET SSE streams are flushed as data arrives; client cancellation propagates upstream. The server shuts down on SIGINT/SIGTERM, allowing up to 10 seconds for in-flight requests before forcing them closed. No request retries are performed.

**Security boundary:** There is **no downstream authentication or authorization**. Never bind to a public address or expose this proxy via public ingress without adding an external, independently reviewed authentication/access-control layer. The default listener is loopback (`127.0.0.1:8080`); an explicit `--listen` can change it, at the operator's risk. Browser-origin requests are rejected. The proxy deliberately forwards only `Accept`, `Content-Type`, `Mcp-Session-Id`, `Mcp-Protocol-Version`, and `Last-Event-Id`. Downstream `Authorization`, cookies, `Forwarded`, `X-Forwarded-*`, and other identity headers are not sent upstream; upstream `Set-Cookie` is stripped from responses. There is no upstream credential configuration: use a trusted upstream reachable without credentials. TLS to HTTPS upstreams uses Go's system trust roots.

Bounds: up to 64 simultaneous MCP requests; excess requests receive 503. Request bodies are limited to 8 MiB (known oversize receives 413); the HTTP server limits headers to 16 KiB and reads headers within 5 seconds. Idle connections time out after 60 seconds. Streaming responses intentionally have no fixed duration or response-size cap; one stream occupies one slot until it closes. Run this behind a trusted access-control/traffic-limiting layer if your threat model requires more. This bootstrap does not enforce MCP message validity, origin allowlists beyond rejecting Origin, per-client quotas, or DNS/IP egress restrictions on the configured upstream.

Verification (from `proxy/`): `GOWORK=off go test -race ./...`, `GOWORK=off go vet ./...`, and `GOWORK=off go build -o /tmp/mcp-proxy ./cmd/mcp-proxy`.
