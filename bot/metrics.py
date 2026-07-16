import time

START_TIME = time.time()
bot_metrics = {
    "total_messages": 0,
    "unique_users": set(),
    "commands_used": {},
}

def track_message(user_id, username, command=None):
    bot_metrics["total_messages"] += 1
    bot_metrics["unique_users"].add(user_id)
    if command:
        bot_metrics["commands_used"][command] = bot_metrics["commands_used"].get(command, 0) + 1

def get_uptime_str():
    uptime_sec = int(time.time() - START_TIME)
    months = uptime_sec // (30 * 86400)
    weeks = (uptime_sec % (30 * 86400)) // (7 * 86400)
    days = (uptime_sec % (7 * 86400)) // 86400
    hours = (uptime_sec % 86400) // 3600
    minutes = (uptime_sec % 3600) // 60

    parts = []
    if months > 0:
        parts.append(f"{months}mo")
    if weeks > 0:
        parts.append(f"{weeks}w")
    if days > 0:
        parts.append(f"{days}d")
    if hours > 0 or (months == 0 and weeks == 0 and days == 0):
        parts.append(f"{hours}h")
    if months == 0 and weeks == 0 and days == 0 and hours == 0 and minutes > 0:
        parts.append(f"{minutes}m")
    if not parts:
        parts.append("0m")
    return " ".join(parts)

def get_bio_texts():
    unique_users = len(bot_metrics["unique_users"])
    uptime_str = get_uptime_str()
    total_msg = bot_metrics["total_messages"]

    short = f"\U0001f465 {unique_users:,} users  |  \u23f1 {uptime_str}"
    if len(short) > 120:
        short = short[:117] + "..."

    full = (
        f"\U0001f916 Puru AI Bot\n\n"
        f"\U0001f465 Users: {unique_users:,}\n"
        f"\u23f1 Active: {uptime_str}\n"
        f"\U0001f4ac Messages: {total_msg:,}\n\n"
        f"AI-powered Telegram bot with sandboxed execution environment."
    )
    return short, full
