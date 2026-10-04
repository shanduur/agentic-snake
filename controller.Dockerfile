# Copyright 2026 Mateusz Urbanek.
FROM ghcr.io/astral-sh/uv:0.11.24@sha256:99ea34acedc870ba4ad11a1f540a1c04267c9f30aadc465a94406f52dfda2c36 AS uv
FROM python:3.13-slim-trixie@sha256:3dd7cc108ec1493442514f5c2a871af6af0ec31d768ff6e378a93340c3b3db5f AS build
COPY --from=uv /uv /usr/local/bin/uv
WORKDIR /build
COPY controller/ ./
ENV UV_PYTHON_DOWNLOADS=never UV_LINK_MODE=copy
RUN uv sync --locked --no-dev --no-editable --python /usr/local/bin/python3

FROM gcr.io/distroless/python3:nonroot@sha256:774595d652a294b54c9bd575b2d9fdd1a4b47547dc17b8bfa4c0e953c64855b3
COPY --from=build /build/.venv/lib/python3.13/site-packages/ /opt/packages/
ENV PYTHONPATH=/opt/packages PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1
USER 65532:65532
ENTRYPOINT ["/usr/bin/python3", "-m", "agentic_snake"]
