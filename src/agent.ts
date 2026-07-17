import { ToolLoopAgent, tool, wrapLanguageModel, isStepCount, type ModelMessage, type LanguageModelMiddleware } from 'ai';
import { type LanguageModelV4CallOptions } from '@ai-sdk/provider';
import { createOpenAI } from '@ai-sdk/openai';
import { config } from './config.js';
import { z } from 'zod';
import * as vfs from './vfs.js';

const provider = createOpenAI({
  baseURL: config.ai.baseURL,
  apiKey: config.ai.apiKey,
  name: 'puru',
});

function estimatePromptTokens(prompt: Array<{ role: string; content: any }>): number {
  let chars = 0;
  for (const msg of prompt) {
    if (typeof msg.content === 'string') {
      chars += msg.content.length;
    } else if (Array.isArray(msg.content)) {
      for (const part of msg.content) {
        if (part.type === 'text') chars += (part.text || '').length;
        else if (part.type === 'reasoning') chars += (part.text || '').length;
        else if (part.type === 'tool-call') chars += JSON.stringify(part.args || {}).length + (part.toolName || '').length + 50;
        else if (part.type === 'tool-result') {
          const r = typeof part.result === 'string' ? part.result : JSON.stringify(part.result || {});
          chars += r.length;
        }
      }
    }
  }
  return Math.round(chars / 4);
}

const MAX_PROMPT_TOKENS = 18000;

const tokenLimitMiddleware: LanguageModelMiddleware = {
  transformParams: async ({ params }: { params: LanguageModelV4CallOptions }) => {
    const prompt = params.prompt;
    if (estimatePromptTokens(prompt as any) <= MAX_PROMPT_TOKENS) return params;

    const sysIdx = prompt.findIndex(m => m.role === 'system');
    const kept = sysIdx >= 0 ? [prompt[sysIdx]] : [];

    let tokens = estimatePromptTokens(kept as any);
    for (let i = prompt.length - 1; i >= 0; i--) {
      if (i === sysIdx) continue;
      const msg = prompt[i];
      const t = estimatePromptTokens([msg] as any);

      if (tokens + t > MAX_PROMPT_TOKENS) {
        if (Array.isArray(msg.content)) {
          const stripped = {
            ...msg,
            content: (msg.content as any[]).filter((p: any) => p.type === 'text' || p.type === 'reasoning'),
          } as typeof msg;
          const st = estimatePromptTokens([stripped] as any);
          if (tokens + st <= MAX_PROMPT_TOKENS) {
            kept.splice(sysIdx >= 0 ? 1 : 0, 0, stripped);
            tokens += st;
            continue;
          }
        }
        if (kept.length <= (sysIdx >= 0 ? 1 : 0)) {
          kept.splice(sysIdx >= 0 ? 1 : 0, 0, msg);
        }
        break;
      }

      kept.splice(sysIdx >= 0 ? 1 : 0, 0, msg);
      tokens += t;
    }

    params.prompt = kept;
    return params;
  },
};

const chatModel = provider.chat(config.ai.model);
const model = wrapLanguageModel({
  model: chatModel,
  middleware: tokenLimitMiddleware,
});

let requestChatId = 0;
let requestSendFile: ((content: string, filename: string, caption?: string) => Promise<void>) | null = null;
let requestSendBuffer: ((buffer: Buffer, filename: string, caption?: string) => Promise<void>) | null = null;

