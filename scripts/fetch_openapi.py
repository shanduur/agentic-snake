# Copyright 2026 Mateusz Urbanek.
"""Acquire a checksum-pinned Kubernetes OpenAPI fixture atomically."""

import argparse
import hashlib
import os
import tempfile
from pathlib import Path
from urllib.request import urlopen


def fetch(url: str, checksum: str, output: Path) -> None:
    if len(checksum) != 64 or any(c not in "0123456789abcdef" for c in checksum):
        raise ValueError("expected a lowercase SHA-256 checksum")
    if output.is_file() and hashlib.sha256(output.read_bytes()).hexdigest() == checksum:
        return
    with urlopen(url, timeout=30) as response:
        data = response.read(16 * 1024 * 1024 + 1)
    if len(data) > 16 * 1024 * 1024 or hashlib.sha256(data).hexdigest() != checksum:
        raise ValueError("OpenAPI fixture size or checksum mismatch")
    output.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(dir=output.parent)
    try:
        with os.fdopen(fd, "wb") as file:
            file.write(data)
        os.replace(temporary, output)
    finally:
        Path(temporary).unlink(missing_ok=True)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", required=True)
    parser.add_argument("--sha256", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if not args.url.startswith("https://"):
        parser.error("OpenAPI fixture URL must use HTTPS")
    fetch(args.url, args.sha256, args.output)


if __name__ == "__main__":
    main()
