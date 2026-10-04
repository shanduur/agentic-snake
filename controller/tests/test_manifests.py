# Copyright (c) Mateusz Urbanek
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]


def test_crd_and_deployment_match_controller_contract():
    crd = yaml.safe_load((ROOT / "deploy/skillset-crd.yaml").read_text())
    resources = list(yaml.safe_load_all((ROOT / "deploy/controller.yaml").read_text()))
    assert crd["spec"]["scope"] == "Namespaced"
    assert crd["spec"]["group"] == "agentic-snake.dev"
    schema = crd["spec"]["versions"][0]
    assert schema["subresources"] == {"status": {}}
    source = schema["schema"]["openAPIV3Schema"]["properties"]["spec"]["properties"]["sources"]
    assert source["maxItems"] == 32
    assert source["items"]["required"] == ["kind", "name", "key"]
    by_kind = {obj["kind"]: obj for obj in resources}
    assert by_kind["Namespace"]["metadata"]["name"] == "agentic-snake"
    for resource in resources[1:]:
        assert resource["metadata"]["namespace"] == "agentic-snake"
    role = by_kind["Role"]["rules"]
    assert role == [
        {"apiGroups": ["agentic-snake.dev"], "resources": ["skillsets"], "verbs": ["get", "list"]},
        {
            "apiGroups": ["agentic-snake.dev"],
            "resources": ["skillsets/status"],
            "verbs": ["get", "patch"],
        },
        {"apiGroups": [""], "resources": ["configmaps", "secrets"], "verbs": ["get"]},
    ]
    deployment = by_kind["Deployment"]["spec"]["template"]["spec"]
    container = deployment["containers"][0]
    assert deployment["serviceAccountName"] == "agentic-snake-controller"
    assert deployment["securityContext"]["runAsNonRoot"] is True
    assert container["image"] == "agentic-snake-controller:dev"
    assert container["securityContext"]["readOnlyRootFilesystem"] is True
    assert container["securityContext"]["allowPrivilegeEscalation"] is False
