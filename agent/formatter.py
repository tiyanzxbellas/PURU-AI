def _format_history(history: list[dict]) -> str:
    current_goal = ""
    for msg in reversed(history):
        if msg["role"] == "user":
            current_goal = msg.get("content", "")
            break
    formatted = ""
    for msg in history:
        role = msg["role"]
        content = msg.get("content", "")
        if role == "system":
            formatted += f"SYSTEM: {content}\n\n"
            if current_goal:
                formatted += f"CURRENT GOAL: {current_goal}\n\n"
        elif role == "user":
            formatted += f"USER: {content}\n\n"
        elif role == "assistant":
            formatted += f"ASSISTANT: {content}\n\n"
        elif role == "tool":
            formatted += f"TOOL_RESULT: {content}\n\n"
    return formatted.strip()
