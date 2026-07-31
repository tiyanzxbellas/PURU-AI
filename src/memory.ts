import { streamText, type ModelMessage } from 'ai';
import { createOpenAI } from '@ai-sdk/openai';
import { config } from './config.js';
import * as vfs from './vfs.js';

const provider = createOpenAI({
  baseURL: config.ai.baseURL,
  apiKey: config.ai.apiKey,
  name: 'puru',
});

const model = provider.chat(config.ai.model);

const MEMORY_MAX_OUTPUT = 2000;

const MEMORY_PROMPT = `Kamu adalah pengelola memori untuk asisten AI "PURU-AI".

Baca MEMORY.md lama (jika ada) dan percakapan terbaru, lalu hasilkan MEMORY.md baru yang ringkas. Aturan:
- Simpan fakta penting tentang user, keputusan, task yang sedang berjalan, preferensi, dan thread yang belum selesai.
- Buang informasi yang sudah basi atau tidak relevan.
- Pertahankan informasi lama yang masih relevan (jangan sampai hilang).
- Output HANYA isi MEMORY.md (markdown, maksimal ${MEMORY_MAX_OUTPUT} karakter), tanpa judul atau pembuka lain.`;

function messageToText(msg: ModelMessage): string {
  const content = msg.content as any;
  if (typeof content === 'string') return content;
  if (Array.isArray(content)) {
    return content
      .map((part: any) => (typeof part === 'string' ? part : part && typeof part.text === 'string' ? part.text : ''))
      .filter(Boolean)
      .join('\n');
  }
  return '';
}

export async function updateMemory(chatId: number, recentMessages: ModelMessage[]): Promise<string | null> {
  try {
    const current = await vfs.readFile(chatId, 'memory/MEMORY.md');

    const historyText = recentMessages
      .slice(-12)
      .map((m) => {
        const role = m.role === 'assistant' ? 'AI' : m.role;
        const text = messageToText(m).slice(0, 2000);
        return text ? `${role}: ${text}` : '';
      })
      .filter(Boolean)
      .join('\n');

    let streamError: unknown = null;
    const result = streamText({
      model,
      system: MEMORY_PROMPT,
      prompt: `MEMORY.md lama:\n${current || '(kosong)'}\n\nPercakapan terbaru:\n${historyText || '(kosong)'}`,
      maxOutputTokens: MEMORY_MAX_OUTPUT,
      onError: ({ error }) => { streamError = error; },
    });
    const text = await result.text;
    if (streamError) throw streamError instanceof Error ? streamError : new Error(String(streamError));

    const trimmed = text.trim();
    if (!trimmed) return null;

    await vfs.writeFile(chatId, 'memory/MEMORY.md', trimmed);
    return trimmed;
  } catch (err) {
    console.warn('Memory update failed:', err);
    return null;
  }
}
