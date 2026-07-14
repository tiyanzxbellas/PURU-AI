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
