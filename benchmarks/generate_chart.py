import json
import os

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np

script_dir = os.path.dirname(os.path.abspath(__file__))
results_file = os.path.join(script_dir, "results.json")

with open(results_file) as f:
    data = json.load(f)

groups = ["Flask (WSGI)", "FastAPI (ASGI)"]
reverse_proxy_keys = ["flask_gunicorn_caddy", "fastapi_uvicorn_caddy"]
caddysnake_keys = ["flask_caddysnake", "fastapi_caddysnake"]

rp_values = [data[k]["requests_per_sec"] for k in reverse_proxy_keys]
cs_values = [data[k]["requests_per_sec"] for k in caddysnake_keys]

x = np.arange(len(groups))
width = 0.3

fig, ax = plt.subplots(figsize=(10, 6))
bars1 = ax.bar(
    x - width / 2,
    rp_values,
    width,
    label="Reverse Proxy (Gunicorn/Uvicorn + Caddy)",
    color="#4A90D9",
    edgecolor="white",
)
bars2 = ax.bar(
    x + width / 2,
    cs_values,
    width,
    label="Caddy Snake",
    color="#2ECC71",
    edgecolor="white",
)

ax.set_ylabel("Requests per second", fontsize=12)
ax.set_title("Caddy Snake vs Traditional Reverse Proxy", fontsize=14, fontweight="bold")
ax.set_xticks(x)
ax.set_xticklabels(groups, fontsize=12)
ax.legend(fontsize=11)
ax.grid(axis="y", alpha=0.3)
ax.set_axisbelow(True)

for bar in list(bars1) + list(bars2):
    height = bar.get_height()
    ax.annotate(
        f"{height:,.0f}",
        xy=(bar.get_x() + bar.get_width() / 2, height),
        xytext=(0, 5),
        textcoords="offset points",
        ha="center",
        va="bottom",
        fontsize=10,
        fontweight="bold",
    )

plt.tight_layout()
plt.savefig(os.path.join(script_dir, "benchmark_chart.png"), dpi=150)
plt.savefig(os.path.join(script_dir, "benchmark_chart.svg"))
print("Charts saved!")

print("\n## Benchmark Results\n")
print("| Configuration | Requests/sec | Avg Latency (ms) | P99 Latency (ms) |")
print("|---|---|---|---|")
for name, key in [
    ("Flask + Gunicorn + Caddy", "flask_gunicorn_caddy"),
    ("Flask + Caddy Snake", "flask_caddysnake"),
    ("FastAPI + Uvicorn + Caddy", "fastapi_uvicorn_caddy"),
    ("FastAPI + Caddy Snake", "fastapi_caddysnake"),
]:
    d = data[key]
    print(
        f"| {name} | {d['requests_per_sec']:,.0f} "
        f"| {d['avg_latency_ms']:.2f} | {d['p99_latency_ms']:.2f} |"
    )