const agent = new ToolLoopAgent({
  model,
  instructions: `You are PURU-AI, a helpful Telegram bot assistant with a personal virtual file system stored in Firebase for each user.
You have the following tools available:

1. list_directory — List files and folders in a directory (virtual file system, user-specific)
2. read_file — Read a file's contents (virtual file system, user-specific)
3. write_file — Create or overwrite a file (virtual file system, user-specific)
4. edit_file — Find and replace text in a file (virtual file system, user-specific)
5. delete_file — Delete a file (virtual file system, user-specific)
6. move_file — Move or rename a file in the virtual file system
7. send_file — Read a file from the virtual file system and send it directly to the user's Telegram chat
8. soundcloud_search — Search for tracks on SoundCloud
9. soundcloud_downloader — Download a SoundCloud track by URL and send the audio to the user
10. search_web — Search the web using Yahoo search
11. crawl — Crawl a website URL and extract its text content for summarization
12. get_current_time — Get the current date and time for a given timezone
13. calculate_math — Evaluate a mathematical expression

=== USER MEMORY SYSTEM ===
You have a persistent memory file at /memory/MEMORY.md in the user's virtual file system.
- At the start of each conversation, read /memory/MEMORY.md to recall information about the user (name, age, hobbies, preferences, etc.).
- When the user tells you personal information about themselves, IMMEDIATELY save it to /memory/MEMORY.md using write_file. Do NOT wait for the user to ask you to save it.
- Only store permanent user information in MEMORY.md. Do NOT store temporary/session data or conversation state there.
- If MEMORY.md is empty or doesn't exist, create it with the information you learn.
- Keep the memory concise and well-organized using Markdown format.

Use the appropriate tools when needed. Be friendly, knowledgeable, and concise.`,
  allowSystemInMessages: true,
  stopWhen: isStepCount(20),
  tools: {
    list_directory: tool({
      description: 'Membaca dan menampilkan daftar file serta folder di dalam direktori yang ditentukan di virtual file system.',
      inputSchema: z.object({
        path: z.string().describe('Jalur direktori yang ingin dilihat (contoh: "project" atau "src/components"). Gunakan string kosong untuk root.'),
      }),
      execute: async ({ path }) => {
        const entries = await vfs.listDirectory(requestChatId, path);
        if (entries.length === 0) return { entries: [], message: 'Directory is empty or does not exist' };
        return { entries };
      },
    }),

    read_file: tool({
      description: 'Membaca isi file teks dari virtual file system berdasarkan path yang ditentukan.',
      inputSchema: z.object({
        path: z.string().describe('Jalur lengkap ke file yang ingin dibaca (contoh: "project/src/index.js").'),
      }),
      execute: async ({ path }) => {
        const content = await vfs.readFile(requestChatId, path);
        if (content === null) return { error: 'File not found', content: null };
        return { content, path };
      },
    }),

    write_file: tool({
      description: 'Membuat file baru atau menulis ulang isi file teks dengan konten yang diberikan di virtual file system.',
      inputSchema: z.object({
        path: z.string().describe('Jalur file yang akan dibuat/ditulis (contoh: "project/src/index.js").'),
        content: z.string().describe('Isi teks yang ingin dimasukkan ke dalam file.'),
      }),
      execute: async ({ path, content }) => {
        await vfs.writeFile(requestChatId, path, content);
        return { success: true, path };
      },
    }),

    edit_file: tool({
      description: 'Mengubah bagian teks tertentu di dalam file dengan teks baru (mekanisme find and replace) di virtual file system.',
      inputSchema: z.object({
        path: z.string().describe('Jalur lengkap ke file yang ingin diedit.'),
        old_string: z.string().describe('Teks lama di dalam file yang ingin diganti. Harus sama persis agar ditemukan.'),
        new_string: z.string().describe('Teks baru yang akan menggantikan teks lama.'),
      }),
      execute: async ({ path, old_string, new_string }) => {
        const result = await vfs.editFile(requestChatId, path, old_string, new_string);
        return result;
      },
    }),

    delete_file: tool({
      description: 'Menghapus file secara permanen dari virtual file system.',
      inputSchema: z.object({
        path: z.string().describe('Jalur lengkap ke file yang ingin dihapus.'),
      }),
      execute: async ({ path }) => {
        const deleted = await vfs.deleteFile(requestChatId, path);
        if (!deleted) return { success: false, error: 'File not found' };
        return { success: true };
      },
    }),

    move_file: tool({
      description: 'Memindahkan atau mengganti nama file di virtual file system dari satu lokasi ke lokasi lain.',
      inputSchema: z.object({
        source: z.string().describe('Path sumber file yang ingin dipindahkan.'),
        destination: z.string().describe('Path tujuan baru untuk file tersebut.'),
      }),
      execute: async ({ source, destination }) => {
        return await vfs.moveFile(requestChatId, source, destination);
      },
    }),

    send_file: tool({
      description: 'Membaca file dari virtual file system dan mengirimkan file tersebut langsung ke chat Telegram pengguna.',
      inputSchema: z.object({
        path: z.string().describe('Jalur lengkap ke file di virtual file system yang ingin dikirim ke Telegram.'),
        caption: z.string().optional().describe('Pesan atau deskripsi singkat yang menyertai file.'),
      }),
      execute: async ({ path, caption }) => {
        const content = await vfs.readFile(requestChatId, path);
        if (content === null) return { success: false, error: 'File not found in VFS' };
        if (requestSendFile) {
          const filename = path.split('/').pop() || 'file.txt';
          await requestSendFile(content, filename, caption);
          return { success: true, message: 'File berhasil dikirim ke Telegram' };
        }
        return { success: false, error: 'Cannot send file to chat' };
      },
    }),

    soundcloud_search: tool({
      description: 'Mencari lagu atau track di SoundCloud berdasarkan kata kunci.',
      inputSchema: z.object({
        q: z.string().describe('Kata kunci pencarian (contoh: "lofi", "chill", "jazz").'),
        limit: z.number().optional().describe('Jumlah hasil maksimal (default 5, maksimal 20).'),
      }),
      execute: async ({ q, limit }) => {
        const res = await fetch(`https://puruboy-api.vercel.app/api/search/soundcloud?q=${encodeURIComponent(q)}&limit=${limit || 5}`);
        if (!res.ok) return { error: `API returned status ${res.status}` };
        const data = await res.json();
        return { query: q, results: data };
      },
    }),

    soundcloud_downloader: tool({
      description: 'Mengunduh lagu dari SoundCloud berdasarkan URL dan mengirimkan file audionya langsung ke chat Telegram pengguna.',
      inputSchema: z.object({
        url: z.string().describe('URL SoundCloud yang ingin diunduh (contoh: "https://soundcloud.com/artist/track-name").'),
      }),
      execute: async ({ url }) => {
        const res = await fetch('https://puruboy-api.vercel.app/api/downloader/soundcloud-v2', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ url }),
        });
        if (!res.ok) return { success: false, error: `Download API returned status ${res.status}` };
        const data = await res.json();

        const dlUrl = data?.result?.url;
        if (!dlUrl) return { success: false, error: 'Could not extract download URL from API response', response: data };

        const audioRes = await fetch(dlUrl);
        if (!audioRes.ok) return { success: false, error: `Failed to download audio (${audioRes.status})` };
        const audioBuffer = Buffer.from(await audioRes.arrayBuffer());

        if (requestSendBuffer) {
          const title = data?.title || data?.result?.title || url.split('/').pop() || 'soundcloud';
          const filename = `${title}.mp3`;
          await requestSendBuffer(audioBuffer, filename, title);
          return { success: true, message: `Audio "${title}" berhasil dikirim ke Telegram` };
        }
        return { success: false, error: 'Cannot send audio to chat' };
      },
    }),

    search_web: tool({
      description: 'Mencari informasi di web menggunakan Yahoo Search. Gunakan untuk mencari berita, artikel, atau informasi terkini.',
      inputSchema: z.object({
        q: z.string().describe('Kata kunci pencarian (contoh: "berita terkini", "cara membuat website").'),
      }),
      execute: async ({ q }) => {
        const MAX_RETRIES = 5;
        let lastError: string | undefined;

        for (let attempt = 1; attempt <= MAX_RETRIES; attempt++) {
          try {
            const res = await fetch(`https://puruboy-api.vercel.app/api/search/yahoo?q=${encodeURIComponent(q)}`);
            if (!res.ok) throw new Error(`HTTP ${res.status}`);
            const data = await res.json();
            return { query: q, results: data?.result || [] };
          } catch (err) {
            lastError = err instanceof Error ? err.message : String(err);
          }
          if (attempt < MAX_RETRIES) {
            const backoff = Math.min(1000 * Math.pow(2, attempt - 1), 30000);
            await delay(backoff);
          }
        }
        return { query: q, error: `Search failed after ${MAX_RETRIES} attempts: ${lastError}`, results: [] };
      },
    }),

    crawl: tool({
      description: 'Mengunjungi dan mengambil isi teks dari sebuah halaman website. Hasilnya akan otomatis diringkas oleh AI.',
      inputSchema: z.object({
        url: z.string().describe('URL website yang ingin di-crawl (contoh: "https://example.com/article").'),
      }),
      execute: async ({ url }) => {
        try {
          const res = await fetch(url, { signal: AbortSignal.timeout(15000) });
          if (!res.ok) return { error: `HTTP ${res.status}`, url };
          const html = await res.text();

          const text = html
            .replace(/<script[^>]*>[\s\S]*?<\/script>/gi, '')
            .replace(/<style[^>]*>[\s\S]*?<\/style>/gi, '')
            .replace(/<nav[^>]*>[\s\S]*?<\/nav>/gi, '')
            .replace(/<header[^>]*>[\s\S]*?<\/header>/gi, '')
            .replace(/<footer[^>]*>[\s\S]*?<\/footer>/gi, '')
            .replace(/<[^>]+>/g, ' ')
            .replace(/&amp;/g, '&')
            .replace(/&lt;/g, '<')
            .replace(/&gt;/g, '>')
            .replace(/&quot;/g, '"')
            .replace(/&#x27;/g, "'")
            .replace(/&#x2F;/g, '/')
            .replace(/&#xD;/g, '')
            .replace(/\s+/g, ' ')
            .trim();

          const maxLength = 8000;
          const truncated = text.length > maxLength ? text.slice(0, maxLength) + '\n\n[Content truncated...]' : text;
          return { url, content: truncated };
        } catch (err) {
          const msg = err instanceof Error ? err.message : String(err);
          return { error: `Failed to crawl: ${msg}`, url };
        }
      },
    }),

    get_current_time: tool({
      description: 'Mendapatkan informasi tanggal dan waktu saat ini berdasarkan zona waktu tertentu.',
      inputSchema: z.object({
        zone: z.string().describe('Kode identifier zona waktu IANA (contoh: "Asia/Jakarta", "Asia/Makassar", "UTC").'),
      }),
      execute: async ({ zone }) => {
        const now = new Date();
        const options: Intl.DateTimeFormatOptions = {
          dateStyle: 'full',
          timeStyle: 'long',
          timeZone: zone,
        };
        const formatted = new Intl.DateTimeFormat('id-ID', options).format(now);
        return { dateTime: formatted, timezone: zone };
      },
    }),

    calculate_math: tool({
      description: 'Mengevaluasi ekspresi matematika yang rumit untuk menghindari kesalahan hitung manual oleh LLM.',
      inputSchema: z.object({
        expression: z.string().describe('Rumus matematika yang ingin dihitung (contoh: "sqrt(144) * (25 + 5)").'),
      }),
      execute: async ({ expression }) => {
        try {
          const result = Function(`"use strict"; return (${expression})`)();
          return { expression, result: String(result) };
        } catch {
          return { expression, error: 'Ekspresi matematika tidak valid' };
        }
      },
    }),
  },
});

