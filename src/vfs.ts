const FIREBASE_BASE = 'https://puru-69425-default-rtdb.firebaseio.com';

function normalizePath(p: string): string {
  return p.replace(/\\/g, '/').replace(/\/+/g, '/').replace(/^\/+|\/+$/g, '');
}

function base64(s: string): string {
  return Buffer.from(s, 'utf-8').toString('base64url');
}

function dirname(p: string): string | null {
  const idx = p.lastIndexOf('/');
  return idx > 0 ? p.slice(0, idx) : (idx === 0 ? '' : null);
}

function basename(p: string): string {
  const idx = p.lastIndexOf('/');
  return idx >= 0 ? p.slice(idx + 1) : p;
}

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

function indexPath(chatId: number, p: string): string {
  return p ? `fs/${chatId}/index/${base64(p)}` : `fs/${chatId}/index`;
}

function contentPath(chatId: number, p: string): string {
  return `fs/${chatId}/content/${base64(p)}`;
}

async function ensureAncestors(chatId: number, p: string): Promise<void> {
  const parts = p.split('/');
  if (parts.length <= 1) return;

  let accumulated = '';
  for (let i = 0; i < parts.length - 1; i++) {
    const part = parts[i];
    const idx = await fbGet(indexPath(chatId, accumulated)) || { entries: [] };
    if (!idx.entries) idx.entries = [];
    if (idx.entries.every((e: any) => e.name !== part)) {
      idx.entries.push({ name: part, type: 'dir' });
      await fbPut(indexPath(chatId, accumulated), idx);
    }
    accumulated = accumulated ? `${accumulated}/${part}` : part;
  }
}

export async function readFile(chatId: number, path: string): Promise<string | null> {
  const p = normalizePath(path);
  if (!p) return null;
  const data = await fbGet(contentPath(chatId, p));
  return typeof data === 'string' ? data : null;
}

export async function writeFile(chatId: number, path: string, content: string): Promise<void> {
  const p = normalizePath(path);
  if (!p) return;

  await fbPut(contentPath(chatId, p), content);

  const parent = dirname(p);
  const idx = await fbGet(indexPath(chatId, parent ?? '')) || { entries: [] };
  if (!idx.entries) idx.entries = [];
  if (idx.entries.every((e: any) => e.name !== basename(p))) {
    idx.entries.push({ name: basename(p), type: 'file' });
    await fbPut(indexPath(chatId, parent ?? ''), idx);
  }

  if (parent !== null) {
    await ensureAncestors(chatId, p);
  }
}

export async function deleteFile(chatId: number, path: string): Promise<boolean> {
  const p = normalizePath(path);
  if (!p) return false;

  const content = await fbGet(contentPath(chatId, p));
  if (content === null) return false;

  await fbDelete(contentPath(chatId, p));

  const parent = dirname(p);
  const idx = await fbGet(indexPath(chatId, parent ?? '')) || { entries: [] };
  if (idx.entries) {
    idx.entries = idx.entries.filter((e: any) => e.name !== basename(p));
    await fbPut(indexPath(chatId, parent ?? ''), idx);
  }

  return true;
}

export async function listDirectory(chatId: number, path: string): Promise<{ name: string; type: 'file' | 'dir' }[]> {
  const p = normalizePath(path);
  const idx = await fbGet(indexPath(chatId, p));
  if (!idx || !idx.entries) return [];
  return idx.entries;
}

export async function editFile(chatId: number, path: string, oldString: string, newString: string): Promise<{ success: boolean; error?: string }> {
  const content = await readFile(chatId, path);
  if (content === null) return { success: false, error: 'File not found' };

  if (!content.includes(oldString)) return { success: false, error: 'old_string not found in file' };

  const newContent = content.replace(oldString, newString);
  await writeFile(chatId, path, newContent);
  return { success: true };
}
