from unittest import mock

import caddysnake_cli
import pytest
from caddysnake_cli import apps_argv, main
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


def test_mount_prefix_is_forwarded():
    with (
        mock.patch("caddysnake_cli.os.path.exists", return_value=True),
        mock.patch("caddysnake_cli.os.execv") as execv,
    ):
        result = CliRunner().invoke(
            main,
            [
                "--server-type",
                "wsgi",
                "--app",
                "main:app",
                "--mount-prefix",
                "/tools/inventory",
            ],
        )

    assert result.exit_code == 0
    argv = execv.call_args.args[1]
    assert "--mount-prefix" in argv
    assert argv[argv.index("--mount-prefix") + 1] == "/tools/inventory"


def test_memory_limit_is_forwarded():
    with (
        mock.patch("caddysnake_cli.os.path.exists", return_value=True),
        mock.patch("caddysnake_cli.os.execv") as execv,
    ):
        result = CliRunner().invoke(
            main,
            ["--server-type", "auto", "--app", "main:app", "--memory-limit", "256MiB"],
        )

    assert result.exit_code == 0
    argv = execv.call_args.args[1]
    assert argv[argv.index("--server-type") + 1] == "auto"
    assert argv[argv.index("--memory-limit") + 1] == "256MiB"


def test_apps_arguments_pass_through_and_pick_this_python():
    argv = apps_argv(["--listen", "127.0.0.1:9000", "--require-login"])
    assert argv[1:5] == ["python-apps", "--listen", "127.0.0.1:9000", "--require-login"]
    assert argv[-2:] == ["--python-path", caddysnake_cli.sys.executable]

    # An explicit interpreter is respected.
    assert apps_argv(["--python-path", "/usr/bin/python3.13"]).count("--python-path") == 1
    assert (
        apps_argv(["--python-path=/usr/bin/python3.13"])[-1] == "--python-path=/usr/bin/python3.13"
    )

    # Subcommands and help are not server starts, so nothing is added.
    assert apps_argv(["user", "add", "ana", "--admin"])[1:] == [
        "python-apps",
        "user",
        "add",
        "ana",
        "--admin",
    ]
    assert apps_argv(["call", "list_apps"])[1:] == ["python-apps", "call", "list_apps"]
    assert apps_argv(["--help"])[1:] == ["python-apps", "--help"]
    assert apps_argv(["install", "--require-login"])[1:] == [
        "python-apps",
        "install",
        "--require-login",
    ]
    assert apps_argv(["uninstall"])[1:] == ["python-apps", "uninstall"]


def test_apps_subcommand_execs_the_bundled_binary():
    with (
        mock.patch.object(caddysnake_cli.sys, "argv", ["caddysnake", "apps", "guide"]),
        mock.patch("caddysnake_cli.os.path.exists", return_value=True),
        mock.patch("caddysnake_cli.os.execve") as execve,
        mock.patch("caddysnake_cli.main") as server_main,
    ):
        caddysnake_cli.cli()

    server_main.assert_not_called()
    binary, argv, env = execve.call_args.args
    assert binary.endswith("caddysnake-cli")
    assert argv[1:] == ["python-apps", "guide"]
    assert env["PATH"].split(caddysnake_cli.os.pathsep)[0] == caddysnake_cli.os.path.dirname(
        caddysnake_cli.sys.executable
    )


def test_other_commands_still_reach_the_server_cli():
    with (
        mock.patch.object(
            caddysnake_cli.sys, "argv", ["caddysnake", "--server-type", "wsgi", "--app", "main:app"]
        ),
        mock.patch("caddysnake_cli.main") as server_main,
    ):
        caddysnake_cli.cli()
    server_main.assert_called_once()


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


def test_windows_runs_the_binary_as_a_child_and_waits():
    """Windows cannot exec: the binary runs as a child, Ctrl-C is waited out, its exit code is ours."""
    proc = mock.Mock()
    proc.wait.side_effect = [KeyboardInterrupt, 3]
    with (
        mock.patch.object(caddysnake_cli, "_WINDOWS", True),
        mock.patch.object(caddysnake_cli.sys, "argv", ["caddysnake", "apps", "guide"]),
        mock.patch("caddysnake_cli.os.path.exists", return_value=True),
        mock.patch("caddysnake_cli.subprocess.Popen", return_value=proc) as popen,
        mock.patch("caddysnake_cli.os.execve") as execve,
        pytest.raises(SystemExit) as exit_info,
    ):
        caddysnake_cli.cli()

    assert exit_info.value.code == 3
    assert proc.wait.call_count == 2
    execve.assert_not_called()
    argv = popen.call_args.args[0]
    assert argv[0].endswith("caddysnake-cli.exe")
    assert argv[1:] == ["python-apps", "guide"]
    path = popen.call_args.kwargs["env"]["PATH"].split(caddysnake_cli.os.pathsep)
    assert path[: len(caddysnake_cli._tool_dirs())] == caddysnake_cli._tool_dirs()


def test_tool_dirs_cover_where_uv_is_installed():
    dirs = caddysnake_cli._tool_dirs()
    assert dirs[0] == caddysnake_cli.os.path.dirname(caddysnake_cli.sys.executable)
    assert caddysnake_cli.sysconfig.get_path("scripts") in dirs
