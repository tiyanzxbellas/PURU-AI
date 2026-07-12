import json
import re
import time
import httpx
import logging
from datetime import datetime
from config import SYSTEM_PROMPT, TOKEN_WARN_LIMIT, MAX_LOOPS
from sandbox import execute_tool, close_sandbox

logger = logging.getLogger(__name__)

conversations: dict[int, list[dict]] = {}
MAX_HISTORY = 30

COMPACT_API_URL = "https://puruboy-api.vercel.app/api/ai/gemini-v2"
AI_MAX_RETRIES = 5
INVALID_TOOL_MAX_RETRIES = 5


def _estimate_tokens(text: str) -> int:
    return len(text) // 4


def get_token_count(user_id: int) -> int:
    if user_id not in conversations:
        return 0
    return sum(_estimate_tokens(m.get("content", "") or "") for m in conversations[user_id])


def get_context_info(user_id: int) -> dict:
    if user_id not in conversations:
        return {"tokens": 0, "messages": 0, "warn": False}
    history = conversations[user_id]
    tokens = get_token_count(user_id)
    return {
        "tokens": tokens,
        "messages": len([m for m in history if m["role"] == "user"]),
        "warn": tokens >= TOKEN_WARN_LIMIT,
    }


def _call_ai_api(prompt: str) -> str | None:
    """Call Gemini AI API with exponential backoff. Returns answer or None on failure."""
    for attempt in range(AI_MAX_RETRIES):
        try:
            resp = httpx.post(
                COMPACT_API_URL,
                json={"prompt": prompt},
                timeout=60,
            )
            resp.raise_for_status()
            data = resp.json()
            if data.get("success") and data.get("result", {}).get("answer"):
                return data["result"]["answer"]
            
            logger.warning(f"AI API returned logical failure (attempt {attempt+1}): {data}")
        except Exception as e:
            logger.warning(f"AI API connection error (attempt {attempt+1}): {e}")
        
        if attempt < AI_MAX_RETRIES - 1:
            delay = 2 ** attempt
            logger.info(f"Retrying AI API in {delay}s...")
            time.sleep(delay)
    return None


def _format_history(history: list[dict]) -> str:
    formatted = ""
    for msg in history:
        role = msg["role"]
        content = msg.get("content", "")
        if role == "system":
            formatted += f"SYSTEM: {content}\n\n"
        elif role == "user":
            formatted += f"USER: {content}\n\n"
        elif role == "assistant":
            formatted += f"ASSISTANT: {content}\n\n"
        elif role == "tool":
            formatted += f"TOOL_RESULT: {content}\n\n"
    return formatted.strip()


def parse_response(text: str):
    """Parse XML response to extract message and tool call."""
    message = ""
    tool_data = None
    
    # Try to find content within <message> tags
    msg_match = re.search(r"<message>(.*?)</message>", text, re.DOTALL)
    if msg_match:
        message = msg_match.group(1).strip()
    else:
        # Fallback: if no <message> tags but <response> exists, take everything inside <response> 
        # that is not <tools_call>
        resp_match = re.search(r"<response>(.*?)</response>", text, re.DOTALL)
        if resp_match:
            inner = resp_match.group(1)
            inner = re.sub(r"<tools_call>.*?</tools_call>", "", inner, flags=re.DOTALL)
            message = inner.strip()
        else:
            # If no tags at all, just take the raw text
            message = text.strip()
            
    # Parse tool call
    tool_match = re.search(r"<tool name=\"(.*?)\">(.*?)</tool>", text, re.DOTALL)
    if tool_match:
        tool_name = tool_match.group(1)
        params_text = tool_match.group(2)
        params = {}
        param_matches = re.finditer(r"<parameter name=\"(.*?)\">(.*?)</parameter>", params_text, re.DOTALL)
        for pm in param_matches:
            params[pm.group(1)] = pm.group(2).strip()
        tool_data = {"name": tool_name, "arguments": params}
        
    return message, tool_data


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
        new_tokens = get_token_count(user_id)
        return f"Context compacted!\nBefore: {len(history)-1} messages\nAfter: 2 messages\nTokens: ~{new_tokens}"
    return "Compact failed: Gemini API unavailable after retries."


