from unittest import mock

from caddysnake_cli import main
from click.testing import CliRunner


def test_max_dynamic_apps_is_forwarded():
    with (
        mock.patch("caddysnake_cli.os.path.exists", return_value=True),
        mock.patch("caddysnake_cli.os.execv") as execv,
    ):
        result = CliRunner().invoke(
            main,
            [
                "--server-type",
                "asgi",
                "--app",
                "main:app",
                "--max-dynamic-apps",
                "12",
            ],
        )

    assert result.exit_code == 0
    argv = execv.call_args.args[1]
    assert "--max-dynamic-apps" in argv
    assert argv[argv.index("--max-dynamic-apps") + 1] == "12"


def test_request_body_max_size_is_forwarded():
    with (
        mock.patch("caddysnake_cli.os.path.exists", return_value=True),
        mock.patch("caddysnake_cli.os.execv") as execv,
    ):
        result = CliRunner().invoke(
            main,
            [
                "--server-type",
                "asgi",
                "--app",
                "main:app",
                "--request-body-max-size",
                "2GB",
            ],
        )

    assert result.exit_code == 0
    argv = execv.call_args.args[1]
    assert "--request-body-max-size" in argv
    assert argv[argv.index("--request-body-max-size") + 1] == "2GB"


def test_cluster_cache_options_are_forwarded():
    with (
        mock.patch("caddysnake_cli.os.path.exists", return_value=True),
        mock.patch("caddysnake_cli.os.execv") as execv,
    ):
        result = CliRunner().invoke(
            main,
            [
                "--server-type",
                "asgi",
                "--app",
                "main:app",
                "--cache-mode",
                "cluster",
                "--cache-listen",
                ":7447",
                "--cache-advertise",
                "node-a:7447",
                "--cache-peer",
                "node-a:7447",
                "--cache-peer",
                "node-b:7447",
                "--cache-namespace",
                "my-app",
                "--cache-secret",
                "0123456789abcdef",
            ],
        )

    assert result.exit_code == 0
    argv = execv.call_args.args[1]
    assert argv.count("--cache-peer") == 2
    assert argv[argv.index("--cache-mode") + 1] == "cluster"
    assert argv[argv.index("--cache-namespace") + 1] == "my-app"
