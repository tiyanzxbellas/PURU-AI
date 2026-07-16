import logging
from datetime import datetime
from config import SYSTEM_PROMPT, MEMORY_PATH
from firebase import save_history, get_history, get_fb_file

logger = logging.getLogger(__name__)

conversations: dict[int, list[dict]] = {}

def _estimate_tokens(text: str) -> int:
    return len(text) // 4

def get_token_count(user_id: int) -> int:
    if user_id not in conversations:
        return 0
    return sum(_estimate_tokens(m.get("content", "") or "") for m in conversations[user_id] if m["role"] != "system")

def get_context_info(user_id: int) -> dict:
    if user_id not in conversations:
        return {"tokens": 0, "messages": 0}
    tokens = get_token_count(user_id)
    return {
        "tokens": tokens,
        "messages": len([m for m in conversations[user_id] if m["role"] != "system"]),
    }

def _ensure_history(user_id: int):
    if user_id not in conversations:
        db_history = get_history(user_id)
        if db_history:
            conversations[user_id] = db_history
            logger.info(f"Loaded history for {user_id} from Firebase.")
        else:
            now = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
            system_with_date = f"{SYSTEM_PROMPT}\n\nCurrent date and time: {now} (UTC+0 / use local timezone if specified)."
            conversations[user_id] = [{"role": "system", "content": system_with_date}]
            save_history(user_id, conversations[user_id])
            logger.info(f"Initialized new history for {user_id}.")
    _inject_memory(user_id)


def _inject_memory(user_id: int):
    memory = get_fb_file(user_id, MEMORY_PATH)
    now = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    base = f"{SYSTEM_PROMPT}\n\nCurrent date and time: {now} (UTC+0 / use local timezone if specified)."
    if memory:
        conversations[user_id][0]["content"] = f"{base}\n\n---\n\n[MEMORY]\n{memory}"
    else:
        conversations[user_id][0]["content"] = base
