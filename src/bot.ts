import { Bot, InputFile, GrammyError, type Context } from 'grammy';
import { type ModelMessage, pruneMessages } from 'ai';
import { HumanMessage, AIMessage, SystemMessage, trimMessages, type BaseMessage } from '@langchain/core/messages';
import { config } from './config.js';
import { processMessage } from './agent.js';
import * as vfs from './vfs.js';

const MAX_HISTORY_TOKENS = config.compactToken;
const MAX_MESSAGE_LENGTH = 4096;

const MENU_TEXT =
  '📋 *Menu PURU-AI*\n\n' +
  'Perintah yang tersedia:\n' +
  '• /start — Memulai bot\n' +
  '• /menu — Menampilkan menu ini\n' +
  '• /clear — Menghapus riwayat percakapan\n' +
  '• /token — Melihat penggunaan token\n' +
  '• /memory — Melihat MEMORY.md\n' +
  '• /reset — Reset semua data (riwayat & file)\n' +
  '• /skills — Melihat daftar skill\n' +
  '• /skills read <nomor> — Membaca isi skill\n' +
  '• /skills delete <nomor> — Menghapus skill\n' +
  '• /ai <pesan> — Mengobrol dengan AI (khusus grup)\n\n' +
  'Di chat pribadi, kirim pesan langsung untuk mengobrol dengan AI.\n' +
  'Di grup, gunakan /ai diikuti pesan Anda.';

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

// Convert Vercel AI SDK ModelMessage to LangChain BaseMessage
function toLangChainMessage(msg: ModelMessage): BaseMessage {
  const content = typeof msg.content === 'string' ? msg.content : JSON.stringify(msg.content);
  if (msg.role === 'system') return new SystemMessage(content);
  if (msg.role === 'user') return new HumanMessage(content);
  if (msg.role === 'assistant') return new AIMessage(content);
  return new HumanMessage(content);
}

// Convert LangChain BaseMessage to Vercel AI SDK ModelMessage
function toModelMessage(msg: BaseMessage): ModelMessage {
  const role = msg.getType() === 'system' ? 'system' : msg.getType() === 'ai' ? 'assistant' : 'user';
  return { role, content: msg.content as any } as ModelMessage;
}

// Token counter based on character count (chars / 4)
function tokenCounter(msgs: BaseMessage[]): number {
  let chars = 0;
  for (const msg of msgs) {
    const content = msg.content as any;
    if (typeof content === 'string') {
      chars += content.length;
    } else if (Array.isArray(content)) {
      for (const part of content) {
        if (typeof part === 'string') chars += part.length;
        else if (part && typeof part === 'object') {
          if ('text' in part && typeof part.text === 'string') chars += part.text.length;
          else if ('args' in part) chars += JSON.stringify(part.args || {}).length + 50;
          else if ('result' in part) {
            const r = typeof part.result === 'string' ? part.result : JSON.stringify(part.result || {});
            chars += r.length;
          }
        }
      }
    }
  }
  return Math.round(chars / 4);
}

