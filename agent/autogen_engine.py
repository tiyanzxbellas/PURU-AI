import asyncio
import json
import logging
import re
from datetime import datetime

from autogen_core.models import SystemMessage, UserMessage, AssistantMessage
from autogen_core.tools import FunctionTool
from autogen_agentchat.agents import AssistantAgent
from autogen_agentchat.messages import TextMessage, ToolCallRequestEvent, ToolCallExecutionEvent, ToolCallSummaryMessage
from autogen_agentchat.base import TaskResult
from autogen_ext.models.openai import OpenAIChatCompletionClient

from config import (
    AUTOGEN_BASE_URL,
    AUTOGEN_API_KEY,
    AUTOGEN_MODEL_NAME,
    MAX_LOOPS,
    SYSTEM_PROMPT,
    WORKER_SYSTEM_PROMPT,
    MEMORY_PATH,
    TOKEN_COMPACT_LIMIT,
)
from sandbox import execute_tool
from firebase import save_history, get_fb_file
from agent.history import conversations, _ensure_history, get_token_count
from agent.compact import compact_history

logger = logging.getLogger(__name__)

MAX_RETRIES = 5
RETRY_DELAYS = [1, 2, 4, 8, 16]


def _make_system_content(user_id: int) -> str:
    now = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    base = f"{SYSTEM_PROMPT}\n\nCurrent date and time: {now} (UTC+0 / use local timezone if specified)."
    memory = get_fb_file(user_id, MEMORY_PATH)
    if memory:
        base = f"{base}\n\n---\n\n[MEMORY]\n{memory}"
    return base


async def _populate_context_from_fb(agent: AssistantAgent, fb_history: list):
    for m in fb_history:
        role, content = m["role"], m.get("content", "")
        if role == "system":
            await agent.model_context.add_message(SystemMessage(content=content))
        elif role == "user":
            await agent.model_context.add_message(UserMessage(content=content, source="user"))
        elif role == "assistant":
            await agent.model_context.add_message(AssistantMessage(content=content, source="puru"))


def _make_worker_tools(user_id: int, pending_files: list) -> list[FunctionTool]:
    def bash_cmd(command: str, reason: str = "") -> str:
        result_text, file_data = execute_tool(user_id, "bash", {"command": command, "reason": reason})
        if file_data:
            pending_files.append(file_data)
        return result_text

    def write_file(path: str, content: str, reason: str = "") -> str:
        result_text, file_data = execute_tool(user_id, "write_file", {"path": path, "content": content, "reason": reason})
        if file_data:
            pending_files.append(file_data)
        return result_text

    def edit_file(path: str, old_text: str, new_text: str, reason: str = "") -> str:
        result_text, file_data = execute_tool(user_id, "edit_file", {"path": path, "old_text": old_text, "new_text": new_text, "reason": reason})
        if file_data:
            pending_files.append(file_data)
        return result_text

    def delete_file(path: str, reason: str = "") -> str:
        result_text, file_data = execute_tool(user_id, "delete_file", {"path": path, "reason": reason})
        if file_data:
            pending_files.append(file_data)
        return result_text

    def read_file(path: str, start_line: int = 1, end_line: int | None = None, reason: str = "") -> str:
        args = {"path": path, "start_line": start_line, "reason": reason}
        if end_line is not None:
            args["end_line"] = end_line
        result_text, file_data = execute_tool(user_id, "read_file", args)
        if file_data:
            pending_files.append(file_data)
        return result_text

    def send_file(path: str, caption: str = "", reason: str = "") -> str:
        result_text, file_data = execute_tool(user_id, "send_file", {"path": path, "caption": caption, "reason": reason})
        if file_data:
            pending_files.append(file_data)
        return result_text

    def save_file(path: str, reason: str = "") -> str:
        result_text, file_data = execute_tool(user_id, "save_file", {"path": path, "reason": reason})
        if file_data:
            pending_files.append(file_data)
        return result_text

    def ls_cmd(path: str = ".", reason: str = "") -> str:
        result_text, file_data = execute_tool(user_id, "bash", {"command": f"ls -la {path}", "reason": reason or "list directory"})
        if file_data:
            pending_files.append(file_data)
        return result_text

    return [
        FunctionTool(bash_cmd, description="Execute a bash command in the sandbox. Timeout: 1 minute.", name="bash"),
        FunctionTool(ls_cmd, description="List files and directories in the sandbox.", name="ls"),
        FunctionTool(write_file, description="Write content to a file in Firebase storage. Auto-saves to Firebase.", name="write_file"),
        FunctionTool(edit_file, description="Edit a file in Firebase storage by replacing exact old text with new text.", name="edit_file"),
        FunctionTool(delete_file, description="Delete a file from Firebase storage.", name="delete_file"),
        FunctionTool(read_file, description="Read a file from the sandbox with optional start and end line numbers.", name="read_file"),
        FunctionTool(send_file, description="Send a file from the sandbox to the Telegram chat.", name="send_file"),
        FunctionTool(save_file, description="Save a file from the sandbox to Firebase permanently. Max 2MB.", name="save_file"),
    ]


