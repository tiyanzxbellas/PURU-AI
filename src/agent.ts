import { ToolLoopAgent, tool, isStepCount, type ModelMessage } from 'ai';
import { createOpenAI } from '@ai-sdk/openai';
import { config } from './config.js';
import { z } from 'zod';
import * as vfs from './vfs.js';
import * as e2b from './e2b.js';
import { getSystemPrompt } from './instruction.js';

const provider = createOpenAI({
  baseURL: config.ai.baseURL,
  apiKey: config.ai.apiKey,
  name: 'puru',
});

const model = provider.chat(config.ai.model);

let requestChatId = 0;
let requestSendFile: ((content: string, filename: string, caption?: string) => Promise<void>) | null = null;
let requestSendBuffer: ((buffer: Buffer, filename: string, caption?: string) => Promise<void>) | null = null;

const agent = new ToolLoopAgent({
  model,
  temperature: config.temperature,
  allowSystemInMessages: true,
  stopWhen: isStepCount(config.maxLoop),
  tools: {
    list_directory: tool({
      description: 'Membaca dan menampilkan daftar file serta folder di dalam direktori yang ditentukan di virtual file system.',
      inputSchema: z.object({
        path: z.string().describe('Jalur direktori yang ingin dilihat (contoh: "project" atau "src/components"). Gunakan string kosong untuk root.'),
      }),
      execute: async ({ path }) => withTimeout((async () => {
        const entries = await vfs.listDirectory(requestChatId, path);
        if (entries.length === 0) return { entries: [], message: 'Directory is empty or does not exist' };
        return { entries };
      })(), TOOL_TIMEOUT),
    }),

    read_file: tool({
      description: 'Membaca isi file teks dari virtual file system berdasarkan path yang ditentukan.',
      inputSchema: z.object({
        path: z.string().describe('Jalur lengkap ke file yang ingin dibaca (contoh: "project/src/index.js").'),
      }),
      execute: async ({ path }) => withTimeout((async () => {
        const content = await vfs.readFile(requestChatId, path);
        if (content === null) return { error: 'File not found', content: null };
        return { content, path };
      })(), TOOL_TIMEOUT),
    }),

    write_file: tool({
      description: 'Membuat file baru atau menulis ulang isi file teks dengan konten yang diberikan di virtual file system.',
      inputSchema: z.object({
        path: z.string().describe('Jalur file yang akan dibuat/ditulis (contoh: "project/src/index.js").'),
        content: z.string().describe('Isi teks yang ingin dimasukkan ke dalam file.'),
      }),
      execute: async ({ path, content }) => withTimeout((async () => {
        await vfs.writeFile(requestChatId, path, content);
        return { success: true, path };
      })(), TOOL_TIMEOUT),
    }),

    edit_file: tool({
      description: 'Mengubah bagian teks tertentu di dalam file dengan teks baru (mekanisme find and replace) di virtual file system.',
      inputSchema: z.object({
        path: z.string().describe('Jalur lengkap ke file yang ingin diedit.'),
        old_string: z.string().describe('Teks lama di dalam file yang ingin diganti. Harus sama persis agar ditemukan.'),
        new_string: z.string().describe('Teks baru yang akan menggantikan teks lama.'),
      }),
      execute: async ({ path, old_string, new_string }) => withTimeout((async () => {
        const result = await vfs.editFile(requestChatId, path, old_string, new_string);
        return result;
      })(), TOOL_TIMEOUT),
    }),

    delete_file: tool({
      description: 'Menghapus file secara permanen dari virtual file system.',
      inputSchema: z.object({
        path: z.string().describe('Jalur lengkap ke file yang ingin dihapus.'),
      }),
      execute: async ({ path }) => withTimeout((async () => {
        const deleted = await vfs.deleteFile(requestChatId, path);
        if (!deleted) return { success: false, error: 'File not found' };
        return { success: true };
      })(), TOOL_TIMEOUT),
    }),

    move_file: tool({
      description: 'Memindahkan atau mengganti nama file di virtual file system dari satu lokasi ke lokasi lain.',
      inputSchema: z.object({
        source: z.string().describe('Path sumber file yang ingin dipindahkan.'),
        destination: z.string().describe('Path tujuan baru untuk file tersebut.'),
      }),
      execute: async ({ source, destination }) => withTimeout(
        vfs.moveFile(requestChatId, source, destination), TOOL_TIMEOUT
      ),
    }),

    send_file: tool({
      description: 'Membaca file dari virtual file system dan mengirimkan file tersebut langsung ke chat Telegram pengguna.',
      inputSchema: z.object({
        path: z.string().describe('Jalur lengkap ke file di virtual file system yang ingin dikirim ke Telegram.'),
        caption: z.string().optional().describe('Pesan atau deskripsi singkat yang menyertai file.'),
      }),
      execute: async ({ path, caption }) => withTimeout((async () => {
        const content = await vfs.readFile(requestChatId, path);
        if (content === null) return { success: false, error: 'File not found in VFS' };
        if (requestSendFile) {
          const filename = path.split('/').pop() || 'file.txt';
          await requestSendFile(content, filename, caption);
          return { success: true, message: 'File berhasil dikirim ke Telegram' };
        }
        return { success: false, error: 'Cannot send file to chat' };
      })(), TOOL_TIMEOUT),
    }),


    search_web: tool({
      description: 'Mencari informasi di web menggunakan Yahoo Search. Gunakan untuk mencari berita, artikel, atau informasi terkini.',
      inputSchema: z.object({
        q: z.string().describe('Kata kunci pencarian (contoh: "berita terkini", "cara membuat website").'),
      }),
      execute: async ({ q }) => withTimeout((async () => {
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
      })(), TOOL_TIMEOUT),
    }),

    crawl: tool({
      description: 'Mengunjungi URL website dan menjalankan kode cheerio untuk mengekstrak data. Kamu WAJIB menulis kode cheerio menggunakan $ sebagai selector. Contoh: $("h1").text()',
      inputSchema: z.object({
        url: z.string().describe('URL website yang ingin di-crawl (contoh: "https://example.com/article").'),
        code: z.string().describe('Kode JavaScript cheerio untuk mengekstrak data dari halaman. Gunakan $ sebagai root cheerio instance. Contoh: $("h1").text()'),
      }),
      execute: async ({ url, code }) => withTimeout((async () => {
        try {
          const res = await fetch(url, { signal: AbortSignal.timeout(15000) });
          if (!res.ok) return { error: `HTTP ${res.status}`, url };
          const html = await res.text();

          const { load } = await import('cheerio');
          const $ = load(html);

          try {
            const result = Function('$', `"use strict"; return (${code})`)($);
            return { url, result: result != null ? String(result) : 'null' };
          } catch (codeError) {
            return {
              error: 'Syntax error dalam kode cheerio',
              syntaxError: codeError instanceof Error ? codeError.message : String(codeError),
              url,
            };
          }
        } catch (err) {
          const msg = err instanceof Error ? err.message : String(err);
          return { error: `Failed to crawl: ${msg}`, url };
        }
      })(), TOOL_TIMEOUT),
    }),

    get_current_time: tool({
      description: 'Mendapatkan informasi tanggal dan waktu saat ini berdasarkan zona waktu tertentu.',
      inputSchema: z.object({
        zone: z.string().describe('Kode identifier zona waktu IANA (contoh: "Asia/Jakarta", "Asia/Makassar", "UTC").'),
      }),
      execute: async ({ zone }) => withTimeout((async () => {
        const now = new Date();
        const options: Intl.DateTimeFormatOptions = {
          dateStyle: 'full',
          timeStyle: 'long',
          timeZone: zone,
        };
        const formatted = new Intl.DateTimeFormat('id-ID', options).format(now);
        return { dateTime: formatted, timezone: zone };
      })(), TOOL_TIMEOUT),
    }),

    calculate_math: tool({
      description: 'Mengevaluasi ekspresi matematika yang rumit untuk menghindari kesalahan hitung manual oleh LLM.',
      inputSchema: z.object({
        expression: z.string().describe('Rumus matematika yang ingin dihitung (contoh: "sqrt(144) * (25 + 5)").'),
      }),
      execute: async ({ expression }) => withTimeout((async () => {
        try {
          const result = Function(`"use strict"; return (${expression})`)();
          return { expression, result: String(result) };
        } catch {
          return { expression, error: 'Ekspresi matematika tidak valid' };
        }
      })(), TOOL_TIMEOUT),
    }),

    e2b_sandbox_create: tool({
      description: 'Membuat dan menginisialisasi instans sandbox cloud E2B baru yang terisolasi dengan akses Linux dan internet. Setiap chat hanya bisa memiliki satu sandbox aktif.',
      inputSchema: z.object({}),
      execute: async () => withTimeout(
        e2b.createSandbox(requestChatId), TOOL_TIMEOUT
      ),
    }),

    e2b_run_code: tool({
      description: 'Membaca file kode dari virtual file system (VFS) lalu mengeksekusinya di dalam sandbox E2B. Setelah kode dijalankan, hasil output (stdout/stderr) akan dikembalikan.',
      inputSchema: z.object({
        path: z.string().describe('Jalur lengkap file kode di VFS yang ingin dijalankan (contoh: "scripts/analisis.py").'),
        language: z.enum(['python', 'javascript']).optional().describe('Bahasa pemrograman (default: python).'),
      }),
      execute: async ({ path, language }) => withTimeout((async () => {
        const code = await vfs.readFile(requestChatId, path);
        if (code === null) return { error: 'File tidak ditemukan di VFS', path };
        return await e2b.runCodeInSandbox(requestChatId, code, language || 'python');
      })(), TOOL_TIMEOUT),
    }),

    e2b_install_package: tool({
      description: 'Memasang package ke dalam sandbox E2B. Mendukung pip (Python) dan npm (Node.js).',
      inputSchema: z.object({
        package_name: z.string().describe('Nama package yang ingin diinstal (contoh: "pandas", "matplotlib", "axios", "express").'),
        manager: z.enum(['pip', 'npm']).optional().describe('Package manager: "pip" untuk Python (default), "npm" untuk Node.js.'),
      }),
      execute: async ({ package_name, manager }) => withTimeout(
        e2b.installPackageInSandbox(requestChatId, package_name, manager || 'pip'), TOOL_TIMEOUT
      ),
    }),

    e2b_send_file: tool({
      description: 'Mengambil file hasil pemrosesan dari sandbox E2B lalu mengirimkannya langsung ke chat Telegram pengguna.',
      inputSchema: z.object({
        path: z.string().describe('Jalur file di dalam sandbox E2B yang ingin dikirim (contoh: "/tmp/chart.png" atau "/home/user/output.csv").'),
        caption: z.string().optional().describe('Deskripsi atau keterangan singkat yang menyertai file saat dikirim ke Telegram.'),
      }),
      execute: async ({ path, caption }) => withTimeout((async () => {
        const { content, error } = await e2b.readFileFromSandbox(requestChatId, path);
        if (error) return { success: false, error };
        if (!content || content.length === 0) return { success: false, error: 'File kosong atau tidak ditemukan' };

        if (requestSendBuffer) {
          const filename = path.split('/').pop() || 'sandbox_file';
          await requestSendBuffer(content, filename, caption);
          return { success: true, message: 'File berhasil dikirim ke Telegram' };
        }
        return { success: false, error: 'Tidak dapat mengirim file ke chat' };
      })(), TOOL_TIMEOUT),
    }),

    e2b_sandbox_kill: tool({
      description: 'Menutup dan menghapus instans sandbox E2B secara permanen untuk membersihkan resource setelah semua proses eksekusi selesai.',
      inputSchema: z.object({}),
      execute: async () => withTimeout(
        Promise.resolve(e2b.killSandbox(requestChatId)), TOOL_TIMEOUT
      ),
    }),

    create_skill: tool({
      description: 'Membuat skill baru di /skills/ virtual file system dengan workflow berupa langkah-langkah (steps).',
      inputSchema: z.object({
        name: z.string().describe('Nama skill. Hanya boleh berisi huruf, angka, hypen, dan underscore (contoh: "soundcloud-downloader").'),
        steps: z.array(z.object({
          title: z.string().describe('Judul langkah (contoh: "Install library").'),
          instruction: z.string().describe('Instruksi detail untuk langkah ini.'),
        })).describe('Array langkah-langkah dalam workflow skill.'),
      }),
      execute: async ({ name, steps }) => withTimeout((async () => {
        if (!/^[a-zA-Z0-9_-]+$/.test(name)) {
          return { success: false, error: 'Nama skill hanya boleh berisi huruf, angka, hypen, dan underscore.' };
        }
        const fileContent = `# ${name}\n\n## Steps\n\n${steps.map((s, i) => `### Langkah ${i + 1}: ${s.title}\n${s.instruction}`).join('\n\n')}\n`;
        await vfs.writeFile(requestChatId, `skills/${name}.md`, fileContent);
        return { success: true, path: `skills/${name}.md` };
      })(), TOOL_TIMEOUT),
    }),

    use_skills: tool({
      description: 'Menggunakan skill dari /skills/ di virtual file system.',
      inputSchema: z.object({
        name: z.string().describe('Nama skill yang ingin digunakan (tanpa ekstensi .md).'),
      }),
      execute: async ({ name }) => withTimeout((async () => {
        const content = await vfs.readFile(requestChatId, `skills/${name}.md`);
        if (content === null) return { error: 'Skill tidak ditemukan', content: null };
        return { name, content };
      })(), TOOL_TIMEOUT),
    }),

    delete_skill: tool({
      description: 'Menghapus file skill dari /skills/ di virtual file system.',
      inputSchema: z.object({
        name: z.string().describe('Nama skill yang ingin dihapus (tanpa ekstensi .md).'),
      }),
      execute: async ({ name }) => withTimeout((async () => {
        const deleted = await vfs.deleteFile(requestChatId, `skills/${name}.md`);
        if (!deleted) return { success: false, error: 'Skill tidak ditemukan' };
        return { success: true };
      })(), TOOL_TIMEOUT),
    }),
  },
});

