"""End-to-end test of caddy-snake apps, driven the way an AI assistant drives it:
through the MCP endpoint, with the official MCP client.

Expects a server started with `caddy python-apps` at APPS_URL (default
http://localhost:9080) and no login required.
"""

from __future__ import annotations

import asyncio
import json
import os
import sys

import requests
from mcp import ClientSession
from mcp.client.streamable_http import streamable_http_client

BASE = os.environ.get("APPS_URL", "http://localhost:9080").rstrip("/")
APP = "lunch-orders"

MAIN_V1 = '''import os

import psycopg
from flask import Flask, redirect, render_template_string, request, url_for

app = Flask(__name__)

PAGE = """<!doctype html>
<title>Lunch</title>
<h1>Lunch orders</h1>
<form method="post" action="{{ url_for('add') }}">
  <input name="dish"><button>Add</button>
</form>
<ul>{% for dish in dishes %}<li>{{ dish }}</li>{% endfor %}</ul>
<p id="who">{{ who }}</p>"""


def db():
    return psycopg.connect(os.environ["DATABASE_URL"], autocommit=True)


@app.get("/")
def index():
    with db() as conn:
        dishes = [row[0] for row in conn.execute("SELECT dish FROM orders ORDER BY id")]
    who = request.headers.get("X-Forwarded-User", "nobody")
    return render_template_string(PAGE, dishes=dishes, who=who)


@app.post("/add")
def add():
    with db() as conn:
        conn.execute("INSERT INTO orders (dish) VALUES (%s)", (request.form["dish"],))
    return redirect(url_for("index"))
'''

MIGRATION_1 = "CREATE TABLE orders (id serial PRIMARY KEY, dish varchar(100) NOT NULL);\n"
MIGRATION_2 = "ALTER TABLE orders ADD COLUMN note varchar(200) NOT NULL DEFAULT '';\n"

# A FastAPI-style (ASGI) version of the same app, to prove both kinds are served.
MAIN_V2 = """import os

import psycopg


async def app(scope, receive, send):
    assert scope["type"] == "http"
    with psycopg.connect(os.environ["DATABASE_URL"]) as conn:
        count = conn.execute("SELECT count(*) FROM orders WHERE note = ''").fetchone()[0]
    body = f"draft={os.environ['APP_IS_DRAFT']} orders={count} root={scope['root_path']}".encode()
    await send({"type": "http.response.start", "status": 200,
                "headers": [(b"content-type", b"text/plain")]})
    await send({"type": "http.response.body", "body": body})
"""


class Assistant:
    def __init__(self, session: ClientSession):
        self.session = session

    async def call(self, tool: str, expect_error: bool = False, **args):
        result = await self.session.call_tool(tool, args)
        text = "".join(c.text for c in result.content if c.type == "text")
        if bool(result.is_error) != expect_error:
            raise AssertionError(
                f"{tool}: is_error={result.is_error}, expected {expect_error}\n{text}"
            )
        try:
            return json.loads(text)
        except json.JSONDecodeError:
            return text


def get(path: str, **kwargs) -> requests.Response:
    return requests.get(f"{BASE}{path}", timeout=60, allow_redirects=False, **kwargs)


