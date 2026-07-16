from e2b import Sandbox
from config import E2B_API_KEY
from firebase import (
    get_fb_version,
    import_files_to_sandbox,
    write_fb_file,
    save_fb_binary_file,
    edit_fb_file,
    delete_fb_file,
)

sandboxes: dict[int, Sandbox] = {}
_sandbox_versions: dict[int, str] = {}  # chat_id -> last synced Firebase version

BASH_TIMEOUT = 60  # 1 minute
SANDBOX_TIMEOUT = 900  # 15 minutes


def _sync_sandbox(sandbox, chat_id: int):
    """Check Firebase version vs sandbox version.txt and sync if needed."""
    fb_ver = get_fb_version(chat_id)
    if fb_ver is None:
        return  # No files in Firebase yet
    try:
        local_ver = sandbox.files.read("/home/user/version.txt").strip()
    except Exception:
        local_ver = None
    if local_ver == fb_ver:
        return  # Already in sync
    import_files_to_sandbox(sandbox, chat_id)
    _sandbox_versions[chat_id] = fb_ver


def get_sandbox(user_id: int) -> Sandbox:
    if user_id not in sandboxes:
        sandboxes[user_id] = Sandbox(api_key=E2B_API_KEY, timeout=SANDBOX_TIMEOUT)
    return sandboxes[user_id]


def close_sandbox(user_id: int) -> None:
    if user_id in sandboxes:
        try:
            sandboxes[user_id].kill()
        except Exception:
            pass
        del sandboxes[user_id]


REASON_DESC = "Wajib diisi — jelaskan kenapa kamu pakai tool ini dan apa yang kamu harapkan"

TOOLS = [
    {
        "type": "function",
        "function": {
            "name": "bash",
            "description": "Execute a bash command in the sandbox. Timeout: 1 minute.",
            "parameters": {
                "type": "object",
                "properties": {
                    "command": {
                        "type": "string",
                        "description": "The bash command to execute",
                    },
                    "reason": {"type": "string", "description": REASON_DESC},
                },
                "required": ["command", "reason"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "write_file",
            "description": "Write content to a file in Firebase storage. Files are persisted and synced to sandbox when needed.",
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {"type": "string", "description": "Absolute file path (e.g. /home/user/app.py)"},
                    "content": {"type": "string", "description": "Full file content to write"},
                    "reason": {"type": "string", "description": REASON_DESC},
                },
                "required": ["path", "content", "reason"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "edit_file",
            "description": "Edit a file in Firebase storage by replacing exact old text with new text. The old_text must match exactly (including whitespace and indentation).",
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {"type": "string", "description": "Absolute file path"},
                    "old_text": {"type": "string", "description": "Exact text to find and replace (must match exactly)"},
                    "new_text": {"type": "string", "description": "Replacement text"},
                    "reason": {"type": "string", "description": REASON_DESC},
                },
                "required": ["path", "old_text", "new_text", "reason"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "delete_file",
            "description": "Delete a file from Firebase storage.",
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {"type": "string", "description": "Absolute file path to delete (e.g. /home/user/app.py)"},
                    "reason": {"type": "string", "description": REASON_DESC},
                },
                "required": ["path", "reason"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "read_file",
            "description": "Read a file from the sandbox with optional start and end line numbers. Use this to view file contents, check code, or read logs.",
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {"type": "string", "description": "Absolute file path (e.g. /home/user/app.py)"},
                    "start_line": {"type": "integer", "description": "Starting line number (1-based, optional). Default: 1"},
                    "end_line": {"type": "integer", "description": "Ending line number (optional). If omitted, reads from start_line to end of file."},
                    "reason": {"type": "string", "description": REASON_DESC},
                },
                "required": ["path", "reason"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "send_file",
            "description": "Send a file from the sandbox to the user's Telegram chat. Use this to share generated files (code, images, documents, etc).",
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {"type": "string", "description": "Absolute path of the file in the sandbox to send"},
                    "caption": {"type": "string", "description": "Caption/description for the file (optional)"},
                    "reason": {"type": "string", "description": REASON_DESC},
                },
                "required": ["path", "reason"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "save_file",
            "description": "Save a file from the sandbox to Firebase storage permanently. Max size: 2MB.",
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {"type": "string", "description": "Absolute path of the file in the sandbox to save"},
                    "reason": {"type": "string", "description": REASON_DESC},
                },
                "required": ["path", "reason"],
            },
        },
    },
]


def execute_tool(user_id: int, name: str, arguments: dict) -> tuple:
    """Execute a tool. Returns (result_text, file_data).
    file_data is None for most tools, or (filename, content_bytes, caption) for send_file.
    If the sandbox is dead, it is automatically recreated and the call is retried once."""
    return _execute_tool_inner(user_id, name, arguments, _retry=True)


