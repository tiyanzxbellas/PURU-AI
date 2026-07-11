import logging
import asyncio
import threading
import time
import platform
import psutil
from datetime import datetime, timezone
from io import BytesIO
from flask import Flask, Response
from telegram import Update, BotCommand, InputFile
from telegram.error import Conflict, TelegramError
from telegram.ext import (
    ApplicationBuilder,
    CommandHandler,
    MessageHandler,
    filters,
    ContextTypes,
)
from config import TELEGRAM_BOT_TOKEN, TOKEN_WARN_LIMIT, TOKEN_COMPACT_LIMIT, MAX_LOOPS, MODEL_NAME
from agent import chat_with_tools, chat_stream, clear_history, get_context_info, compact_history

BOT_COMMANDS = [
    BotCommand("start", "Show welcome message"),
    BotCommand("menu", "Show all commands"),
    BotCommand("ai", "Ask Puru AI (use in groups)"),
    BotCommand("tools", "Show available tools"),
    BotCommand("context", "Show token usage info"),
    BotCommand("compact", "Summarize & compress context"),
    BotCommand("clear", "Clear conversation history"),
]

logging.basicConfig(
    format="%(asctime)s - %(name)s - %(levelname)s - %(message)s",
    level=logging.INFO,
)
logger = logging.getLogger(__name__)

# ─── Bot Metrics ───────────────────────────────────────────────────────────────
START_TIME = time.time()
bot_metrics = {
    "total_messages": 0,
    "unique_users": set(),
    "commands_used": {},
}

BOT_USERNAME = "PuruAI_bot"
BOT_DISPLAY_NAME = "Puru Code AI"
BOT_CREATOR = "Mas Puru"


def _track_message(user_id, username, command=None):
    bot_metrics["total_messages"] += 1
    bot_metrics["unique_users"].add(user_id)
    if command:
        bot_metrics["commands_used"][command] = bot_metrics["commands_used"].get(command, 0) + 1


