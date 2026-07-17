import raw from '../config.js';

interface AiConfig {
  baseURL: string;
  apiKey: string;
  model: string;
}

interface Config {
  telegramBotToken: string;
  ai: AiConfig;
}

const rawConfig = raw as Config;

export const config: Config = {
  telegramBotToken: process.env.BOT_TOKEN || rawConfig.telegramBotToken,
  ai: rawConfig.ai,
};
