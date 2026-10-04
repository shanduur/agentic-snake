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

## CRD generation and validation

`src/agentic_snake/crd.py` owns the Pydantic v2 `SourceReference`, `SkillSetSpec`,
`SkillSetStatus`, and `SkillSet` models and the CRD envelope. Pydantic and
jsonschema are **dev-only dependencies**; the production polling controller does
not import them. The generated `../deploy/skillset-crd.yaml` is checked in. From
`controller/`:

```sh
uv run --locked python -m agentic_snake.crd --output ../deploy/skillset-crd.yaml
uv run --locked python -m agentic_snake.crd --check --output ../deploy/skillset-crd.yaml
uv run --locked python -m agentic_snake.validate_crds --openapi ../.cache/kubernetes/v1.34.0/swagger.json --crd ../deploy/skillset-crd.yaml
```

`--check` compares bytes and never rewrites. The OpenAPI path must be a trusted,
previously downloaded and checksum-verified Kubernetes v1.34.0 `swagger.json`.
The validator resolves the official OpenAPI2 CRD and JSONSchemaProps definitions
with all upstream definitions, checks the embedded schema with Draft 4, and can
also check a SkillSet fixture with repeatable `--resource path.yaml` arguments.
The official schema does not constrain the CRD GVK literal, so this tool checks
that separately. Neither the official OpenAPI document nor JSON Schema fixture
validation proves Kubernetes structural admission, pruning, defaulting, CEL,
status-subresource behavior, or API-server acceptance. Those need a disposable
API-server integration test with an explicit kubeconfig. Root `spec` absence and
optional `status` remain admitted as in the prior CRD; the typed models describe
authored resources, not the controller's runtime decoder.
