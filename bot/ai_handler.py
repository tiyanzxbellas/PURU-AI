import asyncio
import logging
from io import BytesIO
from telegram import Update
from agent.autogen_engine import chat_with_autogen

logger = logging.getLogger(__name__)

async def run_with_tools(update: Update, message_text: str) -> None:
    chat_id = update.effective_chat.id
    status_msg = await update.message.reply_text("Thinking...", do_quote=True)
    try:
        final_response = ""
        async for text, is_done, loop_count, pending_files in chat_with_autogen(chat_id, message_text):
            if not is_done:
                try: await status_msg.edit_text(text)
                except Exception: pass
            else: final_response = text
            for filename, file_bytes, caption in pending_files:
                try:
                    bio = BytesIO(file_bytes)
                    bio.name = filename
                    ext = filename.rsplit(".", 1)[-1].lower() if "." in filename else ""
                    if ext in ("mp3", "wav", "ogg", "flac", "m4a", "aac", "wma"): await update.message.reply_audio(audio=bio, caption=caption or filename)
                    elif ext == "webm":
                        try: await update.message.reply_audio(audio=bio, caption=caption or filename)
                        except Exception:
                            bio.seek(0)
                            await update.message.reply_video(video=bio, caption=caption or filename)
                    elif ext in ("mp4", "mkv", "avi", "mov"): await update.message.reply_video(video=bio, caption=caption or filename)
                    elif ext in ("jpg", "jpeg", "png", "gif", "webp", "bmp"): await update.message.reply_photo(photo=bio, caption=caption or filename)
                    else: await update.message.reply_document(document=bio, filename=filename, caption=caption or filename)
                except Exception as e: logger.warning("Failed to send file %s: %s", filename, e)
        if final_response:
            try: await status_msg.edit_text(final_response, parse_mode="Markdown")
            except Exception:
                try: await status_msg.edit_text(final_response)
                except Exception: pass
        else:
            try: await status_msg.edit_text("No response from AI.")
            except Exception: pass
    except asyncio.TimeoutError:
        try: await status_msg.edit_text("Timed out. Try /clear and send a shorter message.")
        except Exception: pass
    except Exception as e:
        logger.exception("Error in run_with_tools")
        try: await status_msg.edit_text(f"Error: {e}")
        except Exception: pass
