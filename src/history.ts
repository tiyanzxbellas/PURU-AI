import { LRUCache } from 'lru-cache';
import { type ModelMessage } from 'ai';
import { config } from './config.js';

const FIREBASE_BASE = config.publicRtdb;

// --- LRU Caches ---

const historyCache = new LRUCache<string, ModelMessage[]>({
  max: config.historyCacheMax,
  ttl: config.historyCacheTtl,
});

const tokensCache = new LRUCache<string, { total: number; input: number; output: number }>({
  max: config.historyCacheMax,
  ttl: config.historyCacheTtl,
});

// --- Firebase helpers ---

async function fbGet(path: string): Promise<any> {
  try {
    const res = await fetch(`${FIREBASE_BASE}/${path}.json`);
    if (!res.ok) return null;
    return res.json();
  } catch {
    return null;
  }
}

async function fbPut(path: string, data: any): Promise<void> {
  await fetch(`${FIREBASE_BASE}/${path}.json`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(data),
  });
}

async function fbDelete(path: string): Promise<void> {
  await fetch(`${FIREBASE_BASE}/${path}.json`, { method: 'DELETE' });
}

// --- History CRUD ---

export async function getHistory(chatId: number): Promise<ModelMessage[]> {
  const key = String(chatId);
  const cached = historyCache.get(key);
  if (cached) return [...cached];

  const data = await fbGet(`history/${key}/messages`);
  const messages: ModelMessage[] = Array.isArray(data) ? data : [];
  if (messages.length > 0) {
    historyCache.set(key, messages);
  }
  return messages;
}

export async function setHistory(chatId: number, messages: ModelMessage[]): Promise<void> {
  const key = String(chatId);
  historyCache.set(key, messages);
  await fbPut(`history/${key}/messages`, messages);
}

export async function deleteHistory(chatId: number): Promise<void> {
  const key = String(chatId);
  historyCache.delete(key);
  tokensCache.delete(key);
  await fbDelete(`history/${key}`);
}

// --- Tokens CRUD ---

export async function getTokens(chatId: number): Promise<{ total: number; input: number; output: number } | null> {
  const key = String(chatId);
  const cached = tokensCache.get(key);
  if (cached) return cached;

  const data = await fbGet(`history/${key}/tokens`);
  if (!data || typeof data !== 'object') return null;
  tokensCache.set(key, data);
  return data;
}

export async function setTokens(chatId: number, tokens: { total: number; input: number; output: number }): Promise<void> {
  const key = String(chatId);
  tokensCache.set(key, tokens);
  await fbPut(`history/${key}/tokens`, tokens);
}

// --- Meta CRUD (counter internal, mis. jumlah turn user untuk trigger memory update) ---

export async function getMeta(chatId: number): Promise<{ userTurns: number } | null> {
  const data = await fbGet(`history/${String(chatId)}/meta`);
  if (!data || typeof data !== 'object') return null;
  return { userTurns: Number(data.userTurns) || 0 };
}

export async function setMeta(chatId: number, meta: { userTurns: number }): Promise<void> {
  await fbPut(`history/${String(chatId)}/meta`, meta);
}
