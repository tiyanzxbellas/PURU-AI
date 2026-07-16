from .history import conversations, get_token_count
from .ai import _call_ai_api
from firebase import save_history

def compact_history(user_id: int, latest_message: str = "") -> str:
    if user_id not in conversations or len(conversations[user_id]) <= 1:
        return "No conversation to compact."
    history = conversations[user_id]
    dialog = []
    for m in history[1:]:
        role = "User" if m["role"] == "user" else "Assistant"
        dialog.append(f"{role}: {m['content']}")
    full_text = "\n\n".join(dialog)
    if latest_message:
        full_text += f"\n\n[LATEST USER MESSAGE - jangan hilangkan konteks ini]\nUser: {latest_message}"
    summary_prompt = (
        "Ringkas percakapan berikut menjadi poin-poin penting, "
        "topik utama, dan tujuan user. "
        "Jangan lewatkan detail teknis atau kode yang dibahas. "
        "Pastikan konteks pesan terbaru user tetap terjaga. "
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