async def run() -> None:
    async with (
        streamable_http_client(f"{BASE}/_apps/mcp") as (read, write),
        ClientSession(read, write) as session,
    ):
        init = await session.initialize()
        assert init.server_info.name == "caddy-snake-apps", init.server_info
        assert "relative links" in (init.instructions or ""), (
            "the guide is missing from the instructions"
        )

        tools = {t.name for t in (await session.list_tools()).tools}
        for name in (
            "guide",
            "create_app",
            "save_files",
            "publish_draft",
            "roll_back",
            "get_logs",
        ):
            assert name in tools, f"tool {name} is not listed"
        print("  OK  connected over MCP; tools and instructions are present")

        ai = Assistant(session)

        # Leftovers from an earlier run.
        existing = {a["slug"] for a in (await ai.call("list_apps"))["apps"]}
        if APP in existing:
            await ai.call("delete_app", app=APP, confirm=APP)

        created = await ai.call("create_app", name="Lunch orders", description="Friday lunch")
        assert created["app"]["slug"] == APP and created["publish"]["ok"], created
        assert get(f"/{APP}/").status_code == 200
        print("  OK  create_app publishes a working starter")

        saved = await ai.call(
            "save_files",
            app=APP,
            message="Order list with a form",
            files={
                "main.py": MAIN_V1,
                "requirements.txt": "flask\npsycopg[binary]\n",
                "migrations/001_orders.sql": MIGRATION_1,
                "static/style.css": None,
            },
        )
        assert saved["publish"]["ok"], saved
        assert any("001_orders.sql" in step for step in saved["publish"]["steps"]), saved
        v1 = saved["version"]

        page = get(f"/{APP}/")
        assert page.status_code == 200 and f'action="/{APP}/add"' in page.text, page.text
        posted = requests.post(
            f"{BASE}/{APP}/add", data={"dish": "soup"}, timeout=60, allow_redirects=False
        )
        assert posted.status_code == 302 and posted.headers["Location"] == f"/{APP}/", (
            posted.headers
        )
        assert "<li>soup</li>" in get(f"/{APP}/").text
        # Nobody is signed in, so a spoofed identity header must not reach the app.
        assert (
            '<p id="who">nobody</p>'
            in get(f"/{APP}/", headers={"X-Forwarded-User": "mallory"}).text
        )
        print(
            "  OK  a Flask app with its own database works under its path; links include the path"
        )

        rows = await ai.call("run_sql", app=APP, sql="SELECT dish FROM orders")
        assert rows["rows"] == [{"dish": "soup"}], rows

        broken = await ai.call(
            "save_files",
            expect_error=True,
            app=APP,
            message="Oops",
            files={"main.py": "import flask\n\ndef broken(:\n    pass\n"},
        )
        assert not broken["publish"]["ok"] and "SyntaxError" in broken["publish"]["error"], broken
        assert "<li>soup</li>" in get(f"/{APP}/").text, "a failed publish took the app down"
        print("  OK  a broken version is refused, explained, and the old one keeps running")

        back = await ai.call("roll_back", app=APP, version=v1)
        assert back["ok"], back

        draft = await ai.call("create_draft", app=APP, name="New look")
        assert draft["draft"]["slug"] == f"{APP}--new-look" and draft["publish"]["ok"], draft
        if draft["data_copied"]:
            assert "<li>soup</li>" in get(f"/{APP}--new-look/").text, (
                "the draft did not get a copy of the data"
            )
        changed = await ai.call(
            "save_files",
            app=APP,
            draft="new look",
            message="New page",
            files={"main.py": MAIN_V2, "migrations/002_note.sql": MIGRATION_2},
        )
        assert changed["publish"]["ok"], changed
        draft_page = get(f"/{APP}--new-look/")
        assert draft_page.status_code == 200 and draft_page.text.startswith("draft=1 "), (
            draft_page.text
        )
        assert f"root=/{APP}--new-look" in draft_page.text, draft_page.text
        assert "<li>soup</li>" in get(f"/{APP}/").text, "changing the draft changed the live app"
        print("  OK  a draft has its own address and data, and does not touch the live app")

        published = await ai.call("publish_draft", app=APP, draft="new look")
        assert published["ok"] and any("002_note.sql" in s for s in published["steps"]), published
        live = get(f"/{APP}/")
        assert live.text == f"draft=0 orders=1 root=/{APP}", live.text
        assert get(f"/{APP}--new-look/").status_code == 404
        print(
            "  OK  publishing a draft applies its database change to live data; an ASGI app is served too"
        )

        await ai.call("set_setting", app=APP, name="GREETING", value="hello")
        info = await ai.call("get_app", app=APP)
        assert info["settings"] == ["GREETING"] and len(info["versions"]) >= 4, info

        logs = await ai.call("get_logs", app=APP)
        assert any("published version" in line["line"] for line in logs["lines"]), logs
        status = await ai.call("get_status")
        assert isinstance(status["awake"], list), status

        await ai.call("delete_app", expect_error=True, app=APP, confirm="wrong")
        await ai.call("delete_app", app=APP, confirm=APP)
        assert get(f"/{APP}/").status_code == 404
        print("  OK  settings, logs, status and delete")


def check_dashboard_and_guards() -> None:
    home = get("/")
    assert home.status_code == 200 and "Create app" in home.text
    assert get("/_apps/health").json() == {"status": "ok"}
    # A web page on another site must not be able to reach the tools.
    cross = requests.post(
        f"{BASE}/_apps/api/tools/list_apps",
        json={},
        headers={"Origin": "http://evil.example"},
        timeout=30,
    )
    assert cross.status_code == 403, cross.status_code
    rebind = get("/", headers={"Host": "evil.example"})
    assert rebind.status_code == 421, rebind.status_code
    form = requests.post(
        f"{BASE}/_apps/api/tools/list_apps",
        data="{}",
        headers={"Content-Type": "text/plain"},
        timeout=30,
    )
    assert form.status_code == 415, form.status_code
    print("  OK  dashboard is served; cross-site and wrong-host requests are refused")


def main() -> int:
    check_dashboard_and_guards()
    asyncio.run(run())
    print("apps_e2e integration tests passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