const TOOL_TIMEOUT = 120_000;

function withTimeout<T>(promise: Promise<T>, ms: number): Promise<T> {
  return Promise.race([
    promise,
    new Promise<never>((_, reject) =>
      setTimeout(() => reject(new Error(`Timeout after ${ms}ms`)), ms)
    ),
  ]);
}

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
): Promise<{ text: string; responseMessages: ModelMessage[]; totalTokens: number; lastStepUsage: { inputTokens: number; outputTokens: number; totalTokens: number } }> {
  const MAX_RETRIES = 8;
  let lastError: Error | undefined;

  requestChatId = options.chatId;
  requestSendFile = options.sendFile || null;
  requestSendBuffer = options.sendBuffer || null;

  const memoryContent = await vfs.readFile(requestChatId, 'memory/MEMORY.md');

  const skillEntries = await vfs.listDirectory(requestChatId, 'skills');
  const skillNames = skillEntries
    .filter((e: any) => e.name && e.name.endsWith('.md'))
    .map((e: any) => e.name.replace(/\.md$/, ''));
  const skillsBlock = skillNames.length > 0
    ? skillNames.map((n: string) => `- ${n}`).join('\n')
    : '';

  const systemPrompt = await getSystemPrompt(
    memoryContent || undefined,
    skillsBlock || undefined
  );

  try {
    while (history.length > 0) {
      const firstNonSystem = history.findIndex(m => m.role !== 'system');
      if (firstNonSystem >= 0 && history[firstNonSystem].role !== 'user') {
        history.splice(firstNonSystem, 1);
      } else break;
    }

    for (let attempt = 1; attempt <= MAX_RETRIES; attempt++) {
      try {
        const result = await agent.stream({
          messages: [
            { role: 'system' as const, content: systemPrompt },
            ...history,
            { role: 'user', content: userMessage },
          ],
        });

        const [text, responseMessages, usage] = await Promise.all([
          result.text,
          result.responseMessages,
          result.usage,
        ]);

        const steps = await result.steps;
        const lastStep = steps?.[steps.length - 1];
        const lastStepUsage = {
          inputTokens: lastStep?.usage?.inputTokens ?? 0,
          outputTokens: lastStep?.usage?.outputTokens ?? 0,
          totalTokens: lastStep?.usage?.totalTokens ?? 0,
        };

        const lastStepToolCalls = lastStep?.toolCalls;
        const lastStepHasToolCalls = lastStepToolCalls && lastStepToolCalls.length > 0;

        const stepCount = steps?.length ?? 0;
        if (stepCount >= 20) {
          return {
            text: '⚠️ Percakapan mencapai batas maksimum langkah. Silakan kirim `lanjut` atau `/ai lanjut` untuk melanjutkan percakapan dengan AI, atau masukkan prompt baru.',
            responseMessages: responseMessages as ModelMessage[],
            totalTokens: usage?.totalTokens ?? 0,
            lastStepUsage,
          };
        }

        if (text || lastStepHasToolCalls) {
          return { text, responseMessages: responseMessages as ModelMessage[], totalTokens: usage?.totalTokens ?? 0, lastStepUsage };
        }

        lastError = new Error('Empty response from AI');
      } catch (err) {
        lastError = err instanceof Error ? err : new Error(String(err));
      }

      if (attempt < MAX_RETRIES) {
        console.warn(`API attempt ${attempt}/${MAX_RETRIES} failed, retrying immediately:`, lastError.message);
      }
    }

    console.error(`API failed after ${MAX_RETRIES} retries:`, lastError);
    return {
      text: 'Maaf, saya tidak bisa merespons saat ini.',
      responseMessages: [],
      totalTokens: 0,
      lastStepUsage: { inputTokens: 0, outputTokens: 0, totalTokens: 0 },
    };
  } finally {
    requestChatId = 0;
    requestSendFile = null;
    requestSendBuffer = null;
  }
}


