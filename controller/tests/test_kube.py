# Copyright (c) Mateusz Urbanek
import asyncio
from contextlib import asynccontextmanager

import httpx
import pytest
from kr8s._exceptions import ServerError

from agentic_snake.kube import KubernetesClient


class FakeAPI:
    def __init__(self):
        self.requests = []

    @asynccontextmanager
    async def call_api(self, **kwargs):
        self.requests.append(kwargs)
        if kwargs.get("url") == "secrets/credential":
            yield FakeResponse(
                {"metadata": {"resourceVersion": "6"}, "data": {"token": "SECRET_MUST_NOT_ESCAPE"}}
            )
        elif kwargs.get("url") == "skillsets":
            yield FakeResponse({"items": [{"metadata": {"name": "demo"}}]})
        else:
            yield FakeResponse({})


class FakeResponse:
    def __init__(self, payload):
        self.payload = payload

    def json(self):
        return self.payload


def test_adapter_reads_only_presence_and_resource_version():
    api = FakeAPI()
    client = KubernetesClient(api, "agentic-snake")
    result = asyncio.run(client.source("Secret", "credential"))
    assert result == ("6", {"token"})
    assert "SECRET_MUST_NOT_ESCAPE" not in str(result)
    assert api.requests[0]["namespace"] == "agentic-snake"


def test_adapter_writes_status_subresource_with_resource_version():
    api = FakeAPI()
    client = KubernetesClient(api, "agentic-snake")
    asyncio.run(
        client.update_status(
            {"metadata": {"name": "demo", "resourceVersion": "12"}}, {"valid": False}
        )
    )
    request = api.requests[0]
    assert request["method"] == "PATCH"
    assert request["url"] == "skillsets/demo/status"
    assert request["json"]["metadata"] == {"resourceVersion": "12"}
    assert request["headers"]["Content-Type"] == "application/merge-patch+json"


def test_adapter_lists_namespaced_custom_resources():
    api = FakeAPI()
    client = KubernetesClient(api, "agentic-snake")
    assert asyncio.run(client.list_skillsets()) == [{"metadata": {"name": "demo"}}]
    assert api.requests[0]["namespace"] == "agentic-snake"
    assert api.requests[0]["version"] == "agentic-snake.dev/v1alpha1"


class ErrorAPI:
    def __init__(self, code):
        self.code = code

    @asynccontextmanager
    async def call_api(self, **kwargs):
        response = httpx.Response(self.code, request=httpx.Request("GET", "https://example.test"))
        raise ServerError("request failed", response=response)
        yield  # pragma: no cover


def test_missing_source_is_invalid_not_a_transport_failure():
    assert (
        asyncio.run(KubernetesClient(ErrorAPI(404), "agentic-snake").source("Secret", "gone"))
        is None
    )


def test_forbidden_source_is_not_reported_as_invalid():
    with pytest.raises(ServerError):
        asyncio.run(KubernetesClient(ErrorAPI(403), "agentic-snake").source("Secret", "gone"))


def test_conflicting_status_patch_is_skipped():
    asyncio.run(
        KubernetesClient(ErrorAPI(409), "agentic-snake").update_status(
            {"metadata": {"name": "demo", "resourceVersion": "12"}}, {"valid": True}
        )
    )


@pytest.mark.parametrize("code", [403, 404, 500])
def test_nonconflicting_status_patch_error_propagates(code):
    with pytest.raises(ServerError):
        asyncio.run(
            KubernetesClient(ErrorAPI(code), "agentic-snake").update_status(
                {"metadata": {"name": "demo", "resourceVersion": "12"}}, {"valid": True}
            )
        )
