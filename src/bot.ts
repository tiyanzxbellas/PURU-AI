import { Bot, InputFile, GrammyError, type Context } from 'grammy';
import { type ModelMessage } from 'ai';
import { config } from './config.js';
import { processMessage } from './agent.js';

function estimateHistoryTokens(messages: ModelMessage[]): number {
  let chars = 0;
  for (const msg of messages) {
    if (typeof msg.content === 'string') {
      chars += msg.content.length;
    } else if (Array.isArray(msg.content)) {
      for (const part of msg.content) {
        if (part.type === 'text') chars += (part.text || '').length;
        else if (part.type === 'reasoning') chars += (part.text || '').length;
        else if (part.type === 'tool-call') chars += JSON.stringify((part as any).args || {}).length + ((part as any).toolName || '').length + 50;
        else if (part.type === 'tool-result') {
          const val = (part as any).result;
          const r = typeof val === 'string' ? val : JSON.stringify(val || {});
          chars += r.length;
        }
      }
    }
  }
  return Math.round(chars / 4);
}

const MENU_TEXT =
  '📋 *Menu PURU-AI*\n\n' +
  'Perintah yang tersedia:\n' +
  '• /start — Memulai bot\n' +
  '• /menu — Menampilkan menu ini\n' +
  '• /clear — Menghapus riwayat percakapan\n' +
  '• /token — Melihat penggunaan token\n\n' +
  'Atau kirim pesan langsung untuk mengobrol dengan AI!';

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

const INVALID_COMMAND_TEXT =
  '❌ Perintah tidak dikenal. Gunakan /menu untuk melihat daftar perintah yang tersedia.';

export function createBot() {
  const bot = new Bot(config.telegramBotToken);

  const chatHistories = new Map<number, ModelMessage[]>();

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
    safeReply(ctx, 'Riwayat percakapan telah dihapus!', { reply_to_message_id: ctx.msg?.message_id });
  });

  bot.command('token', (ctx: Context) => {
    const userId = ctx.from!.id;
    const history = chatHistories.get(userId);
    if (!history || history.length === 0) {
      safeReply(ctx, 'Belum ada riwayat percakapan.', { reply_to_message_id: ctx.msg?.message_id });
      return;
    }
    const total = estimateHistoryTokens(history);
    safeReply(
      ctx,
      '📊 *Perkiraan Token*\n\n' +
      `Riwayat saat ini: ~${total} token\n\n` +
      `_Estimasi berdasarkan ~4 karakter per token_`,
      { reply_to_message_id: ctx.msg?.message_id },
    );
  });

  bot.on('message:text', async (ctx: Context) => {
    const chatId = ctx.chat!.id;
    const userId = ctx.from!.id;
    const userMessage = ctx.message!.text!;

    const isCommand = userMessage.startsWith('/');
    if (isCommand) {
      await safeReply(ctx, INVALID_COMMAND_TEXT, { reply_to_message_id: ctx.msg?.message_id });
      return;
    }

    if (!chatHistories.has(userId)) {
      chatHistories.set(userId, []);
    }
    const history = chatHistories.get(userId)!;

    const thinkingMsg = await ctx.reply('🤔 PURU-AI sedang berpikir...', { reply_to_message_id: ctx.msg?.message_id });
    const thinkingMsgId = thinkingMsg.message_id;

    try {
      const { text, responseMessages } = await processMessage(userMessage, history, {
        chatId: userId,
        sendFile: async (content, filename, caption) => {
          await ctx.replyWithDocument(
            new InputFile(Buffer.from(content, 'utf-8'), filename),
            { caption: caption || filename },
          );
        },
      });

      history.push({ role: 'user', content: userMessage } as ModelMessage);
      history.push(...responseMessages);

      if (history.length > 40) {
        history.splice(0, history.length - 40);
      }

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