def _strip_fake_tool_output(text: str) -> str:
    lines = text.split("\n")
    result = []
    skip = False
    for line in lines:
        if line.strip().startswith("⚙️ Tool executions:"):
            skip = True
            continue
        if skip:
            if re.match(r"^[ \t]*[✓⚠️]", line) or line.strip() == "":
                continue
            skip = False
        result.append(line)
    return "\n".join(result).strip()


async def chat_with_autogen(user_id: int, message: str):
    _ensure_history(user_id)
    if get_token_count(user_id) >= TOKEN_COMPACT_LIMIT:
        logger.info("Auto-compacting history for %d", user_id)
        yield "Cleaning up memory (compacting)...", False, 0, []
        compact_history(user_id, message)

    fb_history = conversations[user_id]
    pending_files: list = []
    system_content = _make_system_content(user_id)

    model_client = OpenAIChatCompletionClient(
        model=AUTOGEN_MODEL_NAME,
        base_url=AUTOGEN_BASE_URL,
        api_key=AUTOGEN_API_KEY,
        max_retries=MAX_RETRIES,
        model_info={
            "vision": False,
            "function_calling": True,
            "json_output": True,
            "structured_output": True,
            "family": "unknown",
        },
    )

    worker_tools = _make_worker_tools(user_id, pending_files)

    async def delegate_task(task: str, reason: str = "") -> str:
        try:
            worker = AssistantAgent(
                name="worker",
                model_client=model_client,
                tools=worker_tools,
                system_message=WORKER_SYSTEM_PROMPT,
                reflect_on_tool_use=True,
                max_tool_iterations=MAX_LOOPS,
            )
            result = ""
            async for msg in worker.run_stream(task=task):
                if isinstance(msg, TextMessage) and msg.source == "worker" and msg.content.strip():
                    result = msg.content
            return result or "(no result)"
        except Exception as e:
            logger.warning("Worker agent failed for %d: %s", user_id, e)
            return f"Worker error: {e}"

    def orchestrator_read_file(path: str, reason: str = "") -> str:
        result_text, file_data = execute_tool(user_id, "read_file", {"path": path, "reason": reason})
        if file_data:
            pending_files.append(file_data)
        return result_text

    def orchestrator_write_file(path: str, content: str, reason: str = "") -> str:
        result_text, file_data = execute_tool(user_id, "write_file", {"path": path, "content": content, "reason": reason})
        if file_data:
            pending_files.append(file_data)
        return result_text

    def orchestrator_edit_file(path: str, old_text: str, new_text: str, reason: str = "") -> str:
        result_text, file_data = execute_tool(user_id, "edit_file", {"path": path, "old_text": old_text, "new_text": new_text, "reason": reason})
        if file_data:
            pending_files.append(file_data)
        return result_text

    def orchestrator_ls(path: str = ".", reason: str = "") -> str:
        result_text, file_data = execute_tool(user_id, "bash", {"command": f"ls -la {path}", "reason": reason or "list directory"})
        if file_data:
            pending_files.append(file_data)
        return result_text

    def orchestrator_send_file(path: str, caption: str = "", reason: str = "") -> str:
        result_text, file_data = execute_tool(user_id, "send_file", {"path": path, "caption": caption, "reason": reason})
        if file_data:
            pending_files.append(file_data)
        return result_text

    def orchestrator_delete_file(path: str, reason: str = "") -> str:
        result_text, file_data = execute_tool(user_id, "delete_file", {"path": path, "reason": reason})
        if file_data:
            pending_files.append(file_data)
        return result_text

    def orchestrator_save_file(path: str, reason: str = "") -> str:
        result_text, file_data = execute_tool(user_id, "save_file", {"path": path, "reason": reason})
        if file_data:
            pending_files.append(file_data)
        return result_text

    orchestrator_tools = [
        FunctionTool(
            orchestrator_ls,
            description="List files and directories in the sandbox.",
            name="ls",
        ),
        FunctionTool(
            orchestrator_read_file,
            description="Read a file from the sandbox with optional start and end line numbers.",
            name="read_file",
        ),
        FunctionTool(
            orchestrator_write_file,
            description="Write content to a file in Firebase storage. Auto-saves to Firebase.",
            name="write_file",
        ),
        FunctionTool(
            orchestrator_edit_file,
            description="Edit a file in Firebase storage by replacing exact old text with new text.",
            name="edit_file",
        ),
        FunctionTool(
            orchestrator_send_file,
            description="Send a file from the sandbox to the Telegram chat.",
            name="send_file",
        ),
        FunctionTool(
            orchestrator_delete_file,
            description="Delete a file from Firebase storage.",
            name="delete_file",
        ),
        FunctionTool(
            orchestrator_save_file,
            description="Save a file from the sandbox to Firebase permanently. Max 2MB.",
            name="save_file",
        ),
        FunctionTool(
            delegate_task,
            description="Delegate a task to the worker agent. ONLY use this for bash commands, searching info, downloading media, or running code. For file operations (ls/read/write/edit), use the direct tools.",
            name="delegate_task",
        ),
    ]

    agent = AssistantAgent(
        name="puru",
        model_client=model_client,
        tools=orchestrator_tools,
        system_message=system_content,
        reflect_on_tool_use=True,
        max_tool_iterations=MAX_LOOPS,
    )

    fb_history.append({"role": "user", "content": message})

    final_response = ""
    loop_count = 0

    try:
        for attempt in range(MAX_RETRIES):
            await agent.model_context.clear()
            await _populate_context_from_fb(agent, fb_history[:-1])

            try:
                async for msg in agent.run_stream(task=message):
                    if isinstance(msg, TextMessage):
                        if msg.source == "puru" and msg.content.strip():
                            final_response = msg.content
                    elif isinstance(msg, ToolCallRequestEvent):
                        for call in msg.content:
                            args = json.loads(call.arguments) if isinstance(call.arguments, str) else call.arguments
                            reason = args.get("reason", "")[:60] if isinstance(args, dict) else str(args)[:60]
                            yield f"[{call.name}] {reason}", False, loop_count, []
                    elif isinstance(msg, ToolCallExecutionEvent):
                        loop_count += 1
                    elif isinstance(msg, ToolCallSummaryMessage):
                        loop_count += 1
                    elif isinstance(msg, TaskResult):
                        for m in reversed(msg.messages):
                            if isinstance(m, TextMessage) and m.source == "puru" and m.content.strip():
                                final_response = m.content
                                break

                if final_response.strip():
                    break

                if attempt < MAX_RETRIES - 1:
                    delay = RETRY_DELAYS[attempt]
                    yield f"⚠️ API kosong, coba lagi dalam {delay}s...", False, 0, []
                    await asyncio.sleep(delay)
                    final_response = ""
                    loop_count = 0
                else:
                    final_response = "(no response after retries)"

            except Exception as e:
                logger.warning("AutoGen attempt %d/%d failed for %d: %s", attempt + 1, MAX_RETRIES, user_id, e)
                if attempt < MAX_RETRIES - 1:
                    delay = RETRY_DELAYS[attempt]
                    yield f"⚠️ API error: {e}, coba lagi dalam {delay}s...", False, 0, []
                    await asyncio.sleep(delay)
                    final_response = ""
                    loop_count = 0
                else:
                    raise

    except Exception as e:
        logger.exception("AutoGen error for %d after %d retries", user_id, MAX_RETRIES)
        error_text = f"Error: {e}"
        fb_history.append({"role": "assistant", "content": error_text})
        save_history(user_id, fb_history)
        yield error_text, True, loop_count, []
        return
    finally:
        await model_client.close()

    display_response = _strip_fake_tool_output(final_response)

    fb_history.append({"role": "assistant", "content": display_response})
    save_history(user_id, fb_history)
    yield display_response, True, loop_count, pending_files
