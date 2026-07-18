import { Bot, InputFile, GrammyError, type Context } from 'grammy';
import { type ModelMessage, pruneMessages } from 'ai';
import { config } from './config.js';
import { processMessage, estimateSimpleTokens, summarizeHistory } from './agent.js';
import * as vfs from './vfs.js';

const COMPACT_THRESHOLD = 20000;
const SUMMARIZE_THRESHOLD = 10000;

const MENU_TEXT =
  '📋 *Menu PURU-AI*\n\n' +
  'Perintah yang tersedia:\n' +
  '• /start — Memulai bot\n' +
  '• /menu — Menampilkan menu ini\n' +
  '• /clear — Menghapus riwayat percakapan\n' +
  '• /token — Melihat penggunaan token\n' +
  '• /reset — Reset semua data (riwayat & file)\n' +
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

async function compactHistory(history: ModelMessage[], userId: number, totalTokens: number, chatAccumulatedTokens: Map<number, number>) {
  if (totalTokens <= COMPACT_THRESHOLD) return;

  const pruned = pruneMessages({
    messages: history,
    reasoning: 'before-last-message',
    toolCalls: 'before-last-2-messages',
    emptyMessages: 'remove',
  });

  const estimatedTokens = estimateSimpleTokens(pruned);

  if (estimatedTokens > SUMMARIZE_THRESHOLD) {
    try {
      const summary = await summarizeHistory(pruned);
      history.length = 0;
      history.push(...summary);
      console.log(`[${userId}] History summarized (${estimatedTokens} -> ${estimateSimpleTokens(summary)} estimated tokens)`);
    } catch (err) {
      history.length = 0;
      history.push(...pruned);
      console.log(`[${userId}] Summarize failed, using pruned history:`, err);
    }
  } else {
    history.length = 0;
    history.push(...pruned);
    console.log(`[${userId}] History pruned (${totalTokens} -> ${estimatedTokens} estimated tokens)`);
  }

  chatAccumulatedTokens.set(userId, estimateSimpleTokens(history));
}

const INVALID_COMMAND_TEXT =
  '❌ Perintah tidak dikenal. Gunakan /menu untuk melihat daftar perintah yang tersedia.';

export function createBot() {
  const bot = new Bot(config.telegramBotToken);

  const chatHistories = new Map<number, ModelMessage[]>();
  const chatAccumulatedTokens = new Map<number, number>();

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
    safeReply(ctx, 'Riwayat percakapan telah dihapus!', { reply_to_message_id: ctx.msg?.message_id });
  });

  bot.command('reset', async (ctx: Context) => {
    const userId = ctx.from!.id;
    chatHistories.delete(userId);
    chatAccumulatedTokens.delete(userId);
    await vfs.deleteAll(userId);
    safeReply(ctx, '🗑️ Semua data Anda (riwayat percakapan & file VFS) telah dihapus.', { reply_to_message_id: ctx.msg?.message_id });
  });

  bot.command('token', (ctx: Context) => {
    const userId = ctx.from!.id;
    const lastUsage = chatAccumulatedTokens.get(userId) || 0;
    if (lastUsage === 0) {
      safeReply(ctx, 'Belum ada riwayat percakapan.', { reply_to_message_id: ctx.msg?.message_id });
      return;
    }
    const pct = lastUsage > 0 ? Math.round((lastUsage / 128000) * 100) : 0;
    safeReply(
      ctx,
      '📊 *Penggunaan Token*\n\n' +
      `Terakhir dipakai: ${lastUsage.toLocaleString()} token\n` +
      `Batas API: 128.000 token (${pct}%)\n\n` +
      `_${lastUsage > COMPACT_THRESHOLD ? '⚠️' : '✅'} Auto-compact di ${COMPACT_THRESHOLD.toLocaleString()} token_`,
      { reply_to_message_id: ctx.msg?.message_id },
    );
  });

  const KNOWN_COMMANDS = ['/start', '/menu', '/clear', '/token', '/reset'];

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

    try {
      const filePreview = fileContent.length > 4000 ? fileContent.slice(0, 4000) + '\n...(truncated)' : fileContent;
      const injectedPrompt = prompt
        ? `[Uploaded file: /${vfsPath}]\n\`\`\`\n${filePreview}\n\`\`\`\n\n${prompt}`
        : `[Uploaded file: /${vfsPath}]\n\`\`\`\n${filePreview}\n\`\`\``;

      const { text, responseMessages, totalTokens } = await processMessage(injectedPrompt, history, {
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

      chatAccumulatedTokens.set(userId, totalTokens);

      await compactHistory(history, userId, totalTokens, chatAccumulatedTokens);

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
      const { text, responseMessages, totalTokens } = await processMessage(userMessage!, history, {
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

      chatAccumulatedTokens.set(userId, totalTokens);

      await compactHistory(history, userId, totalTokens, chatAccumulatedTokens);

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
