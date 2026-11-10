"""Record the demo GIF shown in the README and the docs (docs/static/img/apps-demo.gif).

It starts a real server on port 9080 with an empty data folder, publishes a
real app through the tools, takes real screenshots with headless Chrome and
joins them with ffmpeg. The cards between the screenshots say what the person
asked their assistant for.

    go build -o /tmp/caddysnake ./cmd/cli
    python3 scripts/record_demo.py /tmp/caddysnake docs/static/img/apps-demo.gif

Needs Chrome (set CHROME to its path if it is not the macOS default), ffmpeg,
the Postgres programs and network access (the app installs Flask). Working
files go to a temporary folder.
"""

import html
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.parse
import urllib.request
from pathlib import Path

HERE = tempfile.mkdtemp(prefix="caddysnake-demo-")
BINARY, OUT = sys.argv[1], sys.argv[2]
URL = "http://localhost:9080"
CHROME = os.environ.get("CHROME", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome")
W, H = 1120, 700
FRAMES = os.path.join(HERE, "frames")
DATA = os.path.join(HERE, "data")

MAIN_V2 = '''import os

import psycopg
from flask import Flask, redirect, render_template_string, request, url_for

app = Flask(__name__)

PAGE = """<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Friday lunch</title>
  <link rel="stylesheet" href="{{ url_for('static', filename='style.css') }}">
</head>
<body>
  <main>
    <h1>Friday lunch</h1>
    <p class="lead">{{ people|length }} coming this Friday</p>
    <form method="post" action="{{ url_for('add') }}">
      <input name="name" placeholder="Your name" maxlength="60" required>
      <input name="dish" placeholder="What are you having?" maxlength="80" required>
__EXTRA_INPUT__
      <button>Count me in</button>
    </form>
    <table>
      <tr><th>Name</th><th>Dish</th>__EXTRA_TH__</tr>
      {% for p in people %}<tr><td>{{ p[0] }}</td><td>{{ p[1] }}</td>__EXTRA_TD__</tr>{% endfor %}
    </table>
  </main>
</body>
</html>"""


def db():
    return psycopg.connect(os.environ["DATABASE_URL"])


@app.get("/")
def index():
    with db() as conn:
        people = conn.execute("__SELECT__").fetchall()
    return render_template_string(PAGE, people=people)


@app.post("/add")
def add():
__ADD__
    return redirect(url_for("index"))
'''

V2 = (
    MAIN_V2.replace("__EXTRA_INPUT__\n", "")
    .replace("__EXTRA_TH__", "")
    .replace("__EXTRA_TD__", "")
    .replace("__SELECT__", "select name, dish from signups order by id")
    .replace(
        "__ADD__",
        '    name, dish = request.form["name"][:60], request.form["dish"][:80]\n'
        "    with db() as conn:\n"
        '        conn.execute("insert into signups (name, dish) values (%s, %s)", (name, dish))',
    )
)
V3 = (
    MAIN_V2.replace(
        "__EXTRA_INPUT__",
        '      <select name="diet"><option value="">No dietary needs</option><option>Vegetarian</option><option>Vegan</option><option>Gluten free</option></select>',
    )
    .replace("__EXTRA_TH__", "<th>Dietary needs</th>")
    .replace("__EXTRA_TD__", '<td>{% if p[2] %}<span class="tag">{{ p[2] }}</span>{% endif %}</td>')
    .replace("__SELECT__", "select name, dish, diet from signups order by id")
    .replace(
        "__ADD__",
        '    name, dish, diet = request.form["name"][:60], request.form["dish"][:80], request.form.get("diet", "")[:20]\n'
        "    with db() as conn:\n"
        '        conn.execute("insert into signups (name, dish, diet) values (%s, %s, %s)", (name, dish, diet))',
    )
)

CSS = """body { font-family: system-ui, sans-serif; margin: 0; background: #f4f6fa; color: #16191f; }
main { max-width: 760px; margin: 48px auto; background: #fff; padding: 36px 40px; border-radius: 16px; box-shadow: 0 1px 3px rgba(0,0,0,.08), 0 12px 32px rgba(20,40,80,.06); }
h1 { margin: 0 0 4px; font-size: 30px; }
.lead { margin: 0 0 24px; color: #5b6472; }
form { display: flex; gap: 10px; margin-bottom: 26px; }
input, select { flex: 1; padding: 11px 13px; border: 1px solid #cfd6e0; border-radius: 10px; font-size: 15px; background: #fff; }
button { padding: 11px 20px; border: 0; border-radius: 999px; background: #1f5eff; color: #fff; font-size: 15px; font-weight: 600; }
table { width: 100%; border-collapse: collapse; }
th { text-align: left; font-size: 12px; text-transform: uppercase; letter-spacing: .05em; color: #5b6472; padding: 8px 6px; border-bottom: 1px solid #dfe4ec; }
td { padding: 12px 6px; border-bottom: 1px solid #eef1f5; font-size: 15px; }
.tag { background: #e7f5ec; color: #17633a; padding: 3px 10px; border-radius: 999px; font-size: 13px; }
"""

MIG1 = "create table signups (\n    id serial primary key,\n    name varchar(60) not null,\n    dish varchar(80) not null\n);\n"
MIG2 = "alter table signups add column diet varchar(20) not null default '';\n"


def tool(name, args):
    req = urllib.request.Request(
        f"{URL}/_apps/api/tools/{name}",
        data=json.dumps(args).encode(),
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=600) as r:
        out = json.load(r)
    if "error" in out or (isinstance(out.get("publish"), dict) and not out["publish"]["ok"]):
        sys.exit(f"{name} failed: {json.dumps(out)[:1500]}")
    return out


def signup(**fields):
    data = urllib.parse.urlencode(fields).encode()
    urllib.request.urlopen(
        urllib.request.Request(f"{URL}/friday-lunch/add", data=data), timeout=60
    ).read()


def chrome(url, png, width=W, height=H):
    if os.path.exists(png):
        os.remove(png)
    r = subprocess.run(
        [
            CHROME,
            "--headless=new",
            "--disable-gpu",
            "--hide-scrollbars",
            "--force-device-scale-factor=1",
            "--no-first-run",
            "--no-default-browser-check",
            f"--user-data-dir={os.path.join(HERE, 'chrome-profile')}",
            f"--window-size={width},{height}",
            "--virtual-time-budget=4000",
            f"--screenshot={png}",
            url,
        ],
        capture_output=True,
        timeout=120,
    )
    # Chrome often exits with a teardown error after writing the file.
    if not os.path.exists(png):
        sys.exit("chrome failed: " + r.stderr.decode()[-1500:])


PAGE = """<!doctype html><meta charset="utf-8"><style>
html,body{margin:0;height:100%;font-family:-apple-system,system-ui,sans-serif;background:linear-gradient(135deg,#e9eef8,#f6f1fa 60%,#e8f3f1);overflow:hidden}
.wrap{position:absolute;inset:0;display:flex;flex-direction:column;padding:22px 26px 20px;box-sizing:border-box}
.cap{font-size:19px;font-weight:600;color:#1b2230;margin:0 0 14px 4px;display:flex;align-items:center;gap:12px}
.step{font-size:12px;font-weight:700;letter-spacing:.06em;text-transform:uppercase;color:#fff;background:#1f5eff;border-radius:999px;padding:4px 11px}
.win{flex:1;border-radius:14px;overflow:hidden;background:#fff;box-shadow:0 2px 6px rgba(0,0,0,.10),0 18px 48px rgba(20,40,90,.14);display:flex;flex-direction:column}
.bar{height:38px;background:#eef1f6;border-bottom:1px solid #dde2ea;display:flex;align-items:center;padding:0 14px}
.addr{flex:1;max-width:520px;margin:0 auto;background:#fff;border:1px solid #d9dfe8;border-radius:999px;font-size:13px;color:#4a5363;padding:5px 16px;text-align:center}
.win img{display:block;width:100%}
.card{position:absolute;inset:0;display:flex;flex-direction:column;align-items:center;justify-content:center;padding:0 110px;text-align:center}
.who{font-size:15px;font-weight:700;letter-spacing:.08em;text-transform:uppercase;color:#1f5eff;margin-bottom:22px}
.say{font-size:40px;line-height:1.25;font-weight:650;color:#141a26}
.sub{margin-top:26px;font-size:19px;color:#566070}
</style>"""


def card(png, who, say, sub=""):
    path = os.path.join(FRAMES, "card.html")
    Path(path).write_text(
        PAGE
        + f'<div class="card"><div class="who">{html.escape(who)}</div><div class="say">{html.escape(say)}</div>'
        + (f'<div class="sub">{html.escape(sub)}</div>' if sub else "")
        + "</div>"
    )
    chrome("file://" + path, png)


def framed(png, step, caption, address, url):
    raw = png.replace(".png", "-raw.png")
    chrome(url, raw, width=W - 52, height=H - 22 - 20 - 38 - 38)
    path = os.path.join(FRAMES, "frame.html")
    Path(path).write_text(
        PAGE
        + f'<div class="wrap"><div class="cap"><span class="step">{html.escape(step)}</span>{html.escape(caption)}</div>'
        f'<div class="win"><div class="bar"><div class="addr">{html.escape(address)}</div></div><img src="{os.path.basename(raw)}"></div></div>'
    )
    chrome("file://" + path, png)


shutil.rmtree(FRAMES, ignore_errors=True)
shutil.rmtree(DATA, ignore_errors=True)
os.makedirs(FRAMES)
log = open(os.path.join(HERE, "server.log"), "w")  # noqa: SIM115 - held open for the server
server = subprocess.Popen([BINARY, "python-apps", "--data-dir", DATA], stdout=log, stderr=log)
try:
    for _ in range(120):
        try:
            urllib.request.urlopen(URL + "/_apps/health", timeout=2).read()
            break
        except OSError:
            time.sleep(1)
    else:
        sys.exit("server did not start")

    def f(n):
        return os.path.join(FRAMES, f"{n:02d}.png")

    card(
        f(1),
        "You, to your AI assistant",
        "“Make an app where the team can sign up for Friday lunch.”",
    )
    framed(f(2), "Before", "Your server has no apps yet", "localhost:9080", URL + "/")

    tool(
        "create_app",
        {"name": "Friday lunch", "description": "Sign up for the team lunch on Friday"},
    )
    tool(
        "save_files",
        {
            "app": "friday-lunch",
            "message": "Sign-up form and list of who is coming",
            "files": {
                "main.py": V2,
                "static/style.css": CSS,
                "requirements.txt": "flask\npsycopg[binary]\n",
                "migrations/001_signups.sql": MIG1,
            },
        },
    )
    for name, dish in [
        ("Ana", "Mushroom risotto"),
        ("Tomaz", "Chicken sandwich"),
        ("Priya", "Greek salad"),
        ("Jon", "Ramen"),
    ]:
        signup(name=name, dish=dish)

    framed(f(3), "1", "The assistant builds it and publishes it", "localhost:9080", URL + "/")
    framed(
        f(4),
        "2",
        "Open the address and share it with the team",
        "localhost:9080/friday-lunch/",
        URL + "/friday-lunch/",
    )

    card(
        f(5),
        "You, a week later",
        "“Also ask people about dietary needs.”",
        "The assistant changes the app and its data, and publishes a new version.",
    )
    tool(
        "save_files",
        {
            "app": "friday-lunch",
            "message": "Ask for dietary needs",
            "files": {"main.py": V3, "migrations/002_diet.sql": MIG2},
        },
    )
    signup(name="Mei", dish="Veggie burger", diet="Vegetarian")
    signup(name="Luca", dish="Lentil soup", diet="Vegan")
    framed(
        f(6),
        "3",
        "The change is live; nobody lost their sign-up",
        "localhost:9080/friday-lunch/",
        URL + "/friday-lunch/",
    )
    framed(
        f(7),
        "4",
        "Every change is a version you can go back to",
        "localhost:9080/_apps/app/friday-lunch",
        URL + "/_apps/app/friday-lunch",
    )
finally:
    server.send_signal(2)
    try:
        server.wait(timeout=90)
    except subprocess.TimeoutExpired:
        server.kill()

# Seconds each frame stays up.
hold = {1: 3.2, 2: 2.0, 3: 2.8, 4: 3.6, 5: 3.6, 6: 3.6, 7: 3.8}
concat = os.path.join(FRAMES, "list.txt")
with open(concat, "w") as fh:
    for n, secs in hold.items():
        fh.write(f"file '{n:02d}.png'\nduration {secs}\n")
    fh.write(f"file '{max(hold):02d}.png'\n")
subprocess.run(
    [
        "ffmpeg",
        "-y",
        "-loglevel",
        "error",
        "-f",
        "concat",
        "-safe",
        "0",
        "-i",
        concat,
        "-vf",
        "fps=5,scale=960:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=128:stats_mode=diff[p];[b][p]paletteuse=dither=bayer:bayer_scale=4",
        "-loop",
        "0",
        OUT,
    ],
    check=True,
)
print("wrote", OUT, os.path.getsize(OUT) // 1024, "KB")