async function delay(ms: number) {
  return new Promise(resolve => setTimeout(resolve, ms));
}

export interface ProcessMessageOptions {
  chatId: number;
  sendFile?: (content: string, filename: string, caption?: string) => Promise<void>;
  sendBuffer?: (buffer: Buffer, filename: string, caption?: string) => Promise<void>;
}

export async function processMessage(
  userMessage: string,
  history: ModelMessage[],
  options: ProcessMessageOptions,
): Promise<{ text: string; responseMessages: ModelMessage[] }> {
  const MAX_RETRIES = 8;
  let lastError: Error | undefined;

  requestChatId = options.chatId;
  requestSendFile = options.sendFile || null;
  requestSendBuffer = options.sendBuffer || null;

  const memoryContent = await vfs.readFile(requestChatId, 'memory/MEMORY.md');

  try {
    for (let attempt = 1; attempt <= MAX_RETRIES; attempt++) {
      try {
        const result = await agent.stream({
          messages: [
            ...(memoryContent ? [{ role: 'system' as const, content: `[USER MEMORY]\n${memoryContent}\n[/USER MEMORY]` }] : []),
            ...history,
            { role: 'user', content: userMessage },
          ],
        });

        const [text, responseMessages] = await Promise.all([
          result.text,
          result.responseMessages,
        ]);

        if (text) {
          return { text, responseMessages: responseMessages as ModelMessage[] };
        }

        lastError = new Error('Empty response from AI');
      } catch (err) {
        lastError = err instanceof Error ? err : new Error(String(err));
      }

      if (attempt < MAX_RETRIES) {
        const backoff = Math.min(3000 * Math.pow(2, attempt - 1) + Math.random() * 1000, 45000);
        console.warn(`API attempt ${attempt}/${MAX_RETRIES} failed, retrying in ${Math.round(backoff)}ms:`, lastError.message);
        await delay(backoff);
      }
    }

    console.error(`API failed after ${MAX_RETRIES} retries:`, lastError);
    return {
      text: 'Maaf, saya tidak bisa merespons saat ini.',
      responseMessages: [],
    };
  } finally {
    requestChatId = 0;
    requestSendFile = null;
    requestSendBuffer = null;
  }
}