def chat_with_tools(user_id: int, message: str, on_loop=None):
    if user_id not in conversations:
        now = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
        system_with_date = f"{SYSTEM_PROMPT}\n\nCurrent date and time: {now} (UTC+0 / use local timezone if specified)."
        conversations[user_id] = [{"role": "system", "content": system_with_date}]

    history = conversations[user_id]
    history.append({"role": "user", "content": message})

    if len(history) > MAX_HISTORY + 1:
        history[:] = [history[0]] + history[-(MAX_HISTORY):]

    loop_count = 0
    invalid_tool_count = 0

    while loop_count < MAX_LOOPS:
        full_prompt = _format_history(history)
        raw_response = _call_ai_api(full_prompt)
        
        if not raw_response:
            yield "Error: Gemini API unavailable.", True, loop_count, []
            return

        reply, tool_call = parse_response(raw_response)

        if not tool_call:
            history.append({"role": "assistant", "content": reply})
            yield reply, True, loop_count, []
            return

        # Process tool call
        history.append({"role": "assistant", "content": raw_response})
        
        func_name = tool_call["name"]
        func_args = tool_call["arguments"]

        result_text, file_data = execute_tool(user_id, func_name, func_args)
        
        # Check if tool call is invalid (starts with FAIL or Error)
        is_invalid = result_text.startswith("FAIL") or result_text.startswith("Error")
        
        if is_invalid:
            invalid_tool_count += 1
            if invalid_tool_count > INVALID_TOOL_MAX_RETRIES:
                error_msg = f"Error: Too many invalid tool calls ({INVALID_TOOL_MAX_RETRIES}). Stopping."
                history.append({"role": "assistant", "content": error_msg})
                yield error_msg, True, loop_count, []
                return
            
            # Exponential backoff for invalid tool call
            delay = 2 ** (invalid_tool_count - 1)
            logger.warning(f"Invalid tool call '{func_name}' (attempt {invalid_tool_count}). Backing off {delay}s...")
            time.sleep(delay)
            
            # Tegur AI untuk self-improvement
            feedback = (
                f"[SYSTEM ERROR] Your tool call to '{func_name}' was invalid:\n{result_text}\n\n"
                "Please analyze why it failed (check parameters, file paths, or command syntax), "
                "correct your mistake, and try again. Focus on self-improvement."
            )
            history.append({"role": "tool", "content": feedback})
        else:
            # Success, reset invalid counter
            invalid_tool_count = 0
            history.append({"role": "tool", "content": result_text})
        
        loop_count += 1
        if on_loop:
            on_loop(loop_count)

        pending_files = [file_data] if file_data else []
        yield f"Processing... ({loop_count}/{MAX_LOOPS})", False, loop_count, pending_files

    reply = "Error: Max tool call loops reached."
    history.append({"role": "assistant", "content": reply})
    yield reply, True, loop_count, []


def chat_stream(user_id: int, message: str):
    """Simulated streaming for the compact API."""
    if user_id not in conversations:
        conversations[user_id] = [{"role": "system", "content": SYSTEM_PROMPT}]

    history = conversations[user_id]
    history.append({"role": "user", "content": message})

    if len(history) > MAX_HISTORY + 1:
        history[:] = [history[0]] + history[-(MAX_HISTORY):]

    full_prompt = _format_history(history)
    raw_response = _call_ai_api(full_prompt)
    
    if not raw_response:
        yield "Error: Gemini API unavailable.", True
        return

    reply, _ = parse_response(raw_response)
    history.append({"role": "assistant", "content": reply})
    
    # Simulate streaming by yielding chunks
    words = reply.split(" ")
    for i in range(len(words)):
        chunk = words[i] + (" " if i < len(words) - 1 else "")
        yield chunk, False
        time.sleep(0.01)
    
    yield reply, True


def chat(user_id: int, message: str) -> str:
    if user_id not in conversations:
        conversations[user_id] = [{"role": "system", "content": SYSTEM_PROMPT}]

    history = conversations[user_id]
    history.append({"role": "user", "content": message})

    if len(history) > MAX_HISTORY + 1:
        history[:] = [history[0]] + history[-(MAX_HISTORY):]

    full_prompt = _format_history(history)
    raw_response = _call_ai_api(full_prompt)
    
    if not raw_response:
        return "Error: Gemini API unavailable."

    reply, _ = parse_response(raw_response)
    history.append({"role": "assistant", "content": reply})
    return reply


def clear_history(user_id: int) -> None:
    conversations.pop(user_id, None)
    close_sandbox(user_id)
