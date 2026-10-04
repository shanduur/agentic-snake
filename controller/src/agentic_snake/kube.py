# Copyright (c) Mateusz Urbanek
"""kr8s async transport; never return source values to reconciliation."""

from __future__ import annotations

import re
from typing import Any

from kr8s._exceptions import ServerError

GROUP_VERSION = "agentic-snake.dev/v1alpha1"
DNS_NAME = re.compile(r"^[a-z0-9]([-a-z0-9]*[a-z0-9])?$")


class KubernetesClient:
    def __init__(self, api: Any, namespace: str) -> None:
        self.api = api
        self.namespace = namespace

    async def list_skillsets(self) -> list[dict]:
        async with self.api.call_api(
            version=GROUP_VERSION, namespace=self.namespace, url="skillsets"
        ) as response:
            return response.json()["items"]

    async def source(self, kind: str, name: str) -> tuple[str, set[str]] | None:
        if kind not in ("ConfigMap", "Secret") or len(name) > 253 or not DNS_NAME.fullmatch(name):
            return None
        plural = "configmaps" if kind == "ConfigMap" else "secrets"
        try:
            async with self.api.call_api(
                namespace=self.namespace, url=f"{plural}/{name}"
            ) as response:
                resource = response.json()
        except ServerError as exc:
            if exc.response is not None and exc.response.status_code == 404:
                return None
            raise
        keys = set(resource.get("data") or {})
        if kind == "ConfigMap":
            keys.update(resource.get("binaryData") or {})
        return resource["metadata"]["resourceVersion"], keys

    async def update_status(self, obj: dict, status: dict) -> None:
        name = obj["metadata"]["name"]
        if len(name) > 253 or not DNS_NAME.fullmatch(name):
            raise ValueError("invalid SkillSet name")
        try:
            async with self.api.call_api(
                method="PATCH",
                version=GROUP_VERSION,
                namespace=self.namespace,
                url=f"skillsets/{name}/status",
                headers={"Content-Type": "application/merge-patch+json"},
                json={
                    "metadata": {"resourceVersion": obj["metadata"]["resourceVersion"]},
                    "status": status,
                },
            ):
                pass
        except ServerError as exc:
            if exc.response is not None and exc.response.status_code == 409:
                return
            raise
