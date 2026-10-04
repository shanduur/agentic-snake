# Copyright (c) Mateusz Urbanek
"""Official Kubernetes OpenAPI2 CRD envelope and local instance checks."""

import copy
import json
import os
import sys
from pathlib import Path

import pytest
import yaml
from jsonschema import ValidationError
from jsonschema.exceptions import SchemaError

from agentic_snake.validate_crds import main, validate_document

ROOT = Path(__file__).resolve().parents[2]
OPENAPI = Path(
    os.environ.get("KUBERNETES_OPENAPI", ROOT / ".cache/kubernetes/v1.34.0/swagger.json")
)


@pytest.fixture(scope="module")
def official_openapi():
    if not OPENAPI.is_file():
        pytest.fail("official Kubernetes swagger fixture not acquired; set KUBERNETES_OPENAPI")
    return json.loads(OPENAPI.read_text())


@pytest.fixture
def crd():
    return yaml.safe_load((ROOT / "deploy/skillset-crd.yaml").read_text())


def test_official_crd_envelope_and_embedded_schema(official_openapi, crd):
    validate_document(official_openapi, crd)


@pytest.mark.parametrize(
    "mutation",
    [
        lambda crd: crd.update(kind="Deployment"),
        lambda crd: crd["spec"].pop("names"),
        lambda crd: crd["spec"].update(scope=17),
        lambda crd: crd["spec"]["versions"][0]["schema"].update(openAPIV3Schema="wrong"),
    ],
)
def test_official_envelope_rejects_invalid_documents(official_openapi, crd, mutation):
    mutation(crd)
    with pytest.raises(ValidationError):
        validate_document(official_openapi, crd)


def test_resource_fixture_accepts_absent_status(official_openapi, crd):
    resource = {
        "apiVersion": "agentic-snake.dev/v1alpha1",
        "kind": "SkillSet",
        "metadata": {"name": "example"},
        "spec": {"sources": [{"kind": "Secret", "name": "my-secret", "key": "SKILL.md"}]},
    }
    validate_document(official_openapi, crd, [resource])
    resource["status"] = {"valid": True, "observedGeneration": 1, "sourceVersions": ["42"]}
    validate_document(official_openapi, crd, [resource])


@pytest.mark.parametrize(
    "change",
    [
        lambda obj: obj["spec"].pop("sources"),
        lambda obj: obj["spec"]["sources"][0].pop("key"),
        lambda obj: obj["spec"]["sources"][0].update(kind="Service"),
        lambda obj: obj["spec"]["sources"].clear(),
        lambda obj: obj.update(status={"observedGeneration": "not an integer"}),
    ],
)
def test_resource_fixture_rejects_invalid_contract(official_openapi, crd, change):
    resource = {
        "apiVersion": "agentic-snake.dev/v1alpha1",
        "kind": "SkillSet",
        "spec": {"sources": [{"kind": "Secret", "name": "good", "key": "SKILL.md"}]},
    }
    change(resource)
    with pytest.raises(ValidationError):
        validate_document(official_openapi, crd, [resource])


def test_cli_resource_fixture(official_openapi, tmp_path, monkeypatch):
    valid = tmp_path / "valid.yaml"
    valid.write_text(
        "apiVersion: agentic-snake.dev/v1alpha1\nkind: SkillSet\nspec:\n"
        "  sources:\n  - kind: Secret\n    name: my-secret\n    key: SKILL.md\n"
    )
    command = [
        "agentic_snake.validate_crds",
        "--openapi",
        str(OPENAPI),
        "--crd",
        str(ROOT / "deploy/skillset-crd.yaml"),
        "--resource",
        str(valid),
    ]
    monkeypatch.setattr(sys, "argv", command)
    assert main() == 0
    valid.write_text("apiVersion: agentic-snake.dev/v1alpha1\nkind: SkillSet\nspec: {}\n")
    with pytest.raises(ValidationError):
        main()


def test_official_schema_rejects_unsupported_embedded_type(official_openapi, crd):
    broken = copy.deepcopy(crd)
    broken["spec"]["versions"][0]["schema"]["openAPIV3Schema"]["type"] = "fictional"
    with pytest.raises(SchemaError):
        validate_document(official_openapi, broken)
