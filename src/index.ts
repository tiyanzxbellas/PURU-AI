import 'dotenv/config';
import { createBot } from './bot.js';
import { startHealthServer } from './server.js';

startHealthServer();

async function start() {
  const bot = createBot();

  while (true) {
    try {
      await bot.start({
        onStart: () => {
          console.log('Bot started!');
        },
        drop_pending_updates: true,
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

start();
