import { Bot, InputFile, GrammyError, type Context } from 'grammy';
import { type ModelMessage, pruneMessages } from 'ai';
import { getEncoding } from 'js-tiktoken';
import { config } from './config.js';
import { processMessage } from './agent.js';
import * as vfs from './vfs.js';
import { getHistory, setHistory, deleteHistory, getTokens, setTokens, getMeta, setMeta } from './history.js';
import { updateMemory } from './memory.js';
import { listSkills, loadSkill, listSkillFiles, deleteSkill as deleteSkillLoader } from './skills-loader.js';
import { installFromGitHub, migrateOldSkills } from './skills-registry.js';

const encoder = getEncoding('o200k_base');

const MAX_MESSAGE_LENGTH = 4096;

const MENU_TEXT =
  '*PURU-AI*\n\n' +
  'CMD: /start\nDESC: "Memulai bot"\n\n' +
  'CMD: /menu\nDESC: "Menampilkan menu ini"\n\n' +
  'CMD: /clear\nDESC: "Menghapus riwayat percakapan"\n\n' +
  'CMD: /token\nDESC: "Melihat penggunaan token"\n\n' +
  'CMD: /info\nDESC: "Melihat info (memory/user/soul)"\n\n' +
  'CMD: /reset\nDESC: "Reset semua data (riwayat & file)"\n\n' +
  'CMD: /ai <pesan>\nDESC: "Mengobrol dengan AI (khusus grup)"\n\n' +
  '---SKILLS-MENU---\n\n' +
  'CMD: /skills\nDESC: "Melihat daftar skill"\n\n' +
  'CMD: /skills search <query>\nDESC: "Mencari skill dari GitHub"\n\n' +
  'CMD: /skills install <url>\nDESC: "Install skill dari GitHub"\n\n' +
  'CMD: /skills info <nama>\nDESC: "Info detail skill"\n\n' +
  'CMD: /skills read <nama>\nDESC: "Membaca isi skill"\n\n' +
  'CMD: /skills delete <nama>\nDESC: "Menghapus skill"\n\n' +
  'CMD: /skills migrate\nDESC: "Migrate skill lama ke format baru"\n\n' +
  'Di chat pribadi, kirim pesan langsung untuk mengobrol dengan AI.\nDi grup, gunakan /ai diikuti pesan Anda.';

async function safeReply(ctx: Context, text: string, extra?: Record<string, any>) {
  try {
    await ctx.reply(text, { ...extra, parse_mode: 'Markdown' });
  } catch (err) {
    if (err instanceof GrammyError && err.error_code === 400 && err.description?.includes('parse entities')) {
      await ctx.reply(text, { ...extra, parse_mode: undefined });
    } else {
      throw err;
    }
  }
}

async function safeEdit(ctx: Context, chatId: number, messageId: number, text: string, extra?: Record<string, any>) {
  if (text.length > MAX_MESSAGE_LENGTH) {
    // Edit placeholder with a note
    const note = '⚠️ Respon terlalu panjang, dikirim sebagai file:';
    try {
      await ctx.api.editMessageText(chatId, messageId, note, { ...extra, parse_mode: 'Markdown' });
    } catch (err) {
      if (err instanceof GrammyError && err.error_code === 400 && err.description?.includes('parse entities')) {
        await ctx.api.editMessageText(chatId, messageId, note, { ...extra, parse_mode: undefined });
      } else {
        throw err;
      }
    }
    // Send the full response as a file
    await ctx.replyWithDocument(
      new InputFile(Buffer.from(text, 'utf-8'), 'respon.md'),
      { caption: 'Respon lengkap terlalu panjang untuk ditampilkan di chat.' }
    );
    return;
  }

  try {
    await ctx.api.editMessageText(chatId, messageId, text, { ...extra, parse_mode: 'Markdown' });
  } catch (err) {
    if (err instanceof GrammyError && err.error_code === 400 && err.description?.includes('parse entities')) {
      await ctx.api.editMessageText(chatId, messageId, text, { ...extra, parse_mode: undefined });
    } else {
      throw err;
    }
  }
}

// Guarantee the first non-system message is a user message (avoids API error 400).
// Leading system messages are preserved.
function ensureStartsWithUser(msgs: ModelMessage[]): ModelMessage[] {
  const result = [...msgs];
  let i = 0;
  while (i < result.length && result[i].role === 'system') i++;
  while (i < result.length && result[i].role !== 'user') {
    result.splice(i, 1);
  }
  return result;
}

