# Copyright 2026 Mateusz Urbanek.
import hashlib
import tempfile
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

from scripts.fetch_openapi import fetch


class FixtureTests(unittest.TestCase):
    def test_cached_file_is_verified_without_network(self):
        data = b'{"swagger":"2.0"}'
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "swagger.json"
            output.write_bytes(data)
            with patch("scripts.fetch_openapi.urlopen") as download:
                fetch(
                    "https://example.invalid/schema",
                    hashlib.sha256(data).hexdigest(),
                    output,
                )
                download.assert_not_called()

    def test_checksum_failure_preserves_previous_fixture(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "swagger.json"
            output.write_bytes(b"old")
            response = MagicMock()
            response.__enter__.return_value.read.return_value = b"corrupt"
            with (
                patch("scripts.fetch_openapi.urlopen", return_value=response),
                self.assertRaisesRegex(ValueError, "checksum mismatch"),
            ):
                fetch("https://example.invalid/schema", "0" * 64, output)
            self.assertEqual(output.read_bytes(), b"old")

    def test_valid_download_replaces_corrupt_cache(self):
        data = b'{"swagger":"2.0"}'
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "swagger.json"
            output.write_bytes(b"corrupt")
            response = MagicMock()
            response.__enter__.return_value.read.return_value = data
            with patch("scripts.fetch_openapi.urlopen", return_value=response):
                fetch(
                    "https://example.invalid/schema",
                    hashlib.sha256(data).hexdigest(),
                    output,
                )
            self.assertEqual(output.read_bytes(), data)

    def test_empty_checksum_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "SHA-256"):
            fetch("https://example.invalid/schema", "", Path("unused"))
