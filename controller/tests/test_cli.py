# Copyright (c) Mateusz Urbanek
import pytest

from agentic_snake.cli import parser


def test_once_and_namespace_are_parsed():
    args = parser().parse_args(["--once", "--namespace", "agentic-snake", "--poll-interval", "15"])
    assert args.once is True
    assert args.namespace == "agentic-snake"
    assert args.poll_interval == 15


@pytest.mark.parametrize("value", ["0", "3601", "-1", "nan"])
def test_poll_interval_is_bounded(value):
    with pytest.raises(SystemExit):
        parser().parse_args(["--poll-interval", value])


def test_invalid_namespace_is_rejected():
    with pytest.raises(SystemExit):
        parser().parse_args(["--namespace", "../../default"])
