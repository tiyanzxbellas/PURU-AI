import json
import re
import time
import httpx
import logging
from datetime import datetime
from config import SYSTEM_PROMPT, TOKEN_WARN_LIMIT, TOKEN_BLOCK_LIMIT, MAX_LOOPS
from sandbox import execute_tool, close_sandbox
from firebase import save_history, get_history, clear_fb_history, clear_all_fb_data

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
    # Find the latest user message to use as the current goal
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


def parse_response(text: str):
    """Parse XML response to extract message and tool call."""
    # Parse tool call first
    tool_match = re.search(r"<tool>(.*?)</tool>", text, re.DOTALL)
    tool_data = None
    if tool_match:
        tool_content = tool_match.group(1)
        # Extract tool name
        name_match = re.search(r"<name>(.*?)</name>", tool_content, re.DOTALL)
        if name_match:
            tool_name = name_match.group(1).strip()
            params = {}
            # Extract parameters
            param_matches = re.finditer(r"<parameter>(.*?)</parameter>", tool_content, re.DOTALL)
            for pm in param_matches:
                p_content = pm.group(1)
                p_name_match = re.search(r"<name>(.*?)</name>", p_content, re.DOTALL)
                p_val_match = re.search(r"<value>(.*?)</value>", p_content, re.DOTALL)
                if p_name_match and p_val_match:
                    params[p_name_match.group(1).strip()] = p_val_match.group(1).strip()
            tool_data = {"name": tool_name, "arguments": params}

    # Extract message
    message = ""
    msg_match = re.search(r"<message>(.*?)</message>", text, re.DOTALL)
    if msg_match:
        message = msg_match.group(1).strip()
    else:
        resp_match = re.search(r"<response>(.*?)</response>", text, re.DOTALL)
        if resp_match:
            inner = resp_match.group(1)
            inner = re.sub(r"<tools_call>.*?</tools_call>", "", inner, flags=re.DOTALL)
            message = inner.strip()
        else:
            # Fallback: raw text minus tool call
            message = text.strip()
            if tool_match:
                message = re.sub(r"<tool>.*?</tool>", "", message, flags=re.DOTALL).strip()

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
        save_history(user_id, conversations[user_id])
        new_tokens = get_token_count(user_id)
        return f"Context compacted!\nBefore: {len(history)-1} messages\nAfter: 2 messages\nTokens: ~{new_tokens}"
    return "Compact failed: Gemini API unavailable after retries."


def _ensure_history(user_id: int):
    """Ensure history is loaded from Firebase or initialized."""
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


