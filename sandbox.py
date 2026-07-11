from e2b import Sandbox
from config import E2B_API_KEY

sandboxes: dict[int, Sandbox] = {}

BASH_TIMEOUT = 60_000  # 1 minute in ms


def get_sandbox(user_id: int) -> Sandbox:
    if user_id not in sandboxes:
        sandboxes[user_id] = Sandbox(api_key=E2B_API_KEY)
    return sandboxes[user_id]


def close_sandbox(user_id: int) -> None:
    if user_id in sandboxes:
        try:
            sandboxes[user_id].close()
        except Exception:
            pass
        del sandboxes[user_id]


TOOLS = [
    {
        "type": "function",
        "function": {
            "name": "bash",
            "description": "Execute a bash command in the sandbox. Timeout: 60 seconds.",
            "parameters": {
                "type": "object",
                "properties": {
                    "command": {
                        "type": "string",
                        "description": "The bash command to execute",
                    }
                },
                "required": ["command"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "write_file",
            "description": "Write content to a file in the sandbox. Creates parent directories automatically.",
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {"type": "string", "description": "Absolute file path (e.g. /home/user/app.py)"},
                    "content": {"type": "string", "description": "Full file content to write"},
                },
                "required": ["path", "content"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "edit_file",
            "description": "Edit a file by replacing exact old text with new text. The old_text must match exactly (including whitespace and indentation).",
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {"type": "string", "description": "Absolute file path"},
                    "old_text": {"type": "string", "description": "Exact text to find and replace (must match exactly)"},
                    "new_text": {"type": "string", "description": "Replacement text"},
                },
                "required": ["path", "old_text", "new_text"],
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
                },
                "required": ["path"],
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
                },
                "required": ["path"],
            },
        },
    },
]


def execute_tool(user_id: int, name: str, arguments: dict) -> tuple:
    """Execute a tool. Returns (result_text, file_data).
    file_data is None for most tools, or (filename, content_bytes, caption) for send_file."""
    sandbox = get_sandbox(user_id)
    try:
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

        elif name == "write_file":
            path = arguments.get("path", "")
            content = arguments.get("content", "")
            if not path:
                return "FAIL: No file path provided. Specify a path like /home/user/file.py", None
            if not content:
                return f"FAIL: Empty content for {path}. Provide the file content to write.", None
            sandbox.files.write(path, content)
            return f"SUCCESS: File written to {path} ({len(content)} chars, {len(content.splitlines())} lines)", None

        elif name == "edit_file":
            path = arguments.get("path", "")
            old_text = arguments.get("old_text", "")
            new_text = arguments.get("new_text", "")
            if not path:
                return "FAIL: No file path provided.", None
            if not old_text:
                return "FAIL: old_text is empty. Provide the exact text to find.", None
            try:
                content = sandbox.files.read(path)
            except Exception:
                return f"FAIL: Cannot read {path}. File may not exist. Use write_file to create it first.", None
            if old_text not in content:
                preview = content[:500] + "..." if len(content) > 500 else content
                return (
                    f"FAIL: old_text not found in {path}.\n"
                    f"Make sure the text matches exactly (including spaces and indentation).\n"
                    f"File preview:\n```\n{preview}\n```"
                ), None
            new_content = content.replace(old_text, new_text, 1)
            sandbox.files.write(path, new_content)
            return f"SUCCESS: File edited at {path} ({len(old_text)} chars replaced)", None

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
            return f"Error: Unknown tool '{name}'. Available tools: bash, write_file, read_file, edit_file, send_file.", None

    except Exception as e:
        return f"Tool error ({name}): {str(e)}\nTip: Analyze the error and try a different approach.", None
