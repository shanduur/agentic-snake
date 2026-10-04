# Copyright (c) Mateusz Urbanek
"""Generated CRD contract and drift checks."""

import sys
from pathlib import Path

import pytest
import yaml
from pydantic import ValidationError, create_model

import agentic_snake.crd as crd_module
from agentic_snake.crd import SkillSetSpec, SourceReference, crd_document, main, render

CRD = Path(__file__).resolve().parents[2] / "deploy/skillset-crd.yaml"


@pytest.mark.parametrize(
    ("annotation", "default"),
    [
        (str, "enabled"),
        (str, ""),
        (bool, False),
        (int, 0),
        (list[str], []),
        (dict[str, str], {}),
        (str | None, "enabled"),
    ],
)
def test_generator_rejects_non_null_model_defaults(monkeypatch, annotation, default):
    model = create_model(
        "DefaultedSkillSet", __base__=crd_module.SkillSet, setting=(annotation, default)
    )
    monkeypatch.setattr(crd_module, "SkillSet", model)
    with pytest.raises(ValueError, match="non-null defaults are unsupported"):
        crd_module.resource_schema()


def test_generated_document_matches_checked_in_artifact():
    assert CRD.read_text() == render()
    assert yaml.safe_load(CRD.read_text()) == crd_document()


def test_check_detects_drift_without_rewriting(tmp_path, monkeypatch):
    artifact = tmp_path / "skillset-crd.yaml"
    command = ["agentic_snake.crd", "--output", str(artifact)]
    monkeypatch.setattr(sys, "argv", command)
    assert main() == 0
    original = artifact.read_text()
    monkeypatch.setattr(sys, "argv", [*command, "--check"])
    assert main() == 0
    artifact.write_text(original + "# drift\n")
    assert main() == 1
    assert artifact.read_text() == original + "# drift\n"


def test_nested_fields_and_structural_schema():
    document = crd_document()
    assert document["metadata"]["name"] == "skillsets.agentic-snake.dev"
    version = document["spec"]["versions"][0]
    assert version["subresources"] == {"status": {}}
    schema = version["schema"]["openAPIV3Schema"]
    assert "required" not in schema  # Existing admission does not require root spec.
    assert schema["properties"]["spec"]["required"] == ["sources"]
    sources = schema["properties"]["spec"]["properties"]["sources"]
    assert sources["minItems"] == 1 and sources["maxItems"] == 32
    assert sources["items"]["required"] == ["kind", "name", "key"]
    assert sources["items"]["properties"]["kind"]["enum"] == ["ConfigMap", "Secret"]
    assert schema["properties"]["status"]["properties"]["observedGeneration"]["format"] == "int64"

    def walk(value):
        if isinstance(value, dict):
            assert (
                not {"$ref", "$defs", "definitions", "anyOf", "oneOf", "allOf", "default", "title"}
                & value.keys()
            )
            for child in value.values():
                walk(child)
        elif isinstance(value, list):
            for child in value:
                walk(child)

    walk(schema)


@pytest.mark.parametrize(
    "source",
    [
        {"kind": "Service", "name": "valid", "key": "file"},
        {"kind": "Secret", "name": "BAD", "key": "file"},
        {"kind": "Secret", "name": "valid", "key": ""},
        {"kind": "Secret", "name": "x" * 254, "key": "file"},
        {"kind": "Secret", "name": "valid", "key": "x" * 254},
    ],
)
def test_invalid_source_contract(source):
    with pytest.raises(ValidationError):
        SourceReference.model_validate(source)


@pytest.mark.parametrize("count", [0, 33])
def test_invalid_source_count(count):
    with pytest.raises(ValidationError):
        SkillSetSpec.model_validate(
            {"sources": [{"kind": "Secret", "name": "ok", "key": "a"}] * count}
        )


def test_missing_nested_fields():
    with pytest.raises(ValidationError):
        SourceReference.model_validate({"kind": "ConfigMap", "name": "ok"})
    with pytest.raises(ValidationError):
        SkillSetSpec.model_validate({})