def chat_with_tools(user_id: int, message: str, on_loop=None):
    _ensure_history(user_id)

    # Auto-compact if context is too high before adding new message
    if get_token_count(user_id) >= TOKEN_BLOCK_LIMIT:
        logger.info(f"Auto-compacting history for {user_id} (tokens: {get_token_count(user_id)})")
        yield "🧹 Cleaning up memory (compacting)...", False, 0, []
        compact_history(user_id)

    global_history = conversations[user_id]
    user_msg = {"role": "user", "content": message}
    global_history.append(user_msg)

    # Maintain MAX_HISTORY (including system prompt)
    if len(global_history) > MAX_HISTORY + 1:
        global_history[:] = [global_history[0]] + global_history[-(MAX_HISTORY):]
    
    save_history(user_id, global_history)

    # Use global history for the tool-calling loop to preserve tool interaction history.
    loop_history = global_history

    loop_count = 0
    invalid_tool_count = 0

    while loop_count < MAX_LOOPS:
        full_prompt = _format_history(loop_history)
        raw_response = _call_ai_api(full_prompt)
        
        if not raw_response:
            err_msg = "Error: Gemini API unavailable."
            global_history.append({"role": "assistant", "content": err_msg})
            save_history(user_id, global_history)
            yield err_msg, True, loop_count, []
            return

        reply, tool_call = parse_response(raw_response)

        if not tool_call:
            # Final response: append ONLY this to the permanent global history.
            global_history.append({"role": "assistant", "content": reply})
            save_history(user_id, global_history)
            yield reply, True, loop_count, []
            return

        # Intermediate step: append to history and persist.
        loop_history.append({"role": "assistant", "content": raw_response})
        save_history(user_id, global_history)
        
        func_name = tool_call["name"]
        func_args = tool_call["arguments"]

        result_text, file_data = execute_tool(user_id, func_name, func_args)
        
        is_invalid = any(result_text.startswith(prefix) for prefix in ["FAIL", "Error", "Tool error"])
        
        if is_invalid:
            invalid_tool_count += 1
            if invalid_tool_count > INVALID_TOOL_MAX_RETRIES:
                error_msg = f"Error: Too many invalid tool calls ({INVALID_TOOL_MAX_RETRIES}). Stopping."
                global_history.append({"role": "assistant", "content": error_msg})
                save_history(user_id, global_history)
                yield error_msg, True, loop_count, []
                return
            
            delay = 2 ** (invalid_tool_count - 1)
            logger.warning(f"Invalid tool call '{func_name}' (attempt {invalid_tool_count}). Backing off {delay}s...")
            time.sleep(delay)
            
            feedback = (
                f"[SYSTEM ERROR] Your tool call to '{func_name}' was invalid:\n{result_text}\n\n"
                "Please analyze why it failed (check parameters, file paths, or command syntax), "
                "correct your mistake, and try again. Focus on self-improvement."
            )
            loop_history.append({"role": "tool", "content": feedback})
            
            # Prune old tool interactions (max 12 pairs)
            tool_indices = [i for i, m in enumerate(global_history) if m["role"] == "tool"]
            if len(tool_indices) > 12:
                idx = tool_indices[0]
                if idx > 0 and global_history[idx-1]["role"] == "assistant":
                    del global_history[idx]
                    del global_history[idx-1]
                else:
                    del global_history[idx]
            
            save_history(user_id, global_history)
        else:
            invalid_tool_count = 0
            loop_history.append({"role": "tool", "content": result_text})
            
            # Prune old tool interactions (max 12 pairs)
            tool_indices = [i for i, m in enumerate(global_history) if m["role"] == "tool"]
            if len(tool_indices) > 12:
                idx = tool_indices[0]
                if idx > 0 and global_history[idx-1]["role"] == "assistant":
                    del global_history[idx]
                    del global_history[idx-1]
                else:
                    del global_history[idx]
            
            save_history(user_id, global_history)
        
        loop_count += 1
        
        # Maintain MAX_HISTORY inside the loop to prevent bloat
        if len(global_history) > MAX_HISTORY + 1:
            global_history[:] = [global_history[0]] + global_history[-(MAX_HISTORY):]
            save_history(user_id, global_history)

        if on_loop:
            on_loop(loop_count)

        pending_files = [file_data] if file_data else []
        yield f"Processing... ({loop_count}/{MAX_LOOPS})", False, loop_count, pending_files

    reply = "Error: Max tool call loops reached."
    global_history.append({"role": "assistant", "content": reply})
    save_history(user_id, global_history)
    yield reply, True, loop_count, []


def chat_stream(user_id: int, message: str):
    """Simulated streaming for the compact API."""
    _ensure_history(user_id)

    global_history = conversations[user_id]
    global_history.append({"role": "user", "content": message})

    if len(global_history) > MAX_HISTORY + 1:
        global_history[:] = [global_history[0]] + global_history[-(MAX_HISTORY):]
    
    save_history(user_id, global_history)

    full_prompt = _format_history(global_history)
    raw_response = _call_ai_api(full_prompt)
    
    if not raw_response:
        err_msg = "Error: Gemini API unavailable."
        global_history.append({"role": "assistant", "content": err_msg})
        save_history(user_id, global_history)
        yield err_msg, True
        return

    reply, _ = parse_response(raw_response)
    global_history.append({"role": "assistant", "content": reply})
    save_history(user_id, global_history)
    
    words = reply.split(" ")
    for i in range(len(words)):
        chunk = words[i] + (" " if i < len(words) - 1 else "")
        yield chunk, False
        time.sleep(0.01)
    
    yield reply, True


def chat(user_id: int, message: str) -> str:
    _ensure_history(user_id)

    global_history = conversations[user_id]
    global_history.append({"role": "user", "content": message})

    if len(global_history) > MAX_HISTORY + 1:
        global_history[:] = [global_history[0]] + global_history[-(MAX_HISTORY):]
    
    save_history(user_id, global_history)

    full_prompt = _format_history(global_history)
    raw_response = _call_ai_api(full_prompt)
    
    if not raw_response:
        err_msg = "Error: Gemini API unavailable."
        global_history.append({"role": "assistant", "content": err_msg})
        save_history(user_id, global_history)
        return err_msg

    reply, _ = parse_response(raw_response)
    global_history.append({"role": "assistant", "content": reply})
    save_history(user_id, global_history)
    return reply


def clear_history(user_id: int) -> None:
    conversations.pop(user_id, None)
    clear_fb_history(user_id)
    close_sandbox(user_id)


def clear_all_data(user_id: int) -> None:
    """Wipe everything: in-memory, Firebase (history, files, versions), and sandbox."""
    conversations.pop(user_id, None)
    clear_all_fb_data(user_id)
    close_sandbox(user_id)
