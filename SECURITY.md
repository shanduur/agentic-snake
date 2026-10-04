# Security

Agentic Snake is an early bootstrap and is not ready for untrusted tenants.

- Do not expose the MCP gateway publicly. It does not authenticate callers or
  implement per-agent authorization. Put it behind an independently authenticated
  boundary and restrict network access to the configured upstreams.
- Upstream MCP sessions are service-owned and shared by trusted callers. They do
  not isolate each agent's upstream state or credentials. Per-agent sessions and
  access policy are required before multi-tenant use.
- Tool descriptions, schemas, annotations, and results are upstream-provided
  data, not authorization grants. Periodic discovery withdraws stale routes but
  is not instantaneous credential or permission revocation.
- The controller's namespaced Role can read Secrets in its namespace. Treat that
  namespace as a trusted administrative boundary. Kubernetes RBAC does not make
  untrusted SkillSet authors safe co-tenants with unrelated secrets.
- A validated skill source is not trusted executable content and is not an
  authorization grant. The current controller does not execute skill content.
- Secret values must not appear in status, logs, errors, or revision identifiers.
- The controller is single-replica. There is no leader election or durable task
  execution in this version. SkillSet listing is not paginated; large namespaces
  are outside the current bootstrap's tested operating envelope.
- Proxy tests cover forwarding and SSE delivery, but do not yet establish all
  overload, oversized chunked-body, upstream-failure, or cancellation cleanup paths.
- Container build tools and development dependencies must stay in builder stages.

Do not include credentials or private skill contents in public bug reports.
