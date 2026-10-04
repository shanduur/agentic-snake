# Copyright (c) Mateusz Urbanek
"""Validate CRD documents with Kubernetes's published OpenAPI2 definitions.

This is a static compatibility check, not API-server structural admission.
"""

from __future__ import annotations

import argparse
import json
from collections.abc import Sequence
from pathlib import Path
from typing import Any

import yaml
from jsonschema import Draft4Validator, ValidationError

CRD_DEFINITION = "io.k8s.apiextensions-apiserver.pkg.apis.apiextensions.v1.CustomResourceDefinition"
SCHEMA_DEFINITION = "io.k8s.apiextensions-apiserver.pkg.apis.apiextensions.v1.JSONSchemaProps"


def _definition_validator(definitions: dict[str, Any], name: str):
    if name not in definitions:
        raise ValueError(f"Kubernetes OpenAPI missing definition {name}")
    return Draft4Validator({"$ref": f"#/definitions/{name}", "definitions": definitions})


def validate_document(
    openapi: dict[str, Any], crd: dict[str, Any], resources: Sequence[dict[str, Any]] = ()
) -> None:
    """Check official CRD/JSONSchemaProps shapes and JSON Schema resource fixtures."""
    definitions = openapi["definitions"]
    _definition_validator(definitions, CRD_DEFINITION).validate(crd)
    # The OpenAPI2 CRD definition types kind/apiVersion as strings, but does not
    # constrain their values to this endpoint's GVK.
    for field, expected in (
        ("apiVersion", "apiextensions.k8s.io/v1"),
        ("kind", "CustomResourceDefinition"),
    ):
        if crd.get(field) != expected:
            raise ValidationError(f"{field} must equal {expected}")
    schema_validator = _definition_validator(definitions, SCHEMA_DEFINITION)
    for version in crd["spec"]["versions"]:
        schema = version["schema"]["openAPIV3Schema"]
        schema_validator.validate(schema)
        Draft4Validator.check_schema(schema)
        for resource in resources:
            Draft4Validator(schema).validate(resource)


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Validate SkillSet CRD against official Kubernetes OpenAPI2"
    )
    parser.add_argument(
        "--openapi", type=Path, required=True, help="verified Kubernetes swagger.json"
    )
    parser.add_argument("--crd", type=Path, required=True, help="generated CRD YAML")
    parser.add_argument(
        "--resource",
        type=Path,
        action="append",
        default=[],
        help="SkillSet YAML fixture (repeatable)",
    )
    args = parser.parse_args()
    openapi = json.loads(args.openapi.read_text())
    crd = yaml.safe_load(args.crd.read_text())
    resources = [yaml.safe_load(path.read_text()) for path in args.resource]
    validate_document(openapi, crd, resources)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
