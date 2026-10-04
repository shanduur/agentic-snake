# Copyright (c) Mateusz Urbanek
import asyncio
from copy import deepcopy

from agentic_snake.reconcile import reconcile


class MemoryClient:
    def __init__(self):
        self.skillsets: list[dict] = []
        self.skillsets = [
            {
                "metadata": {
                    "name": "demo",
                    "namespace": "agentic-snake",
                    "generation": 1,
                    "resourceVersion": "1",
                },
                "spec": {
                    "sources": [
                        {"kind": "ConfigMap", "name": "rules", "key": "SKILL.md"},
                        {"kind": "Secret", "name": "credential", "key": "token"},
                    ]
                },
                "status": {},
            }
        ]
        self.sources = {
            ("ConfigMap", "rules"): ("8", {"SKILL.md"}),
            ("Secret", "credential"): ("4", {"token"}),
        }
        self.writes = 0

    async def list_skillsets(self):
        return deepcopy(self.skillsets)

    async def source(self, kind, name):
        return self.sources.get((kind, name))

    async def update_status(self, obj, status):
        assert (
            obj["metadata"]["resourceVersion"] == self.skillsets[0]["metadata"]["resourceVersion"]
        )
        self.writes += 1
        self.skillsets[0]["status"] = deepcopy(status)
        self.skillsets[0]["metadata"]["resourceVersion"] = str(self.writes + 1)


def test_valid_sources_record_opaque_revision_without_content():
    client = MemoryClient()
    asyncio.run(reconcile(client))
    status = client.skillsets[0]["status"]
    assert status["valid"] is True
    assert status["lastValidRevision"]
    assert "token" not in str(status)
    assert "SKILL.md" not in str(status)
    assert "credential" not in str(status)
    assert "rules" not in str(status)
    assert status["observedGeneration"] == 1
    asyncio.run(reconcile(client))
    assert client.writes == 1


def test_source_resource_version_change_advances_revision():
    client = MemoryClient()
    asyncio.run(reconcile(client))
    first = client.skillsets[0]["status"]["lastValidRevision"]
    client.sources[("ConfigMap", "rules")] = ("9", {"SKILL.md"})
    asyncio.run(reconcile(client))
    assert client.skillsets[0]["status"]["lastValidRevision"] != first


def test_invalid_key_retains_last_valid_revision_and_recovers():
    client = MemoryClient()
    asyncio.run(reconcile(client))
    first = client.skillsets[0]["status"]["lastValidRevision"]
    client.sources[("Secret", "credential")] = ("5", set())
    asyncio.run(reconcile(client))
    status = client.skillsets[0]["status"]
    assert status["valid"] is False
    assert status["lastValidRevision"] == first
    assert "credential" not in str(status)
    client.sources[("Secret", "credential")] = ("6", {"token"})
    asyncio.run(reconcile(client))
    assert client.skillsets[0]["status"]["lastValidRevision"] != first


def test_spec_change_advances_revision_even_if_source_versions_match():
    client = MemoryClient()
    asyncio.run(reconcile(client))
    first = client.skillsets[0]["status"]["lastValidRevision"]
    client.skillsets[0]["metadata"]["generation"] = 2
    asyncio.run(reconcile(client))
    assert client.skillsets[0]["status"]["lastValidRevision"] != first


def test_missing_source_does_not_initialize_valid_revision():
    client = MemoryClient()
    client.sources.clear()
    asyncio.run(reconcile(client))
    assert client.skillsets[0]["status"] == {"valid": False, "observedGeneration": 1}
