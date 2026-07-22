const REQUIRED_ENV_VARS = [
  'HOSTNAME',
  'PORT',
  'PUBLIC_RTDB',
  'BOT_TOKEN',
  'E2B_APIKEY',
  'OPENAI_BASEURL',
  'OPENAI_APIKEY',
  'OPENAI_MODEL',
] as const;

const missing = REQUIRED_ENV_VARS.filter((key) => !process.env[key]);

if (missing.length > 0) {
  console.error(`❌ Missing required environment variables:\n${missing.map((k) => `  - ${k}`).join('\n')}`);
  process.exit(1);
}

const port = Number(process.env.PORT);
if (isNaN(port) || port <= 0 || port > 65535) {
  console.error(`❌ PORT must be a valid port number (1-65535), got: "${process.env.PORT}"`);
  process.exit(1);
}

interface AiConfig {
  baseURL: string;
  apiKey: string;
  model: string;
}

interface Config {
  telegramBotToken: string;
  hostname: string;
  port: number;
  publicRtdb: string;
  ai: AiConfig;
  e2bApiKey: string;
}

export const config: Config = {
  telegramBotToken: process.env.BOT_TOKEN!,
  hostname: process.env.HOSTNAME!,
  port,
  publicRtdb: process.env.PUBLIC_RTDB!,
  ai: {
    baseURL: process.env.OPENAI_BASEURL!,
    apiKey: process.env.OPENAI_APIKEY!,
    model: process.env.OPENAI_MODEL!,
  },
  e2bApiKey: process.env.E2B_APIKEY!,
};