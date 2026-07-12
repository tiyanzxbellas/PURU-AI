import base64
import uuid
import json
import httpx
from cryptography.fernet import Fernet
from config import FB_RTDB_URL, FERNET_KEY

cipher = Fernet(FERNET_KEY.encode())


def _encode_path(path: str) -> str:
    """Encode file path as Firebase-safe key using base64url."""
    return base64.urlsafe_b64encode(path.encode()).decode().rstrip("=")


def _decode_path(encoded: str) -> str:
    """Decode base64url key back to file path."""
    padded = encoded + "=" * (4 - len(encoded) % 4)
    return base64.urlsafe_b64decode(padded).decode()


def _fb_get(path: str):
    """GET from Firebase RTDB. Returns parsed JSON or None."""
    resp = httpx.get(f"{FB_RTDB_URL}/{path}.json", timeout=10)
    resp.raise_for_status()
    if resp.status_code == 200 and resp.text.strip() not in ("null", ""):
        return resp.json()
    return None


def _fb_put(path: str, data):
    """PUT to Firebase RTDB."""
    resp = httpx.put(f"{FB_RTDB_URL}/{path}.json", json=data, timeout=10)
    resp.raise_for_status()


def _fb_delete(path: str):
    """DELETE from Firebase RTDB."""
    resp = httpx.delete(f"{FB_RTDB_URL}/{path}.json", timeout=10)
    resp.raise_for_status()


def generate_version() -> str:
    return uuid.uuid4().hex


def update_file_version(chat_id: int) -> str:
    """Generate and store a new version string in Firebase."""
    ver = generate_version()
    _fb_put(f"versions/{chat_id}", ver)
    return ver


def get_fb_version(chat_id: int) -> str | None:
    """Get the current version string from Firebase."""
    return _fb_get(f"versions/{chat_id}")


def get_all_fb_files(chat_id: int) -> dict[str, str]:
    """Get all files for a chat from Firebase. Returns {path: content}."""
    data = _fb_get(f"files/{chat_id}")
    if not data or not isinstance(data, dict):
        return {}
    return {_decode_path(k): v for k, v in data.items()}


def import_files_to_sandbox(sandbox, chat_id: int):
    """Import all files from Firebase RTDB into the sandbox."""
    files = get_all_fb_files(chat_id)
    
    # Collect all parent directories to create them
    dirs_to_create = set()
    for path in files.keys():
        parts = path.rstrip("/").split("/")
        if len(parts) > 2:  # has directories beyond root (e.g. /home/user/dir/file.py)
            dir_path = "/".join(parts[:-1])
            if dir_path:
                dirs_to_create.add(dir_path)
                
    if dirs_to_create:
        dirs_str = " ".join(f"'{d}'" for d in sorted(dirs_to_create, key=len))
        try:
            sandbox.commands.run(f"mkdir -p {dirs_str}")
        except Exception:
            pass

    for path, content in files.items():
        sandbox.files.write(path, content)
    ver = get_fb_version(chat_id)
    if ver:
        sandbox.files.write("/home/user/version.txt", ver)


def write_fb_file(chat_id: int, path: str, content: str) -> str:
    """Write a file to Firebase RTDB."""
    _fb_put(f"files/{chat_id}/{_encode_path(path)}", content)
    update_file_version(chat_id)
    return f"SUCCESS: File written to Firebase at {path} ({len(content)} chars)"


def save_fb_binary_file(chat_id: int, path: str, file_bytes: bytes) -> str:
    """Save a binary file to Firebase RTDB (base64 encoded) with 2MB limit."""
    if len(file_bytes) > 2 * 1024 * 1024:
        return "FAIL: File size exceeds 2MB limit. Cannot save."
    
    encoded_bytes = base64.b64encode(file_bytes).decode()
    _fb_put(f"files/{chat_id}/{_encode_path(path)}", encoded_bytes)
    update_file_version(chat_id)
    return f"SUCCESS: Binary file saved to Firebase at {path} ({len(file_bytes)} bytes)"


def edit_fb_file(chat_id: int, path: str, old_text: str, new_text: str) -> str:
    """Read, edit, and write back a file in Firebase RTDB."""
    content = _fb_get(f"files/{chat_id}/{_encode_path(path)}")
    if content is None:
        return f"FAIL: Cannot read {path}. File may not exist in Firebase. Use write_file to create it first."
    if not isinstance(content, str):
        content = str(content)
    if old_text not in content:
        preview = content[:500] + "..." if len(content) > 500 else content
        return (
            f"FAIL: old_text not found in {path}.\n"
            f"Make sure the text matches exactly (including spaces and indentation).\n"
            f"File preview:\n```\n{preview}\n```"
        )
    new_content = content.replace(old_text, new_text, 1)
    _fb_put(f"files/{chat_id}/{_encode_path(path)}", new_content)
    update_file_version(chat_id)
    return f"SUCCESS: File edited at {path} ({len(old_text)} chars replaced)"


def delete_fb_file(chat_id: int, path: str) -> str:
    """Delete a file from Firebase RTDB."""
    existing = _fb_get(f"files/{chat_id}/{_encode_path(path)}")
    if existing is None:
        return f"FAIL: File {path} does not exist in Firebase."
    _fb_delete(f"files/{chat_id}/{_encode_path(path)}")
    update_file_version(chat_id)
    return f"SUCCESS: File {path} deleted from Firebase."


def save_history(chat_id: int, history: list[dict]):
    """Encrypt and save conversation history to Firebase."""
    try:
        data_str = json.dumps(history)
        encrypted_data = cipher.encrypt(data_str.encode()).decode()
        _fb_put(f"history/{chat_id}", encrypted_data)
    except Exception as e:
        print(f"Error saving history for {chat_id}: {e}")


def get_history(chat_id: int) -> list[dict] | None:
    """Retrieve and decrypt conversation history from Firebase."""
    try:
        encrypted_data = _fb_get(f"history/{chat_id}")
        if not encrypted_data:
            return None
        decrypted_data = cipher.decrypt(encrypted_data.encode()).decode()
        return json.loads(decrypted_data)
    except Exception as e:
        print(f"Error retrieving history for {chat_id}: {e}")
        return None


def clear_fb_history(chat_id: int):
    """Delete history from Firebase."""
    _fb_delete(f"history/{chat_id}")


def clear_all_fb_data(chat_id: int):
    """Delete all data (history, files, versions) for a chat from Firebase."""
    _fb_delete(f"history/{chat_id}")
    _fb_delete(f"files/{chat_id}")
    _fb_delete(f"versions/{chat_id}")