// Trim history to stay within token limit
async function trimHistory(history: ModelMessage[]): Promise<ModelMessage[]> {
  if (history.length === 0) return history;
  
  const lcMessages = history.map(toLangChainMessage);
  const estimatedTokens = tokenCounter(lcMessages);
  
  if (estimatedTokens <= MAX_HISTORY_TOKENS) {
    return history;
  }
  
  const trimmed = await trimMessages(lcMessages, {
    maxTokens: MAX_HISTORY_TOKENS,
    tokenCounter,
    strategy: 'last',
    includeSystem: true,
    allowPartial: false,
  });
  
  const result = trimmed.map(toModelMessage);
  const trimmedTokens = tokenCounter(trimmed);
  console.log(`History trimmed from ${estimatedTokens} to ${trimmedTokens} estimated tokens (limit: ${MAX_HISTORY_TOKENS})`);
  
  return result;
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

const INVALID_COMMAND_TEXT =
  '❌ Perintah tidak dikenal. Gunakan /menu untuk melihat daftar perintah yang tersedia.';

export function createBot() {
  const bot = new Bot(config.telegramBotToken);

  const chatHistories = new Map<number, ModelMessage[]>();
  const chatAccumulatedTokens = new Map<number, number>();
  const chatTotalTokens = new Map<number, { total: number; input: number; output: number }>();

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

  bot.command('clear', (ctx: Context) => {
    const userId = ctx.from!.id;
    chatHistories.delete(userId);
    chatAccumulatedTokens.delete(userId);
    chatTotalTokens.delete(userId);
    safeReply(ctx, 'Riwayat percakapan telah dihapus!', { reply_to_message_id: ctx.msg?.message_id });
  });

  bot.command('reset', async (ctx: Context) => {
    const userId = ctx.from!.id;
    chatHistories.delete(userId);
    chatAccumulatedTokens.delete(userId);
    chatTotalTokens.delete(userId);
    await vfs.deleteAll(userId);
    safeReply(ctx, '🗑️ Semua data Anda (riwayat percakapan & file VFS) telah dihapus.', { reply_to_message_id: ctx.msg?.message_id });
  });

  bot.command('token', (ctx: Context) => {
    const userId = ctx.from!.id;
    const historyTokens = chatAccumulatedTokens.get(userId) || 0;
    const lastStep = chatTotalTokens.get(userId);
    const history = chatHistories.get(userId);
    if ((historyTokens === 0 || !history || history.length === 0) && !lastStep) {
      safeReply(ctx, 'Belum ada riwayat percakapan.', { reply_to_message_id: ctx.msg?.message_id });
      return;
    }
    const userCount = history ? history.filter(m => m.role === 'user').length : 0;
    const assistantCount = history ? history.filter(m => m.role === 'assistant').length : 0;

    let reply = '📊 *Penggunaan Token*\n\n' +
      `👤 User: ${userCount} pesan\n` +
      `🤖 Assistant: ${assistantCount} pesan\n` +
      `📜 History: ${historyTokens.toLocaleString()} token (estimasi)\n`;
    if (lastStep) {
      reply += `🔢 Last step: ${lastStep.total.toLocaleString()} token (input: ${lastStep.input.toLocaleString()} + output: ${lastStep.output.toLocaleString()})\n\n`;
    }
    reply += `_ℹ️ History otomatis di-prune & dipotong jika estimasi > ${MAX_HISTORY_TOKENS.toLocaleString()} token_`;
    safeReply(ctx, reply, { reply_to_message_id: ctx.msg?.message_id });
  });

  bot.command('memory', async (ctx: Context) => {
    const userId = ctx.from!.id;
    const content = await vfs.readFile(userId, 'memory/MEMORY.md');
    if (!content) {
      await safeReply(ctx, 'Belum ada MEMORY.md.', { reply_to_message_id: ctx.msg?.message_id });
      return;
    }
    await safeReply(ctx, `🧠 *MEMORY.md*\n\n${content}`, { reply_to_message_id: ctx.msg?.message_id });
  });

  bot.command('skills', async (ctx: Context) => {
    const userId = ctx.from!.id;
    const fullText = ctx.message?.text || '';
    const args = fullText.replace(/^\/skills\s*/i, '').trim().split(/\s+/);

    const entries = await vfs.listDirectory(userId, 'skills');
    const skillNames = entries.filter(e => e.name && e.name.endsWith('.md')).map(e => e.name.replace(/\.md$/, ''));

    if (args.length === 0 || (args.length === 1 && args[0] === '')) {
      if (skillNames.length === 0) {
        await safeReply(ctx, 'Belum ada skill tersimpan.', { reply_to_message_id: ctx.msg?.message_id });
        return;
      }
      await safeReply(ctx, `📚 *Daftar Skills:*\n\n${skillNames.map((n, i) => `• ${i + 1}. ${n}`).join('\n')}\n\nGunakan:\n/skills read <nomor>\n/skills delete <nomor>`, { reply_to_message_id: ctx.msg?.message_id });
      return;
    }

    const sub = args[0].toLowerCase();
    const num = parseInt(args[1], 10);

    if (isNaN(num) || num < 1 || num > skillNames.length) {
      await safeReply(ctx, `Nomor tidak valid. Gunakan /skills untuk melihat daftar skill.`, { reply_to_message_id: ctx.msg?.message_id });
      return;
    }

    const skillName = skillNames[num - 1];

    if (sub === 'read') {
      const content = await vfs.readFile(userId, `skills/${skillName}.md`);
      const filename = `${skillName}.md`;
      await ctx.replyWithDocument(
        new InputFile(Buffer.from(content ?? '', 'utf-8'), filename),
        { caption: `📄 ${filename}` },
      );
    } else if (sub === 'delete') {
      await vfs.deleteFile(userId, `skills/${skillName}.md`);
      await safeReply(ctx, `🗑️ Skill "${skillName}" berhasil dihapus.`, { reply_to_message_id: ctx.msg?.message_id });
    } else {
      await safeReply(ctx, 'Subperintah tidak dikenal. Gunakan: /skills read <nomor> atau /skills delete <nomor>', { reply_to_message_id: ctx.msg?.message_id });
    }
  });

  const KNOWN_COMMANDS = ['/start', '/menu', '/clear', '/token', '/memory', '/reset', '/skills'];

  bot.on('message:document', async (ctx: Context) => {
    const userId = ctx.from!.id;
    const isGroup = ctx.chat?.type === 'group' || ctx.chat?.type === 'supergroup';
    if (isGroup) return;

    const doc = ctx.message!.document!;
    const caption = ctx.message!.caption || '';
    const chatId = ctx.chat!.id;

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

    if (!chatHistories.has(userId)) {
      chatHistories.set(userId, []);
    }
    const history = chatHistories.get(userId)!;

    // Prune then trim history before processing
    const pruned = pruneHistory(history);
    const trimmedHistory = await trimHistory(pruned);
    history.length = 0;
    history.push(...trimmedHistory);

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
      history.push(...responseMessages);

      chatTotalTokens.set(userId, { total: lastStepUsage.totalTokens, input: lastStepUsage.inputTokens, output: lastStepUsage.outputTokens });
      chatAccumulatedTokens.set(userId, tokenCounter(history.map(toLangChainMessage)));

      await safeEdit(ctx, chatId, saveMsg.message_id, text);
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

    if (!chatHistories.has(userId)) {
      chatHistories.set(userId, []);
    }
    const history = chatHistories.get(userId)!;

    // Prune then trim history before processing
    const pruned = pruneHistory(history);
    const trimmedHistory = await trimHistory(pruned);
    history.length = 0;
    history.push(...trimmedHistory);

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
      history.push(...responseMessages);

      chatTotalTokens.set(userId, { total: lastStepUsage.totalTokens, input: lastStepUsage.inputTokens, output: lastStepUsage.outputTokens });
      chatAccumulatedTokens.set(userId, tokenCounter(history.map(toLangChainMessage)));

      await safeEdit(ctx, chatId, thinkingMsgId, text);
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