// Count tokens for conversation context: only user & assistant text
// (system, tool results, and tool-call args are NOT counted)
function countConvTokens(msgs: ModelMessage[]): number {
  let count = 0;
  for (const msg of msgs) {
    if (msg.role !== 'user' && msg.role !== 'assistant') continue;
    const content = msg.content as any;
    if (typeof content === 'string') {
      count += encoder.encode(content).length;
    } else if (Array.isArray(content)) {
      for (const part of content) {
        if (typeof part === 'string') {
          count += encoder.encode(part).length;
        } else if (part && typeof part === 'object' && part.type === 'text' && typeof part.text === 'string') {
          count += encoder.encode(part.text).length;
        }
      }
    }
  }
  return count;
}

// Prune history to remove noise (reasoning, old tool-calls)
function pruneHistory(history: ModelMessage[]): ModelMessage[] {
  return pruneMessages({
    messages: history,
    reasoning: 'before-last-message',
    toolCalls: 'before-last-6-messages',
    emptyMessages: 'remove',
  });
}

const MAX_USER_MESSAGES = 5;

const MAX_UPLOAD_SIZE = 10 * 1024 * 1024;
const MAX_STORED_CONTENT = 8000;

function truncateString(s: string, max = MAX_STORED_CONTENT): string {
  return s.length > max ? s.slice(0, max) + '\n...[truncated]' : s;
}

function truncateArgs(args: unknown): unknown {
  if (typeof args === 'string') return truncateString(args);
  if (args && typeof args === 'object') {
    try {
      const json = JSON.stringify(args);
      if (json.length <= MAX_STORED_CONTENT) return args;
      return truncateString(json);
    } catch {
      return args;
    }
  }
  return args;
}

// Batasi isi history sebelum disimpan agar tool output besar tidak menumpuk
// di memori (LRU cache) maupun Firebase RTDB.
function sanitizeHistoryMessages(messages: ModelMessage[]): ModelMessage[] {
  return messages.map((msg) => {
    let out: ModelMessage = msg;
    const content = msg.content as any;

    if (typeof content === 'string') {
      out = { ...out, content: truncateString(content) } as ModelMessage;
    } else if (Array.isArray(content)) {
      out = {
        ...out,
        content: content.map((part: any) => {
          if (typeof part === 'string') return truncateString(part);
          if (part && typeof part === 'object' && 'text' in part && typeof part.text === 'string') {
            return { ...part, text: truncateString(part.text) };
          }
          return part;
        }),
      } as ModelMessage;
    }

    if (Array.isArray((msg as any).toolCalls)) {
      out = {
        ...out,
        toolCalls: (msg as any).toolCalls.map((tc: any) => ({
          ...tc,
          args: truncateArgs(tc.args),
        })),
      } as any;
    }

    return out;
  });
}

// Keep at most MAX_USER_MESSAGES user turns sent to the model. The incoming
// user message is appended at request time, so stored history keeps at most
// MAX_USER_MESSAGES - 1 turns. The oldest user turn is removed together with
// its assistant/tool responses, so the next user message lands right after the
// system messages and the remaining order is preserved.
function capUserTurns(history: ModelMessage[]): ModelMessage[] {
  const result = [...history];
  const countUser = (msgs: ModelMessage[]) => msgs.filter(m => m.role === 'user').length;

  while (countUser(result) >= MAX_USER_MESSAGES) {
    const first = result.findIndex(m => m.role === 'user');
    const next = result.findIndex((m, i) => i > first && m.role === 'user');
    if (first < 0 || next < 0) break;
    result.splice(first, next - first);
  }

  return ensureStartsWithUser(result);
}

const INVALID_COMMAND_TEXT =
  '❌ Perintah tidak dikenal. Gunakan /menu untuk melihat daftar perintah yang tersedia.';

// Increment counter turn user lalu pemicu update MEMORY.md otomatis setiap
// MEMORY_UPDATE_EVERY pesan. Dijalankan SETELAH reply terkirim agar user tidak
// menunggu; error tidak menggagalkan alur chat.
async function maybeUpdateMemory(userId: number, history: ModelMessage[]) {
  try {
    const meta = await getMeta(userId);
    const userTurns = (meta?.userTurns ?? 0) + 1;
    await setMeta(userId, { userTurns });

    if (userTurns % config.memoryUpdateEvery !== 0) return;
    const updated = await updateMemory(userId, history);
    if (updated) {
      console.log(`Memory updated for user ${userId} (turn ${userTurns})`);
    }
  } catch (err) {
    console.warn('Memory update failed:', err);
  }
}

