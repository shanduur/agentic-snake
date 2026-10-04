# Copyright 2026 Mateusz Urbanek.
"""Verify generated CRDs and admission against a disposable Kubernetes API server."""

import argparse
import copy
import json
import shutil
import subprocess
import sys
import tempfile
import uuid
from pathlib import Path

import yaml


def cleanup(kind: str, name: str, kubeconfig: str, primary_error: BaseException | None) -> bool:
    """Keep admission failures primary; failed cleanup still fails a green run."""
    try:
        subprocess.run(
            [kind, "delete", "cluster", "--name", name, "--kubeconfig", kubeconfig],
            check=True,
            timeout=60,
        )
    except (OSError, subprocess.CalledProcessError, subprocess.TimeoutExpired) as error:
        if primary_error is None:
            raise
        print(
            f"WARNING: could not delete test cluster {name}: {error}; retained {kubeconfig}",
            file=sys.stderr,
        )
        return False
    return True


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--kind", default="kind")
    parser.add_argument("--kubectl", default="kubectl")
    parser.add_argument("--image", required=True)
    parser.add_argument("--crd", type=Path, required=True)
    args = parser.parse_args()
    crd = yaml.safe_load(args.crd.read_text())
    name = "agentic-snake-crds-" + uuid.uuid4().hex[:8]
    directory = tempfile.mkdtemp(prefix="agentic-snake-crds-")
    kubeconfig = str(Path(directory) / "kubeconfig")
    base = [args.kubectl, "--kubeconfig", kubeconfig]
    try:
        subprocess.run(
            [
                args.kind,
                "create",
                "cluster",
                "--name",
                name,
                "--image",
                args.image,
                "--kubeconfig",
                kubeconfig,
                "--wait",
                "90s",
            ],
            check=True,
            timeout=180,
        )
        subprocess.run([*base, "apply", "-f", str(args.crd)], check=True, timeout=30)
        subprocess.run(
            [
                *base,
                "wait",
                "--for=condition=Established",
                "crd/" + crd["metadata"]["name"],
                "--timeout=30s",
            ],
            check=True,
            timeout=40,
        )
        valid = {
            "apiVersion": "agentic-snake.dev/v1alpha1",
            "kind": "SkillSet",
            "metadata": {"name": "admission-fixture"},
            "spec": {"sources": [{"kind": "ConfigMap", "name": "fixture", "key": "SKILL.md"}]},
        }

        def check(label, obj, accepted):
            result = subprocess.run(
                [*base, "create", "--dry-run=server", "-f", "-"],
                input=json.dumps(obj),
                capture_output=True,
                check=False,
                text=True,
                timeout=30,
            )
            if (result.returncode == 0) != accepted:
                raise AssertionError(f"{label}: unexpected admission result: {result.stderr}")
            if not accepted and " is invalid:" not in result.stderr.lower():
                raise AssertionError(
                    f"{label}: infrastructure error, not schema rejection: {result.stderr}"
                )
            print("PASS " + label, flush=True)

        check("valid source admitted", valid, True)
        for field in ["kind", "name", "key"]:
            obj = copy.deepcopy(valid)
            del obj["spec"]["sources"][0][field]
            check("missing source " + field + " rejected", obj, False)
        for field, value in [
            ("kind", "Pod"),
            ("name", "Bad_Name"),
            ("name", "x" * 254),
            ("key", ""),
            ("key", "x" * 254),
        ]:
            obj = copy.deepcopy(valid)
            obj["spec"]["sources"][0][field] = value
            check("invalid " + field + " rejected", obj, False)
        for count in [0, 33]:
            obj = copy.deepcopy(valid)
            obj["spec"]["sources"] *= count
            check(f"source count {count} rejected", obj, False)
        for count in [1, 32]:
            obj = copy.deepcopy(valid)
            obj["spec"]["sources"] *= count
            check(f"source count {count} admitted", obj, True)
        broken = copy.deepcopy(crd)
        broken["metadata"]["name"] = "broken.agentic-snake.dev"
        broken["spec"]["names"].update(plural="broken", singular="broken", kind="Broken")
        broken["spec"]["names"].pop("shortNames", None)
        schema = broken["spec"]["versions"][0]["schema"]["openAPIV3Schema"]
        del schema["properties"]["spec"]["properties"]["sources"]["items"]["type"]
        check("nonstructural CRD rejected by API server", broken, False)
    finally:
        if cleanup(args.kind, name, kubeconfig, sys.exception()):
            shutil.rmtree(directory)


if __name__ == "__main__":
    main()
