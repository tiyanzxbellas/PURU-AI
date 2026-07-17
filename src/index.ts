import { createBot } from './bot.js';
import { startHealthServer } from './server.js';

startHealthServer();

const bot = createBot();

bot.start({
  onStart: () => {
    console.log('Bot started!');
  },
});
