import re
import time
import httpx
import logging

logger = logging.getLogger(__name__)

GEMINI_V2_URL = "https://puruboy-api.vercel.app/api/ai/gemini-v2"
GEMINI_FALLBACK_URL = "https://puruboy-api.vercel.app/api/ai/gemini"
AI_MAX_RETRIES = 5
INVALID_TOOL_MAX_RETRIES = 5

def _call_ai_api(prompt: str) -> str | None:
    for attempt in range(AI_MAX_RETRIES):
        url = GEMINI_V2_URL if attempt % 2 == 0 else GEMINI_FALLBACK_URL
        try:
            resp = httpx.post(url, json={"prompt": prompt}, timeout=60)
            resp.raise_for_status()
            data = resp.json()
            if data.get("success") and data.get("result", {}).get("answer"):
                return data["result"]["answer"]
            logger.warning(f"AI API returned logical failure (attempt {attempt+1}, url={url}): {data}")
        except Exception as e:
            logger.warning(f"AI API connection error (attempt {attempt+1}, url={url}): {e}")
        if attempt < AI_MAX_RETRIES - 1:
            time.sleep(2 ** attempt)
    return None

def parse_response(text: str):
    tool_match = re.search(r"<tool>(.*?)</tool>", text, re.DOTALL)
    tool_data = None
    if tool_match:
        tool_content = tool_match.group(1)
        name_match = re.search(r"<name>(.*?)</name>", tool_content, re.DOTALL)
        if name_match:
            tool_name = name_match.group(1).strip()
            params = {}
            param_matches = re.finditer(r"<parameter>(.*?)</parameter>", tool_content, re.DOTALL)
            for pm in param_matches:
                p_content = pm.group(1)
                p_name_match = re.search(r"<name>(.*?)</name>", p_content, re.DOTALL)
                p_val_match = re.search(r"<value>(.*?)</value>", p_content, re.DOTALL)
                if p_name_match and p_val_match:
                    params[p_name_match.group(1).strip()] = p_val_match.group(1).strip()
            tool_data = {"name": tool_name, "arguments": params}

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
            message = text.strip()
            if tool_match:
                message = re.sub(r"<tool>.*?</tool>", "", message, flags=re.DOTALL).strip()
    return message, tool_data
