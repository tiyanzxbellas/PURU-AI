export interface AiConfig {
  baseURL: string;
  apiKey: string;
  model: string;
}

export interface Config {
  telegramBotToken: string;
  ai: AiConfig;
  e2bApiKey: string;
}

declare const config: Config;
export default config;