# ─── HTML Dashboard ────────────────────────────────────────────────────────────
def _get_html():
    now = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M:%S UTC")
    uptime_sec = int(time.time() - START_TIME)
    days = uptime_sec // 86400
    hours = (uptime_sec % 86400) // 3600
    mins = (uptime_sec % 3600) // 60
    secs = uptime_sec % 60
    uptime_str = f"{days}d {hours}h {mins}m {secs}s"
    total_msg = bot_metrics["total_messages"]
    unique_users = len(bot_metrics["unique_users"])
    mem = psutil.Process().memory_info().rss / (1024 * 1024)
    top_cmds = sorted(bot_metrics["commands_used"].items(), key=lambda x: -x[1])[:5]
    top_cmds_html = "\n".join(
        f'<div class="cmd-item"><span class="cmd-name">/{c}</span><span class="cmd-count">{n}</span></div>'
        for c, n in top_cmds
    ) if top_cmds else '<div class="cmd-item"><span class="cmd-name">No data yet</span></div>'

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
.btn{{display:inline-block;padding:8px 16px;border-radius:8px;font-size:.8rem;font-weight:500;text-decoration:none;transition:all .2s}}
.btn-primary{{background:#6366f1;color:#fff}}
.btn-primary:hover{{background:#818cf8}}
</style>
</head>
<body>
<div class="header">
<div class="header-inner">
<div class="logo">P</div>
<div class="header-text">
<h1>{BOT_DISPLAY_NAME}</h1>
<p>Telegram AI Bot &mdash; Powered by OpenAI</p>
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
<div class="info-row"><span class="label">Token Warn Limit</span><span class="value">{TOKEN_WARN_LIMIT:,}</span></div>
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
<div class="info-row"><span class="label">/tools</span><span class="value">Show available tools</span></div>
<div class="info-row"><span class="label">/context</span><span class="value">Token usage info</span></div>
<div class="info-row"><span class="label">/compact</span><span class="value">Summarize &amp; compress context</span></div>
<div class="info-row"><span class="label">/clear</span><span class="value">Clear conversation history</span></div>
</div>
</div>
</div>
<div class="footer">{BOT_DISPLAY_NAME} &copy; 2025 {BOT_CREATOR}. Built with Python &amp; python-telegram-bot.</div>
</body></html>"""


# ─── Flask Web Server ──────────────────────────────────────────────────────────
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
    app.run(host="0.0.0.0", port=3000, debug=False, use_reloader=False)


# ─── Telegram Bot Handlers ────────────────────────────────────────────────────
async def post_init(application) -> None:
    global BOT_USERNAME
    await application.bot.set_my_commands(BOT_COMMANDS)
    me = await application.bot.get_me()
    BOT_USERNAME = me.username or BOT_USERNAME
    logger.info("Bot started as @%s — Dashboard on port 3000", BOT_USERNAME)


async def start(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    _track_message(update.effective_user.id, update.effective_user.username, "start")
    await update.message.reply_text(
        "Puru Code - AI Coding Agent\n\n"
        "Send me any coding question or paste code to debug.",
        quote=True,
    )


async def menu(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    _track_message(update.effective_user.id, update.effective_user.username, "menu")
    await update.message.reply_text(
        "*Puru Code - Menu*\n\n"
        "/start - Welcome message\n"
        "/menu - Show this menu\n"
        "/ai - Ask AI (use in groups)\n"
        "/tools - Show available tools\n"
        "/context - Token usage info\n"
        "/compact - Summarize context\n"
        "/clear - Clear history\n\n"
        "_Send any message to chat with AI._",
        parse_mode="Markdown",
        quote=True,
    )


async def tools_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    _track_message(update.effective_user.id, update.effective_user.username, "tools")
    await update.message.reply_text(
        "*Available Tools*\n\n"
        "The AI agent can use these tools in the sandbox:\n\n"
        "• *bash* — Execute bash commands\n"
        "• *write_file* — Write/create files\n"
        "• *read_file* — Read file contents\n"
        "• *edit_file* — Edit files (find & replace)\n"
        "• *send_file* — Send files to chat\n\n"
        "_Just describe what you want to build or debug, and the AI will use these tools automatically._",
        parse_mode="Markdown",
        quote=True,
    )


async def context_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    _track_message(update.effective_user.id, update.effective_user.username, "context")
    info = get_context_info(update.effective_chat.id)
    used = info["tokens"]
    pct = (used / TOKEN_WARN_LIMIT) * 100
    bar_len = 20
    filled = int(bar_len * used / TOKEN_WARN_LIMIT)
    bar = "\u2588" * filled + "\u2591" * (bar_len - filled)

    status = "Normal"
    if used >= TOKEN_COMPACT_LIMIT:
        status = "\u26a0\ufe0f LIMIT REACHED"
    elif used >= TOKEN_WARN_LIMIT:
        status = "\u26a0\ufe0f Warning"
    elif pct >= 80:
        status = "\u23f3 Getting high"

    await update.message.reply_text(
        f"*Context Status*\n\n"
        f"Model: `{MODEL_NAME}`\n"
        f"Tokens used: `{used:,}` / `{TOKEN_WARN_LIMIT:,}`\n"
        f"[{bar}] {pct:.1f}%\n"
        f"Status: {status}\n"
        f"Messages: `{info['messages']}`\n\n"
        f"/compact to summarize and free tokens",
        parse_mode="Markdown",
        quote=True,
    )


async def compact_cmd(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    _track_message(update.effective_user.id, update.effective_user.username, "compact")
    await update.message.reply_text("Summarizing conversation history...", quote=True)
    result = await asyncio.to_thread(compact_history, update.effective_chat.id)
    await update.message.reply_text(result, quote=True)


async def clear(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    _track_message(update.effective_user.id, update.effective_user.username, "clear")
    clear_history(update.effective_chat.id)
    await update.message.reply_text("History cleared.", quote=True)


async def _run_with_tools(update: Update, message_text: str) -> None:
    """Run chat_with_tools with real-time progress updates."""
    chat_id = update.effective_chat.id

    # Send initial message
    status_msg = await update.message.reply_text("🤖 Thinking...", quote=True)

    try:
        generator = await asyncio.wait_for(
            asyncio.to_thread(chat_with_tools, chat_id, message_text),
            timeout=600,
        )
        final_response = ""
        for text, is_done, loop_count, pending_files in generator:
            if not is_done:
                # Tool loop progress — edit the status message
                try:
                    await status_msg.edit_text(f"🔧 Processing... ({loop_count}/{MAX_LOOPS})")
                except Exception:
                    pass  # message not changed, ignore
            else:
                final_response = text

            # Send any files returned by send_file tool
            for filename, file_bytes, caption in pending_files:
                try:
                    await update.message.reply_document(
                        document=BytesIO(file_bytes),
                        filename=filename,
                        caption=caption or filename,
                    )
                except Exception as e:
                    logger.warning("Failed to send file %s: %s", filename, e)

        if final_response:
            try:
                await status_msg.edit_text(final_response, parse_mode="Markdown")
            except Exception:
                try:
                    await status_msg.edit_text(final_response)
                except Exception:
                    pass
        else:
            try:
                await status_msg.edit_text("No response from AI.")
            except Exception:
                pass
    except asyncio.TimeoutError:
        try:
            await status_msg.edit_text("Timed out. Try /clear and send a shorter message.")
        except Exception:
            pass
    except Exception as e:
        logger.exception("Error in _run_with_tools")
        try:
            await status_msg.edit_text(f"Error: {e}")
        except Exception:
            pass


async def ai_ask(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    """Handle /ai command — pass remaining text as prompt."""
    if not update.message or not update.message.text:
        return
    user = update.effective_user
    _track_message(user.id, user.username, "ai")
    parts = update.message.text.split(maxsplit=1)
    prompt = parts[1] if len(parts) > 1 else ""
    if not prompt:
        await update.message.reply_text("Usage: /ai <your question>", quote=True)
        return

    info = get_context_info(update.effective_chat.id)
    if info["tokens"] >= TOKEN_WARN_LIMIT:
        await update.message.reply_text(
            f"⚠️ *Token usage high:* `{info['tokens']:,}` tokens\n"
            f"Run /compact to summarize and free up context.",
            parse_mode="Markdown",
            quote=True,
        )

    await _run_with_tools(update, prompt)


async def handle_message(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    if not update.message or not update.message.text:
        return
    user = update.effective_user
    _track_message(user.id, user.username)
    chat_id = update.effective_chat.id
    text = update.message.text.strip()

    info = get_context_info(chat_id)
    if info["tokens"] >= TOKEN_WARN_LIMIT:
        logger.info("Token warning for chat %s (%s tokens)", chat_id, info["tokens"])
        await update.message.reply_text(
            f"⚠️ *Token usage high:* `{info['tokens']:,}` tokens\n"
            f"Run /compact to summarize and free up context.",
            parse_mode="Markdown",
            quote=True,
        )

    if update.effective_chat.type in ("group", "supergroup"):
        bot_username = context.bot.username
        if f"@{bot_username}" not in text and not text.startswith("/"):
            return
        text = text.replace(f"@{bot_username}", "").strip()

    if not text:
        return

    await _run_with_tools(update, text)


# ─── Main ──────────────────────────────────────────────────────────────────────
RECONNECT_DELAY = 10  # seconds


def run_bot() -> None:
    app_tg = (
        ApplicationBuilder()
        .token(TELEGRAM_BOT_TOKEN)
        .post_init(post_init)
        .build()
    )

    app_tg.add_handler(CommandHandler("start", start))
    app_tg.add_handler(CommandHandler("menu", menu))
    app_tg.add_handler(CommandHandler("ai", ai_ask))
    app_tg.add_handler(CommandHandler("tools", tools_cmd))
    app_tg.add_handler(CommandHandler("context", context_cmd))
    app_tg.add_handler(CommandHandler("compact", compact_cmd))
    app_tg.add_handler(CommandHandler("clear", clear))
    app_tg.add_handler(MessageHandler(filters.TEXT & ~filters.COMMAND, handle_message))

    logger.info("Starting Telegram bot polling...")
    app_tg.run_polling(drop_pending_updates=True)


if __name__ == "__main__":
    web_thread = threading.Thread(target=run_web_server, daemon=True)
    web_thread.start()
    logger.info("Dashboard running on http://0.0.0.0:3000")

    while True:
        try:
            run_bot()
        except (Conflict, TelegramError) as e:
            logger.error("Bot disconnected: %s — reconnecting in %ds...", e, RECONNECT_DELAY)
            time.sleep(RECONNECT_DELAY)
