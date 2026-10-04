# Copyright 2026 Mateusz Urbanek.
import subprocess
import unittest
from unittest.mock import patch

from scripts.test_crd_admission import cleanup


class CleanupTests(unittest.TestCase):
    def test_cleanup_uses_only_the_temporary_kubeconfig(self):
        with patch("scripts.test_crd_admission.subprocess.run") as command:
            self.assertTrue(cleanup("kind", "fixture", "fixture.kubeconfig", None))
            command.assert_called_once_with(
                [
                    "kind",
                    "delete",
                    "cluster",
                    "--name",
                    "fixture",
                    "--kubeconfig",
                    "fixture.kubeconfig",
                ],
                check=True,
                timeout=60,
            )

    def test_cleanup_failure_does_not_replace_admission_failure(self):
        error = subprocess.CalledProcessError(1, ["kind", "delete"])
        with (
            patch("scripts.test_crd_admission.subprocess.run", side_effect=error),
            patch("scripts.test_crd_admission.print") as warning,
        ):
            self.assertFalse(
                cleanup("kind", "fixture", "fixture.kubeconfig", AssertionError("admission failed"))
            )
            warning.assert_called_once()

    def test_cleanup_failure_fails_otherwise_successful_run(self):
        error = subprocess.CalledProcessError(1, ["kind", "delete"])
        with (
            patch("scripts.test_crd_admission.subprocess.run", side_effect=error),
            self.assertRaises(subprocess.CalledProcessError),
        ):
            cleanup("kind", "fixture", "fixture.kubeconfig", None)

    def test_cleanup_timeout_preserves_original_failure(self):
        with (
            patch(
                "scripts.test_crd_admission.subprocess.run",
                side_effect=subprocess.TimeoutExpired("kind", 60),
            ),
            patch("scripts.test_crd_admission.print") as warning,
        ):
            self.assertFalse(
                cleanup("kind", "fixture", "fixture.kubeconfig", AssertionError("admission failed"))
            )
            warning.assert_called_once()
