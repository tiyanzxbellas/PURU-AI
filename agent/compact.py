from .history import conversations, get_token_count
from .ai import _call_ai_api
from firebase import save_history

def compact_history(user_id: int) -> str:
    if user_id not in conversations or len(conversations[user_id]) <= 1:
        return "No conversation to compact."
    history = conversations[user_id]
    user_msgs = [m for m in history[1:] if m["role"] == "user"]
    full_text = "\n".join(f"User: {m['content']}" for m in user_msgs)
    summary_prompt = (
        "Ringkas percakapan berikut menjadi paragraf singkat yang mencakup "
        "poin-poin penting, topik utama, dan tujuan user. "
        "Jangan lewatkan detail teknis atau kode yang dibahas. "
        "Gunakan format poin-poin:\n\n"
        f"{full_text}"
    )
    summary = _call_ai_api(summary_prompt)
    if summary:
        conversations[user_id] = [
            history[0],
            {"role": "user", "content": f"[Conversation Summary]\n{summary}"},
            {"role": "assistant", "content": "Context compacted. Continuing from summary."},
        ]
        save_history(user_id, conversations[user_id])
        new_tokens = get_token_count(user_id)
        return f"Context compacted!\nBefore: {len(history)-1} messages\nAfter: 2 messages\nTokens: ~{new_tokens}"
    return "Compact failed: Gemini API unavailable after retries."
