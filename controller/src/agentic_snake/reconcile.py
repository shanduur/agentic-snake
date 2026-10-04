# Copyright (c) Mateusz Urbanek
"""SkillSet source validation. This controller does not load or activate skills."""

from __future__ import annotations

from typing import Protocol
from uuid import uuid4

MAX_SOURCES = 32


class SourceReader(Protocol):
    async def list_skillsets(self) -> list[dict]: ...

    async def source(self, kind: str, name: str) -> tuple[str, set[str]] | None: ...

    async def update_status(self, obj: dict, status: dict) -> None: ...


async def reconcile(client: SourceReader) -> None:
    """Poll one bounded namespace snapshot; API failures propagate to the caller."""
    for obj in await client.list_skillsets():
        spec = obj.get("spec", {})
        sources = spec.get("sources", [])
        valid = isinstance(sources, list) and 1 <= len(sources) <= MAX_SOURCES
        versions: list[str] = []
        if valid:
            for ref in sources:
                if not isinstance(ref, dict) or ref.get("kind") not in ("ConfigMap", "Secret"):
                    valid = False
                    break
                name, key = ref.get("name"), ref.get("key")
                if not isinstance(name, str) or not name or not isinstance(key, str) or not key:
                    valid = False
                    break
                observed = await client.source(ref["kind"], name)
                if observed is None or key not in observed[1]:
                    valid = False
                    break
                versions.append(observed[0])
        previous = obj.get("status", {})
        status = {"valid": valid, "observedGeneration": obj["metadata"].get("generation", 1)}
        if "lastValidRevision" in previous:
            status["lastValidRevision"] = previous["lastValidRevision"]
        if "sourceVersions" in previous:
            status["sourceVersions"] = previous["sourceVersions"]
        if valid and (
            versions != previous.get("sourceVersions")
            or not status.get("lastValidRevision")
            or previous.get("observedGeneration") != status["observedGeneration"]
        ):
            status["sourceVersions"] = versions
            status["lastValidRevision"] = str(uuid4())
        if status != previous:
            await client.update_status(obj, status)
