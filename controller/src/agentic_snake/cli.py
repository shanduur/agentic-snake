# Copyright (c) Mateusz Urbanek
"""Polling entrypoint. Revisions are observational, not runtime activation."""

from __future__ import annotations

import argparse
import asyncio
import logging

from kr8s.asyncio import api

from .kube import DNS_NAME, KubernetesClient
from .reconcile import reconcile


def namespace(value: str) -> str:
    if len(value) > 63 or not DNS_NAME.fullmatch(value):
        raise argparse.ArgumentTypeError("namespace must be a DNS label (1-63 characters)")
    return value


def interval(value: str) -> int:
    try:
        seconds = int(value)
    except ValueError as exc:
        raise argparse.ArgumentTypeError("poll interval must be an integer") from exc
    if not 1 <= seconds <= 3600:
        raise argparse.ArgumentTypeError("poll interval must be 1-3600 seconds")
    return seconds


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser(
        description="Observe namespace-scoped SkillSet source revisions"
    )
    result.add_argument("--namespace", type=namespace, default="agentic-snake")
    result.add_argument("--poll-interval", type=interval, default=30, metavar="SECONDS")
    result.add_argument("--once", action="store_true", help="reconcile once, then exit")
    return result


async def run(args: argparse.Namespace) -> None:
    client = await api(namespace=args.namespace)
    client.timeout = 15
    observer = KubernetesClient(client, args.namespace)
    while True:
        await reconcile(observer)
        if args.once:
            return
        await asyncio.sleep(args.poll_interval)


def main() -> None:
    args = parser().parse_args()
    logging.basicConfig(level=logging.INFO)
    try:
        asyncio.run(run(args))
    except KeyboardInterrupt:
        return
    except Exception:
        logging.error("SkillSet polling failed; no success status reported")
        raise SystemExit(1) from None
