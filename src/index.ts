import 'dotenv/config';
import { config } from './config.js';
import { createBot } from './bot.js';
import { startHealthServer } from './server.js';

startHealthServer();

async function start() {
  const bot = createBot();

  try {
    const res = await fetch(`https://api.telegram.org/bot${config.telegramBotToken}/getMe`);
    const data = await res.json() as any;
    if (data.ok) {
      console.log(`Telegram API connected: @${data.result.username}`);
    } else {
      console.error('Telegram API error:', data.description);
      return;
    }
  } catch (err) {
    console.error('Cannot reach Telegram API:', err);
    return;
  }

  while (true) {
    try {
      console.log('Connecting to Telegram...');
      await bot.start({
        onStart: () => {
          console.log('Bot started!');
        },
      });
      break;
    } catch (err) {
      const isConflict = err instanceof Error && (
        err.message.includes('409') ||
        err.message.toLowerCase().includes('conflict')
      );

      if (isConflict) {
        console.warn(`Conflict detected, reconnecting in 10s...`);
        await new Promise(r => setTimeout(r, 10000));
        continue;
      }

      console.error('Fatal bot error:', err);
      break;
    }
  }
}

start().catch((err) => {
  console.error('Unhandled start error:', err);
});
