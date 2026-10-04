# Contributor guidance

- Keep Python dependencies and tools in `controller/pyproject.toml` and commit
  `controller/uv.lock`. Use uv, Ruff, ty, and pytest through Make.
- Use kr8s for Kubernetes access. Do not add a second operator framework without
  an explicit design decision.
- Keep the MCP proxy in Go, separate from the Python controller.
- Make owns build and verification commands; CI invokes Make targets.
- Run `make check build` before delivery and `make images smoke` for packaging changes.
- Observe a failing test before implementing new behavior. Go tests use external
  `_test` packages and exercise public behavior.
- Keep namespace and ownership boundaries explicit. Never log Secret values or
  put secret-content hashes in status.
- Source validation, configuration delivery, and runtime activation are different
  states. Do not claim a source revision is active without runtime acknowledgement.
- Do not add placeholder CRDs or success-returning stubs for unimplemented features.
- Preserve the exact `gcr.io/distroless/python3` runtime image name, pinned by
  digest. Match its Python minor ABI and copy only production site-packages.
- Keep source headers. No license has been selected; do not add one implicitly.
- Never run integration tests against an ambient Kubernetes context. Use a
  dedicated disposable cluster and explicit kubeconfig.