export function createBot() {
  const bot = new Bot(config.telegramBotToken);

  bot.command('start', (ctx: Context) => {
    safeReply(
      ctx,
      'Halo! Saya PURU-AI 🤖\n\n' +
      'Saya bisa membantu Anda dengan:\n' +
      '• Informasi waktu saat ini\n' +
      '• Informasi cuaca (simulasi)\n' +
      '• Perhitungan matematika\n' +
      '• Tanya jawab umum\n\n' +
      'Silakan kirim pesan!',
      { reply_to_message_id: ctx.msg?.message_id }
    );
  });

  bot.command('menu', (ctx: Context) => {
    safeReply(ctx, MENU_TEXT, { reply_to_message_id: ctx.msg?.message_id });
  });

  bot.command('clear', async (ctx: Context) => {
    const userId = ctx.from!.id;
    await deleteHistory(userId);
    safeReply(ctx, 'Riwayat percakapan telah dihapus!', { reply_to_message_id: ctx.msg?.message_id });
  });

  bot.command('reset', async (ctx: Context) => {
    const userId = ctx.from!.id;
    await deleteHistory(userId);
    await vfs.deleteAll(userId);
    safeReply(ctx, '🗑️ Semua data Anda (riwayat percakapan & file VFS) telah dihapus.', { reply_to_message_id: ctx.msg?.message_id });
  });

  bot.command('token', async (ctx: Context) => {
    const userId = ctx.from!.id;
    const lastStep = await getTokens(userId);
    const history = await getHistory(userId);
    if (history.length === 0 && !lastStep) {
      safeReply(ctx, 'Belum ada riwayat percakapan.', { reply_to_message_id: ctx.msg?.message_id });
      return;
    }

    const userCount = history.filter(m => m.role === 'user').length;
    const assistantCount = history.filter(m => m.role === 'assistant').length;

    // Recalculate on-demand: raw history tokens (user & assistant text only)
    const rawTokens = history.length > 0 ? countConvTokens(history) : 0;

    // Recalculate on-demand: post-prune tokens (what will actually be sent)
    let postPruneTokens = rawTokens;
    if (history.length > 0) {
      postPruneTokens = countConvTokens(capUserTurns(pruneHistory(history)));
    }

    let reply = '📊 *Penggunaan Token*\n\n' +
      `👤 User: ${userCount} pesan\n` +
      `🤖 Assistant: ${assistantCount} pesan\n` +
      `📜 History (raw): ${rawTokens.toLocaleString()} token\n` +
      `✂️ History (post-prune): ${postPruneTokens.toLocaleString()} token\n`;
    if (lastStep) {
      reply += `🔢 Last step: ${lastStep.total.toLocaleString()} token (input: ${lastStep.input.toLocaleString()} + output: ${lastStep.output.toLocaleString()})\n\n`;
    }
    reply += `_ℹ️ History di-prune & dibatasi maksimal 5 pesan user sebelum request. Estimasi tidak termasuk system prompt._`;
    safeReply(ctx, reply, { reply_to_message_id: ctx.msg?.message_id });
  });

  bot.command('info', async (ctx: Context) => {
    const userId = ctx.from!.id;
    const arg = ((ctx.match as string) || '').trim().split(/\s+/)[0].toLowerCase();

    const files: Record<string, string> = {
      memory: 'memory/MEMORY.md',
      user: 'memory/USER.md',
      soul: 'memory/SOUL.md',
    };

    if (!arg) {
      const statuses: string[] = [];
      for (const name of Object.keys(files)) {
        const content = await vfs.readFile(userId, files[name]);
        statuses.push(`• /info ${name} — ${content ? 'ada' : 'kosong'}`);
      }
      await safeReply(ctx, `📁 *Info Memory*\n\n${statuses.join('\n')}\n\nGunakan:\n/info memory — Isi MEMORY.md\n/info user — Persona user\n/info soul — Persona AI`, { reply_to_message_id: ctx.msg?.message_id });
      return;
    }

    const file = files[arg];
    if (!file) {
      await safeReply(ctx, 'Subperintah tidak dikenal.\n\nGunakan:\n/info memory — Isi MEMORY.md\n/info user — Persona user\n/info soul — Persona AI', { reply_to_message_id: ctx.msg?.message_id });
      return;
    }

    const content = await vfs.readFile(userId, file);
    const label = arg === 'memory' ? 'MEMORY.md' : arg === 'user' ? 'USER.md' : 'SOUL.md';
    if (!content) {
      await safeReply(ctx, `Belum ada ${label}.`, { reply_to_message_id: ctx.msg?.message_id });
      return;
    }
    await safeReply(ctx, `📄 *${label}*\n\n${content}`, { reply_to_message_id: ctx.msg?.message_id });
  });

  bot.command('skills', async (ctx: Context) => {
    const userId = ctx.from!.id;
    const args = ((ctx.match as string) || '').trim().split(/\s+/);

    if (args.length === 0 || args[0] === '') {
      const skills = await listSkills(userId);

      if (skills.length === 0) {
        await safeReply(ctx, 'Belum ada skill tersimpan.\n\nGunakan:\n/skills search <query> — Mencari skill\n/skills install <url> — Install dari GitHub', { reply_to_message_id: ctx.msg?.message_id });
        return;
      }

      const skillList = skills.map((s, i) => `${i + 1}. *${s.name}* — ${s.description.substring(0, 50)}${s.description.length > 50 ? '...' : ''}`).join('\n');
      await safeReply(ctx, `📚 *Daftar Skills:*\n\n${skillList}\n\nGunakan:\n/skills info <nama> — Info detail\n/skills read <nama> — Baca isi\n/skills delete <nama> — Hapus`, { reply_to_message_id: ctx.msg?.message_id });
      return;
    }

    const sub = args[0].toLowerCase();

    if (sub === 'search') {
      const query = args.slice(1).join(' ');
      if (!query) {
        await safeReply(ctx, 'Gunakan: /skills search <query>', { reply_to_message_id: ctx.msg?.message_id });
        return;
      }

      const thinkingMsg = await ctx.reply('🔍 Mencari skill...', { reply_to_message_id: ctx.msg?.message_id });

      try {
        const { searchSkills } = await import('./skills-registry.js');
        const results = await searchSkills(query);

        if (results.length === 0) {
          await safeEdit(ctx, thinkingMsg.chat.id, thinkingMsg.message_id, `Tidak ditemukan skill untuk "${query}"`);
          return;
        }

        const resultList = results.slice(0, 10).map((r, i) => `${i + 1}. *${r.displayName}*\n   ${r.summary.substring(0, 100)}...\n   ${r.url}`).join('\n\n');
        await safeEdit(ctx, thinkingMsg.chat.id, thinkingMsg.message_id, `🔍 *Hasil Pencarian "${query}":*\n\n${resultList}\n\nGunakan /skills install <url> untuk menginstall`);
      } catch (err) {
        await safeEdit(ctx, thinkingMsg.chat.id, thinkingMsg.message_id, 'Gagal mencari skill. Silakan coba lagi.');
      }
      return;
    }

    if (sub === 'install') {
      const url = args[1];
      if (!url) {
        await safeReply(ctx, 'Gunakan: /skills install <url>\n\nContoh:\n/skills install https://github.com/user/repo\n/skills install user/repo', { reply_to_message_id: ctx.msg?.message_id });
        return;
      }

      const thinkingMsg = await ctx.reply('📦 Menginstall skill...', { reply_to_message_id: ctx.msg?.message_id });

      try {
        const result = await installFromGitHub(userId, url);

        if (result.success) {
          await safeEdit(ctx, thinkingMsg.chat.id, thinkingMsg.message_id, `✅ Skill "${result.name}" berhasil diinstall!\n\nPath: ${result.path}\n\nGunakan /skills info ${result.name} untuk melihat detail.`);
        } else {
          await safeEdit(ctx, thinkingMsg.chat.id, thinkingMsg.message_id, `❌ Gagal install: ${result.error}`);
        }
      } catch (err) {
        await safeEdit(ctx, thinkingMsg.chat.id, thinkingMsg.message_id, 'Gagal install skill. Silakan coba lagi.');
      }
      return;
    }

    if (sub === 'info') {
      const name = args[1];
      if (!name) {
        await safeReply(ctx, 'Gunakan: /skills info <nama>', { reply_to_message_id: ctx.msg?.message_id });
        return;
      }

      const { loadSkillWithMetadata } = await import('./skills-loader.js');
      const result = await loadSkillWithMetadata(userId, name);

      if (!result) {
        await safeReply(ctx, `Skill "${name}" tidak ditemukan.`, { reply_to_message_id: ctx.msg?.message_id });
        return;
      }

      const { metadata } = result;
      let info = `📋 *Info Skill*\n\n`;
      info += `*Nama:* ${metadata.name}\n`;
      info += `*Deskripsi:* ${metadata.description}\n`;
      if (metadata.homepage) {
        info += `*Homepage:* ${metadata.homepage}\n`;
      }
      info += `\nGunakan:\n/skills read ${name} — Baca isi\n/skills delete ${name} — Hapus`;

      await safeReply(ctx, info, { reply_to_message_id: ctx.msg?.message_id });
      return;
    }

    if (sub === 'read') {
      const name = args[1];
      const fileName = args[2];
      if (!name) {
        await safeReply(ctx, 'Gunakan: /skills read <nama> [file]', { reply_to_message_id: ctx.msg?.message_id });
        return;
      }

      if (fileName) {
        const filePath = `skills/${name}/${fileName}`;
        const content = await vfs.readFile(userId, filePath);

        if (!content) {
          await safeReply(ctx, `File "${fileName}" tidak ditemukan di skill "${name}".`, { reply_to_message_id: ctx.msg?.message_id });
          return;
        }

        const ext = fileName.split('.').pop() || 'txt';
        const filename = `${name}-${fileName}`;
        await ctx.replyWithDocument(
          new InputFile(Buffer.from(content, 'utf-8'), filename),
          { caption: `${filename}` },
        );
      } else {
        const content = await loadSkill(userId, name);

        if (!content) {
          await safeReply(ctx, `Skill "${name}" tidak ditemukan.`, { reply_to_message_id: ctx.msg?.message_id });
          return;
        }

        const files = await listSkillFiles(userId, name);
        const fileList = files.length > 1
          ? `\n\nFile tersedia:\n${files.map(f => `• ${f}`).join('\n')}\nGunakan: /skills read ${name} <file>`
          : '';

        const filename = `${name}.md`;
        await ctx.replyWithDocument(
          new InputFile(Buffer.from(content, 'utf-8'), filename),
          { caption: `${filename}${fileList}` },
        );
      }
      return;
    }

    if (sub === 'delete') {
      const name = args[1];
      if (!name) {
        await safeReply(ctx, 'Gunakan: /skills delete <nama>', { reply_to_message_id: ctx.msg?.message_id });
        return;
      }

      const deleted = await deleteSkillLoader(userId, name);

      if (deleted) {
        await safeReply(ctx, `🗑️ Skill "${name}" berhasil dihapus.`, { reply_to_message_id: ctx.msg?.message_id });
      } else {
        await safeReply(ctx, `Skill "${name}" tidak ditemukan.`, { reply_to_message_id: ctx.msg?.message_id });
      }
      return;
    }

    if (sub === 'migrate') {
      const thinkingMsg = await ctx.reply('🔄 Migrating skills...', { reply_to_message_id: ctx.msg?.message_id });

      try {
        const result = await migrateOldSkills(userId);

        if (result.migrated > 0) {
          let msg = `✅ Berhasil migrate ${result.migrated} skill`;
          if (result.errors.length > 0) {
            msg += `\n\n⚠️ Errors:\n${result.errors.join('\n')}`;
          }
          await safeEdit(ctx, thinkingMsg.chat.id, thinkingMsg.message_id, msg);
        } else if (result.errors.length > 0) {
          await safeEdit(ctx, thinkingMsg.chat.id, thinkingMsg.message_id, `❌ Tidak ada skill yang di-migrate:\n${result.errors.join('\n')}`);
        } else {
          await safeEdit(ctx, thinkingMsg.chat.id, thinkingMsg.message_id, 'Tidak ada skill lama yang perlu di-migrate.');
        }
      } catch (err) {
        await safeEdit(ctx, thinkingMsg.chat.id, thinkingMsg.message_id, 'Gagal migrate skills. Silakan coba lagi.');
      }
      return;
    }

    await safeReply(ctx, 'Subperintah tidak dikenal.\n\nGunakan:\n/skills — Daftar skill\n/skills search <query> — Cari skill\n/skills install <url> — Install dari GitHub\n/skills info <nama> — Info detail\n/skills read <nama> — Baca isi\n/skills delete <nama> — Hapus\n/skills migrate — Migrate skill lama', { reply_to_message_id: ctx.msg?.message_id });
  });

  const KNOWN_COMMANDS = ['/start', '/menu', '/clear', '/token', '/info', '/reset', '/skills'];

  bot.on('message:document', async (ctx: Context) => {
    const userId = ctx.from!.id;
    const isGroup = ctx.chat?.type === 'group' || ctx.chat?.type === 'supergroup';
    if (isGroup) return;

    const doc = ctx.message!.document!;
    const caption = ctx.message!.caption || '';
    const chatId = ctx.chat!.id;

    if (doc.file_size && doc.file_size > MAX_UPLOAD_SIZE) {
      await safeReply(ctx, '⚠️ File terlalu besar untuk diproses (maks 10MB).', { reply_to_message_id: ctx.msg?.message_id });
      return;
    }

    const trimmed = caption.trim();
    let vfsPath: string;
    let prompt: string;

    const firstSpace = trimmed.indexOf(' ');
    if (firstSpace > 0 && trimmed.startsWith('/')) {
      vfsPath = trimmed.slice(0, firstSpace).replace(/^\//, '');
      prompt = trimmed.slice(firstSpace + 1).trim();
    } else if (trimmed.startsWith('/')) {
      vfsPath = trimmed.slice(1);
      prompt = '';
    } else {
      vfsPath = doc.file_name || 'uploaded_file';
      prompt = trimmed;
    }

    vfsPath = vfsPath.replace(/\\/g, '/').replace(/\/+/g, '/').replace(/^\/+|\/+$/g, '');

    const file = await ctx.getFile();
    if (!file.file_path) {
      await safeReply(ctx, 'Gagal mengunduh file.');
      return;
    }

    const fileUrl = `https://api.telegram.org/file/bot${config.telegramBotToken}/${file.file_path}`;
    const fileRes = await fetch(fileUrl);
    const arrayBuffer = await fileRes.arrayBuffer();
    const fileContent = Buffer.from(arrayBuffer).toString('utf-8');

    await vfs.writeFile(userId, vfsPath, fileContent);

    const saveMsg = await ctx.reply(`📁 Tersimpan di \`/${vfsPath}\`\n\n🤔 PURU-AI sedang memproses...`, {
      reply_to_message_id: ctx.msg?.message_id,
      parse_mode: 'Markdown',
    });

    const history = await getHistory(userId);

    // Prune then cap to max 5 user turns before processing
    const pruned = pruneHistory(history);
    history.length = 0;
    history.push(...capUserTurns(pruned));

    try {
      const filePreview = fileContent.length > 4000 ? fileContent.slice(0, 4000) + '\n...(truncated)' : fileContent;
      const injectedPrompt = prompt
        ? `[Uploaded file: /${vfsPath}]\n\`\`\`\n${filePreview}\n\`\`\`\n\n${prompt}`
        : `[Uploaded file: /${vfsPath}]\n\`\`\`\n${filePreview}\n\`\`\``;

      const { text, responseMessages, totalTokens, lastStepUsage } = await processMessage(injectedPrompt, history, {
        chatId: userId,
        sendFile: async (content, filename, caption) => {
          await ctx.replyWithDocument(
            new InputFile(Buffer.from(content, 'utf-8'), filename),
            { caption: caption || filename },
          );
        },
        sendBuffer: async (buffer, filename, caption) => {
          const ext = filename.split('.').pop()?.toLowerCase();
          const audioExts = ['mp3', 'wav', 'flac', 'ogg', 'm4a', 'aac', 'wma'];
          const videoExts = ['mp4', 'webm', 'avi', 'mkv', 'mov'];
          if (audioExts.includes(ext || '')) {
            await ctx.replyWithAudio(new InputFile(buffer, filename), { caption: caption || filename });
          } else if (videoExts.includes(ext || '')) {
            await ctx.replyWithVideo(new InputFile(buffer, filename), { caption: caption || filename });
          } else {
            await ctx.replyWithDocument(new InputFile(buffer, filename), { caption: caption || filename });
          }
        },
      });

      history.push({ role: 'user', content: injectedPrompt } as ModelMessage);
      history.push(...sanitizeHistoryMessages(responseMessages));

      await setTokens(userId, { total: lastStepUsage.totalTokens, input: lastStepUsage.inputTokens, output: lastStepUsage.outputTokens });
      await setHistory(userId, history);

      await safeEdit(ctx, chatId, saveMsg.message_id, text);
      await maybeUpdateMemory(userId, history);
    } catch (error) {
      console.error('Error processing file message:', error);
      await safeEdit(ctx, chatId, saveMsg.message_id, 'Maaf, terjadi kesalahan saat memproses file.');
    }
  });

  bot.on('message:text', async (ctx: Context) => {
    const chatId = ctx.chat!.id;
    const userId = ctx.from!.id;
    const rawText = ctx.message!.text!;
    const isGroup = ctx.chat?.type === 'group' || ctx.chat?.type === 'supergroup';

    let userMessage: string | null = null;

    if (rawText.startsWith('/ai')) {
      const rest = rawText.slice(3).trim();
      if (rest) {
        userMessage = rest;
      } else {
        await safeReply(ctx, 'Gunakan /ai diikuti pesan, contoh: /ai apa kabar?', { reply_to_message_id: ctx.msg?.message_id });
        return;
      }
    } else if (rawText.startsWith('/')) {
      if (isGroup) return;
      const cmd = rawText.split(/\s/)[0];
      if (!KNOWN_COMMANDS.includes(cmd)) {
        await safeReply(ctx, INVALID_COMMAND_TEXT, { reply_to_message_id: ctx.msg?.message_id });
      }
      return;
    } else {
      if (isGroup) return;
      userMessage = rawText;
    }

    const history = await getHistory(userId);

    // Prune then cap to max 5 user turns before processing
    const pruned = pruneHistory(history);
    history.length = 0;
    history.push(...capUserTurns(pruned));

    let thinkingMsg;
    try {
      thinkingMsg = await ctx.reply('🤔 PURU-AI sedang berpikir...', { reply_to_message_id: ctx.msg?.message_id });
    } catch (err) {
      if (err instanceof GrammyError && err.error_code === 400 && err.description?.includes('message to be replied not found')) {
        thinkingMsg = await ctx.reply('🤔 PURU-AI sedang berpikir...');
      } else {
        throw err;
      }
    }
    const thinkingMsgId = thinkingMsg.message_id;

    try {
      const { text, responseMessages, totalTokens, lastStepUsage } = await processMessage(userMessage!, history, {
        chatId: userId,
        sendFile: async (content, filename, caption) => {
          await ctx.replyWithDocument(
            new InputFile(Buffer.from(content, 'utf-8'), filename),
            { caption: caption || filename },
          );
        },
        sendBuffer: async (buffer, filename, caption) => {
          const ext = filename.split('.').pop()?.toLowerCase();
          const audioExts = ['mp3', 'wav', 'flac', 'ogg', 'm4a', 'aac', 'wma'];
          const videoExts = ['mp4', 'webm', 'avi', 'mkv', 'mov'];
          if (audioExts.includes(ext || '')) {
            await ctx.replyWithAudio(new InputFile(buffer, filename), { caption: caption || filename });
          } else if (videoExts.includes(ext || '')) {
            await ctx.replyWithVideo(new InputFile(buffer, filename), { caption: caption || filename });
          } else {
            await ctx.replyWithDocument(new InputFile(buffer, filename), { caption: caption || filename });
          }
        },
      });

      history.push({ role: 'user', content: userMessage } as ModelMessage);
      history.push(...sanitizeHistoryMessages(responseMessages));

      await setTokens(userId, { total: lastStepUsage.totalTokens, input: lastStepUsage.inputTokens, output: lastStepUsage.outputTokens });
      await setHistory(userId, history);

      await safeEdit(ctx, chatId, thinkingMsgId, text);
      await maybeUpdateMemory(userId, history);
    } catch (error) {
      console.error('Error processing message:', error);
      await safeEdit(ctx, chatId, thinkingMsgId, 'Maaf, terjadi kesalahan saat memproses pesan Anda. Silakan coba lagi.');
    }
  });

  bot.catch((err) => {
    console.error('Bot error:', err);
  });

  return bot;
}
