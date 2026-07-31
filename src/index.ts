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

  let conflictCount = 0;
  const MAX_CONSECUTIVE_CONFLICTS = 5;

  while (true) {
    try {
      console.log('Connecting to Telegram...');
      await bot.start({
        drop_pending_updates: true,
        onStart: () => {
          console.log('Bot started!');
        },
      });
      conflictCount = 0;
      break;
    } catch (err) {
      const isConflict = err instanceof Error && (
        err.message.includes('409') ||
        err.message.toLowerCase().includes('conflict')
      );

      if (isConflict) {
        conflictCount++;
        if (conflictCount >= MAX_CONSECUTIVE_CONFLICTS) {
          console.error(`Conflict berturut-turut ${conflictCount}x — kemungkinan instance lain memakai token yang sama. Keluar agar platform restart bersih.`);
          process.exit(1);
        }
        console.warn(`Conflict detected (${conflictCount}/${MAX_CONSECUTIVE_CONFLICTS}), reconnecting in 10s...`);
        await new Promise(r => setTimeout(r, 10000));
        continue;
      }

      console.error('Fatal bot error:', err);
      process.exit(1);
    }
  }
}

start().catch((err) => {
  console.error('Unhandled start error:', err);
});
