"""Check .github/workflows/changes.yml: run its script against sample lists of
changed files, with a stand-in for `gh`, and compare the areas it turns on
with what each change should trigger.

    uv run scripts/test_ci_changes.py      (from the repository root)
"""
# /// script
# dependencies = ["pyyaml"]
# ///

import os
import stat
import subprocess
import sys
import tempfile
from pathlib import Path

import yaml

wf = yaml.safe_load(Path(".github/workflows/changes.yml").read_text())
script = wf["jobs"]["detect"]["steps"][0]["run"]

CASES = {
    "docs only": ["docs/docs/apps/index.md", "docs/src/css/custom.css"],
    "README only": ["README.md"],
    "apps Go code": ["apps/publish.go", "apps/publish_test.go"],
    "apps SQL only": ["apps/db/migrations/0002_x.sql"],
    "plugin Go code": ["dynamic.go"],
    "the worker": ["caddysnake.py"],
    "an integration test app": ["tests/flask/main.py"],
    "PyPI wrapper": ["cmd/cli/caddysnake_cli.py"],
    "Rust stub": ["cmd/cli/src/lib.rs"],
    "Dockerfile": ["Dockerfile"],
    "docs dependencies": ["docs/package-lock.json"],
    "a workflow": [".github/workflows/lint.yml"],
    "push to main (not a pull request)": None,
}
EXPECT = {
    "docs only": {"docs"},
    "README only": set(),
    "apps Go code": {"go", "code", "apps", "integration", "docker", "cli", "embed"},
    "apps SQL only": {"apps"},
    "plugin Go code": {"go", "code", "apps", "integration", "docker", "cli", "embed"},
    "the worker": {"go", "python", "code", "apps", "integration", "docker", "cli", "embed"},
    "an integration test app": {"python", "code", "integration"},
    "PyPI wrapper": {"python", "code", "cli"},
    "Rust stub": {"cli", "rust"},
    "Dockerfile": {"docker"},
    "docs dependencies": {"docs", "js"},
}
KEYS = [
    "go",
    "python",
    "code",
    "apps",
    "integration",
    "docker",
    "cli",
    "embed",
    "docs",
    "rust",
    "js",
    "workflows",
]

failed = False
for name, files in CASES.items():
    with tempfile.TemporaryDirectory() as tmp:
        gh = os.path.join(tmp, "gh")
        with open(gh, "w") as f:
            f.write("#!/bin/sh\ncat <<'EOF'\n" + "\n".join(files or []) + "\nEOF\n")
        os.chmod(gh, os.stat(gh).st_mode | stat.S_IEXEC)
        out = os.path.join(tmp, "out")
        env = dict(
            os.environ,
            PATH=tmp + os.pathsep + os.environ["PATH"],
            GITHUB_OUTPUT=out,
            EVENT="pull_request" if files is not None else "push",
            PR="1",
            REPO="x/y",
            GH_TOKEN="x",
        )
        r = subprocess.run(["bash", "-c", script], env=env, capture_output=True, text=True)
        if r.returncode != 0:
            print(f"{name}: script failed\n{r.stderr}")
            failed = True
            continue
        vals = dict(line.split("=", 1) for line in Path(out).read_text().splitlines())
        on = {k for k in KEYS if vals.get(k) == "true"}
        missing = [k for k in KEYS + ["full", "pythons", "oses", "build_pythons"] if k not in vals]
        verdict = ""
        if name in EXPECT:
            verdict = "ok" if on == EXPECT[name] else f"UNEXPECTED (wanted {sorted(EXPECT[name])})"
            failed |= on != EXPECT[name]
        elif len(on) != len(KEYS):
            verdict, failed = "UNEXPECTED (wanted everything)", True
        else:
            verdict = "ok (everything)"
        if missing:
            verdict += f" MISSING OUTPUTS {missing}"
            failed = True
        print(
            f"{name:36} -> {', '.join(sorted(on)) or 'nothing'}  [{verdict}]  pythons={vals.get('pythons')} build={vals.get('build_pythons')}"
        )
sys.exit(1 if failed else 0)
