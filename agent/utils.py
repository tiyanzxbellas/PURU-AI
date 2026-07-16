from .history import conversations
from firebase import clear_fb_history, clear_all_fb_data
from sandbox import close_sandbox
from config import MEMORY_PATH

def clear_history(user_id: int) -> None:
    conversations.pop(user_id, None)
    clear_fb_history(user_id)
    close_sandbox(user_id)

def clear_all_data(user_id: int) -> None:
    conversations.pop(user_id, None)
    clear_all_fb_data(user_id)
    close_sandbox(user_id)
