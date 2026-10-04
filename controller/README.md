# SkillSet observer

This is a namespace-scoped source observer, not a skill runtime. Install
`deploy/skillset-crd.yaml` before `deploy/controller.yaml`. From the repository
root, `make images` builds `agentic-snake-controller:dev` (and the proxy image).
The controller image runs `/usr/bin/python3 -m agentic_snake` as its entrypoint,
not the installed console script.

A `SkillSet` references 1 to 32 ConfigMap or Secret keys in its own namespace:

```yaml
apiVersion: agentic-snake.dev/v1alpha1
kind: SkillSet
metadata:
  name: example
  namespace: agentic-snake
spec:
  sources:
    - kind: ConfigMap
      name: my-skills
      key: SKILL.md
```

The controller polls the namespace every 30 seconds by default. It checks whether
referenced keys exist, including ConfigMap binary keys. Each successful change to
source `resourceVersion`s or SkillSet generation creates a new opaque UUID in
`status.lastValidRevision`. `status.sourceVersions` holds only ordered Kubernetes
resourceVersion strings, never source values or content digests. Invalid input
sets `status.valid: false` and keeps the last valid revision. A status PATCH
conflict (HTTP 409) skips that stale update; the next poll reads the SkillSet
again. Other API errors exit nonzero; a Deployment restart retries. A successful
status means **source presence observed at poll time**, not that files have been
parsed, validated as skills, mounted, hot-reloaded, or activated in an agent.
There is no event watch, no dynamic runtime delivery, no propagation to agents
and no content integrity check. Concurrent source updates may require a
subsequent poll.

Run locally with `uv sync --group dev`, `uv run agentic-snake-controller --help`,
`uv run pytest`, `uv run ruff check src tests`, and `uv run ty check src tests`.
`--once` makes one API pass and exits; it does not retry a skipped 409 update
before exiting. `--poll-interval` accepts whole seconds from 1 to 3600;
`--namespace` selects one namespace. Source values are read by the client to
check key presence but are not returned to reconciliation or status.
Treat controller memory and its read permission on Secrets as sensitive. The
current tests use synthetic responses, not a live API-server admission test.
