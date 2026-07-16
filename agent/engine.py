import time
import logging
from config import TOKEN_COMPACT_LIMIT, MAX_LOOPS
from .history import conversations, _ensure_history, get_token_count
from .ai import _call_ai_api, parse_response, INVALID_TOOL_MAX_RETRIES
from .formatter import _format_history
from .compact import compact_history
from sandbox import execute_tool
from firebase import save_history

logger = logging.getLogger(__name__)

def chat_with_tools(user_id: int, message: str, on_loop=None):
    _ensure_history(user_id)
    if get_token_count(user_id) >= TOKEN_COMPACT_LIMIT:
        logger.info(f"Auto-compacting history for {user_id} (tokens: {get_token_count(user_id)})")
        yield "🧹 Cleaning up memory (compacting)...", False, 0, []
        compact_history(user_id, message)
    global_history = conversations[user_id]
    global_history.append({"role": "user", "content": message})
    save_history(user_id, global_history)
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
            global_history.append({"role": "assistant", "content": reply})
            save_history(user_id, global_history)
            yield reply, True, loop_count, []
            return
        loop_history.append({"role": "assistant", "content": raw_response})
        save_history(user_id, global_history)
        func_name = tool_call["name"]
        func_args = tool_call["arguments"]
        reason = func_args.pop("reason", "")
        result_text, file_data = execute_tool(user_id, func_name, func_args)
        if any(result_text.startswith(prefix) for prefix in ["FAIL", "Error", "Tool error"]):
            invalid_tool_count += 1
            if invalid_tool_count > INVALID_TOOL_MAX_RETRIES:
                error_msg = f"Error: Too many invalid tool calls ({INVALID_TOOL_MAX_RETRIES}). Stopping."
                global_history.append({"role": "assistant", "content": error_msg})
                save_history(user_id, global_history)
                yield error_msg, True, loop_count, []
                return
            time.sleep(2 ** (invalid_tool_count - 1))
            feedback = (f"[SYSTEM ERROR] Your tool call to '{func_name}' was invalid:\n{result_text}\n\n"
                       "Please analyze why it failed, correct your mistake, and try again.")
            loop_history.append({"role": "tool", "content": feedback})
            save_history(user_id, global_history)
        else:
            invalid_tool_count = 0
            loop_history.append({"role": "tool", "content": result_text})
            save_history(user_id, global_history)
        loop_count += 1
        if on_loop: on_loop(loop_count)
        reason_text = f": {reason}" if reason else ""
        yield f"[{loop_count}/{MAX_LOOPS}] {func_name}{reason_text}", False, loop_count, [file_data] if file_data else []
    reply = "Error: Max tool call loops reached."
    global_history.append({"role": "assistant", "content": reply})
    save_history(user_id, global_history)
    yield reply, True, loop_count, []

def chat_stream(user_id: int, message: str):
    _ensure_history(user_id)
    global_history = conversations[user_id]
    global_history.append({"role": "user", "content": message})
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
    for word in reply.split(" "):
        yield word + " ", False
        time.sleep(0.01)
    yield reply, True

def chat(user_id: int, message: str) -> str:
    _ensure_history(user_id)
    global_history = conversations[user_id]
    global_history.append({"role": "user", "content": message})
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
