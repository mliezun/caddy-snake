from unittest import mock

from click.testing import CliRunner

from caddysnake_cli import main


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
