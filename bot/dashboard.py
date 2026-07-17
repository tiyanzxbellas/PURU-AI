import platform
import psutil
import time
import os
from datetime import datetime, timezone
from flask import Flask, Response
from waitress import serve
from config import TOKEN_COMPACT_LIMIT, TOKEN_BLOCK_LIMIT, MAX_LOOPS, MODEL_NAME
from .metrics import START_TIME, bot_metrics

BOT_DISPLAY_NAME = "Puru Code AI"
BOT_USERNAME = "PuruAI_bot"
BOT_CREATOR = "Mas Puru"

def _get_html():
    now = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M:%S UTC")
    uptime_sec = int(time.time() - START_TIME)
    days, hours, mins, secs = uptime_sec // 86400, (uptime_sec % 86400) // 3600, (uptime_sec % 3600) // 60, uptime_sec % 60
    uptime_str = f"{days}d {hours}h {mins}m {secs}s"
    total_msg = bot_metrics["total_messages"]
    unique_users = len(bot_metrics["unique_users"])
    mem = psutil.Process().memory_info().rss / (1024 * 1024)
    top_cmds = sorted(bot_metrics["commands_used"].items(), key=lambda x: -x[1])[:5]
    top_cmds_html = "\n".join(f'<div class="cmd-item"><span class="cmd-name">/{c}</span><span class="cmd-count">{n}</span></div>' for c, n in top_cmds) if top_cmds else '<div class="cmd-item"><span class="cmd-name">No data yet</span></div>'
    return f"""<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{BOT_DISPLAY_NAME} - Dashboard</title>
<style>
*{{margin:0;padding:0;box-sizing:border-box}}
body{{font-family:'Segoe UI',system-ui,-apple-system,sans-serif;background:#0f172a;color:#e2e8f0;min-height:100vh}}
.header{{background:linear-gradient(135deg,#1e293b 0%,#0f172a 100%);border-bottom:1px solid #334155;padding:20px 0}}
.header-inner{{max-width:1100px;margin:0 auto;padding:0 24px;display:flex;align-items:center;gap:16px}}
.logo{{width:48px;height:48px;background:linear-gradient(135deg,#6366f1,#8b5cf6);border-radius:12px;display:flex;align-items:center;justify-content:center;font-size:24px;font-weight:700;color:#fff}}
.header-text h1{{font-size:1.5rem;font-weight:700;color:#f8fafc}}
.header-text p{{font-size:.875rem;color:#94a3b8;margin-top:2px}}
.status-badge{{margin-left:auto;padding:6px 16px;border-radius:999px;font-size:.75rem;font-weight:600;letter-spacing:.5px;text-transform:uppercase;display:flex;align-items:center;gap:6px}}
.status-badge .dot{{width:8px;height:8px;border-radius:50%;animation:pulse 2s infinite}}
.online{{background:#064e3b;color:#6ee7b7}}
.online .dot{{background:#34d399}}
@keyframes pulse{{0%,100%{{opacity:1}}50%{{opacity:.4}}}}
.container{{max-width:1100px;margin:0 auto;padding:24px}}
.grid{{display:grid;grid-template-columns:repeat(auto-fit,minmax(240px,1fr));gap:16px;margin-bottom:24px}}
.card{{background:#1e293b;border:1px solid #334155;border-radius:12px;padding:20px;transition:border-color .2s}}
.card:hover{{border-color:#6366f1}}
.card-label{{font-size:.75rem;text-transform:uppercase;letter-spacing:.5px;color:#64748b;margin-bottom:8px}}
.card-value{{font-size:1.75rem;font-weight:700;color:#f8fafc}}
.card-sub{{font-size:.8rem;color:#94a3b8;margin-top:4px}}
.card-sub.green{{color:#34d399}}
.card-sub.blue{{color:#60a5fa}}
.card-sub.purple{{color:#a78bfa}}
.section{{background:#1e293b;border:1px solid #334155;border-radius:12px;padding:24px;margin-bottom:24px}}
.section-title{{font-size:1rem;font-weight:600;color:#f8fafc;margin-bottom:16px;display:flex;align-items:center;gap:8px}}
.section-title .icon{{font-size:1.1rem}}
.info-grid{{display:grid;grid-template-columns:repeat(auto-fit,minmax(280px,1fr));gap:12px}}
.info-row{{display:flex;justify-content:space-between;padding:10px 14px;border-radius:8px;background:#0f172a}}
.info-row .label{{color:#94a3b8;font-size:.875rem}}
.info-row .value{{color:#f8fafc;font-size:.875rem;font-weight:500}}
.cmd-item{{display:flex;justify-content:space-between;padding:10px 14px;border-radius:8px;background:#0f172a;margin-bottom:8px}}
.cmd-item:last-child{{margin-bottom:0}}
.cmd-name{{color:#e2e8f0;font-size:.875rem;font-weight:500}}
.cmd-count{{background:#6366f1;color:#fff;padding:2px 10px;border-radius:999px;font-size:.75rem;font-weight:600}}
.footer{{text-align:center;padding:24px;color:#475569;font-size:.8rem;border-top:1px solid #1e293b}}
</style>
</head>
<body>
<div class="header">
<div class="header-inner">
<div class="logo">P</div>
<div class="header-text">
<h1>{BOT_DISPLAY_NAME}</h1>
<p>Telegram AI Bot &mdash; Powered by Gemini</p>
</div>
<div class="status-badge online"><span class="dot"></span>Running</div>
</div>
</div>
<div class="container">
<div class="grid">
<div class="card">
<div class="card-label">Status</div>
<div class="card-value" style="color:#34d399">Online</div>
<div class="card-sub green">All systems operational</div>
</div>
<div class="card">
<div class="card-label">Uptime</div>
<div class="card-value">{uptime_str}</div>
<div class="card-sub blue">Since bot started</div>
</div>
<div class="card">
<div class="card-label">Total Messages</div>
<div class="card-value">{total_msg:,}</div>
<div class="card-sub purple">All conversations</div>
</div>
<div class="card">
<div class="card-label">Unique Users</div>
<div class="card-value">{unique_users:,}</div>
<div class="card-sub">Total users served</div>
</div>
</div>
<div class="section">
<div class="section-title"><span class="icon">&#9881;</span> Bot Information</div>
<div class="info-grid">
<div class="info-row"><span class="label">Bot Name</span><span class="value">{BOT_DISPLAY_NAME}</span></div>
<div class="info-row"><span class="label">Username</span><span class="value">@{BOT_USERNAME}</span></div>
<div class="info-row"><span class="label">Creator</span><span class="value">{BOT_CREATOR}</span></div>
<div class="info-row"><span class="label">AI Model</span><span class="value">{MODEL_NAME}</span></div>
<div class="info-row"><span class="label">Platform</span><span class="value">{platform.system()} {platform.release()}</span></div>
<div class="info-row"><span class="label">Python</span><span class="value">{platform.python_version()}</span></div>
</div>
</div>
<div class="section">
<div class="section-title"><span class="icon">&#9776;</span> Server Info</div>
<div class="info-grid">
<div class="info-row"><span class="label">Web Port</span><span class="value">3000</span></div>
<div class="info-row"><span class="label">Memory Usage</span><span class="value">{mem:.1f} MB</span></div>
<div class="info-row"><span class="label">CPU Cores</span><span class="value">{psutil.cpu_count()}</span></div>
<div class="info-row"><span class="label">Current Time</span><span class="value">{now}</span></div>
<div class="info-row"><span class="label">Max Loops</span><span class="value">{MAX_LOOPS}</span></div>
<div class="info-row"><span class="label">Token Block Limit</span><span class="value">{TOKEN_BLOCK_LIMIT:,}</span></div>
</div>
</div>
<div class="section">
<div class="section-title"><span class="icon">&#128295;</span> Top Commands</div>
{top_cmds_html}
</div>
<div class="section">
<div class="section-title"><span class="icon">&#128187;</span> Available Commands</div>
<div class="info-grid">
<div class="info-row"><span class="label">/start</span><span class="value">Welcome message</span></div>
<div class="info-row"><span class="label">/menu</span><span class="value">Show all commands</span></div>
<div class="info-row"><span class="label">/ai</span><span class="value">Ask AI (use in groups)</span></div>
<div class="info-row"><span class="label">/agents</span><span class="value">Lihat daftar agen</span></div>
<div class="info-row"><span class="label">/context</span><span class="value">Token usage info</span></div>
<div class="info-row"><span class="label">/compact</span><span class="value">Summarize &amp; compress context</span></div>
<div class="info-row"><span class="label">/clear</span><span class="value">Clear conversation history</span></div>
<div class="info-row"><span class="label">/clear_all</span><span class="value">Wipe everything: history, files, versions</span></div>
</div>
</div>
</div>
<div class="footer">{BOT_DISPLAY_NAME} &copy; 2025 {BOT_CREATOR}. Built with Python &amp; python-telegram-bot.</div>
</body></html>"""

app = Flask(__name__)

@app.route("/")
def index():
    return Response(_get_html(), mimetype="text/html")

@app.route("/health")
def health():
    uptime_sec = int(time.time() - START_TIME)
    return {
        "status": "ok",
        "bot": BOT_DISPLAY_NAME,
        "model": MODEL_NAME,
        "uptime_seconds": uptime_sec,
        "total_messages": bot_metrics["total_messages"],
        "unique_users": len(bot_metrics["unique_users"]),
    }

def run_web_server():
    if os.environ.get("DEV_MODE") == "true":
        app.run(host="0.0.0.0", port=3000, debug=True, use_reloader=False)
    else:
        serve(app, host="0.0.0.0", port=3000)