def _execute_tool_inner(user_id: int, name: str, arguments: dict, _retry: bool) -> tuple:
    # Firebase-based tools: write_file, edit_file, delete_file
    if name == "write_file":
        path = arguments.get("path", "")
        content = arguments.get("content", "")
        if not path:
            return "FAIL: No file path provided. Specify a path like /home/user/file.py", None
        if not content:
            return f"FAIL: Empty content for {path}. Provide the file content to write.", None
        return write_fb_file(user_id, path, content), None
    if name == "edit_file":
        path = arguments.get("path", "")
        old_text = arguments.get("old_text", "")
        new_text = arguments.get("new_text", "")
        if not path:
            return "FAIL: No file path provided.", None
        if not old_text:
            return "FAIL: old_text is empty. Provide the exact text to find.", None
        return edit_fb_file(user_id, path, old_text, new_text), None
    if name == "delete_file":
        path = arguments.get("path", "")
        if not path:
            return "FAIL: No file path provided.", None
        return delete_fb_file(user_id, path), None
    if name == "save_file":
        path = arguments.get("path", "")
        if not path:
            return "FAIL: No file path provided.", None
        try:
            sandbox = get_sandbox(user_id)
            file_bytes = sandbox.files.read(path, format="bytes")
        except Exception:
            return f"FAIL: Cannot read {path} from sandbox to save. It might not exist.", None
        max_size = 2 * 1024 * 1024  # 2MB
        if len(file_bytes) > max_size:
            return f"FAIL: File too large ({len(file_bytes) / 1024 / 1024:.1f}MB). Max 2MB.", None
        # Try decoding as text; if it fails, save as binary (base64)
        try:
            content = file_bytes.decode("utf-8")
            return write_fb_file(user_id, path, content), None
        except UnicodeDecodeError:
            return save_fb_binary_file(user_id, path, file_bytes), None

    # Sandbox-based tools: sync from Firebase first, then execute
    try:
        sandbox = get_sandbox(user_id)
        _sync_sandbox(sandbox, user_id)
        if name == "bash":
            cmd = arguments.get("command", "")
            if not cmd.strip():
                return "FAIL: Empty command. Provide a valid bash command to execute.", None
            result = sandbox.commands.run(cmd, timeout=BASH_TIMEOUT)
            output = ""
            if result.stdout:
                output += result.stdout
            if result.stderr:
                output += f"\n[stderr]\n{result.stderr}"
            if result.error:
                output += f"\n[error]\n{result.error}"
            if result.exit_code and result.exit_code != 0:
                output += f"\n[exit code: {result.exit_code}]"
            if not output.strip():
                output = "(command produced no output)"

            if result.exit_code and result.exit_code != 0:
                feedback = f"FAIL (exit code {result.exit_code}):\n"
            else:
                feedback = "SUCCESS:\n"
            return feedback + output, None
        elif name == "read_file":
            path = arguments.get("path", "")
            start_line = arguments.get("start_line", 1)
            end_line = arguments.get("end_line", None)
            if not path:
                return "FAIL: No file path provided. Specify a path like /home/user/file.py", None
            try:
                content = sandbox.files.read(path)
            except Exception:
                return f"FAIL: Cannot read {path}. File may not exist. Use write_file to create it first.", None

            lines = content.splitlines()
            total_lines = len(lines)
            start = max(1, start_line)
            if end_line is None or end_line > total_lines:
                end = total_lines
            else:
                end = end_line

            if start > total_lines:
                return f"FAIL: start_line ({start}) exceeds file length ({total_lines} lines).", None
            if start > end:
                return f"FAIL: start_line ({start}) is greater than end_line ({end}).", None

            selected = lines[start - 1:end]
            result = "\n".join(selected)
            line_range = f"lines {start}-{end}" if start != 1 or end != total_lines else f"all {total_lines} lines"
            return f"SUCCESS: {path} ({line_range}, {total_lines} total):\n```\n{result}\n```", None

        elif name == "send_file":
            path = arguments.get("path", "")
            caption = arguments.get("caption", "")
            if not path:
                return "Error: No file path provided. Specify which file to send.", None
            try:
                file_bytes = sandbox.files.read(path, format="bytes")
            except Exception:
                return f"Error: Cannot read {path}. File may not exist. Use bash or write_file to create it first.", None
            filename = path.split("/")[-1] or "file"
            return f"Sending file: {filename}", (filename, file_bytes, caption)

        else:
            return f"Error: Unknown tool '{name}'. Available tools: bash, write_file, read_file, edit_file, delete_file, send_file, save_file.", None

    except Exception as e:
        err_str = str(e).lower()
        is_sandbox_gone = "sandbox" in err_str and ("not found" in err_str or "does not exist" in err_str or "timeout" in err_str)
        if _retry and is_sandbox_gone:
            # Sandbox is dead/expired — force close and recreate
            close_sandbox(user_id)
            return _execute_tool_inner(user_id, name, arguments, _retry=False)
        if _retry and not is_sandbox_gone:
            # Other error — still try recreating once
            close_sandbox(user_id)
            return _execute_tool_inner(user_id, name, arguments, _retry=False)
        return f"Tool error ({name}): {str(e)}\nTip: The sandbox may have died. Try /clear to reset.", None
