export interface AiConfig {
  baseURL: string;
  apiKey: string;
  model: string;
}

export interface Config {
  telegramBotToken: string;
  ai: AiConfig;
}

declare const config: Config;
export default config;
