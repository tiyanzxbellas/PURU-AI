import { Sandbox } from '@e2b/code-interpreter';
import { config } from './config.js';

if (config.e2bApiKey) {
  process.env.E2B_API_KEY = config.e2bApiKey;
}

const sandboxMap = new Map<number, { sandbox: Sandbox; createdAt: number }>();

const SANDBOX_TIMEOUT = 5 * 60 * 1000;

export function getSandboxInstance(chatId: number): Sandbox | null {
  const entry = sandboxMap.get(chatId);
  if (!entry) return null;
  if (Date.now() - entry.createdAt > SANDBOX_TIMEOUT) {
    killSandbox(chatId);
    return null;
  }
  return entry.sandbox;
}

export async function createSandbox(chatId: number): Promise<{ sandboxId: string }> {
  const existing = getSandboxInstance(chatId);
  if (existing) {
    return { sandboxId: existing.sandboxId };
  }

  const sandbox = await Sandbox.create({ timeoutMs: SANDBOX_TIMEOUT });
  sandboxMap.set(chatId, { sandbox, createdAt: Date.now() });
  return { sandboxId: sandbox.sandboxId };
}

export async function runCodeInSandbox(chatId: number, code: string, language: 'python' | 'javascript' = 'python'): Promise<{
  text: string;
  logs?: { stdout: string[]; stderr: string[] };
  error?: string;
}> {
  const sandbox = getSandboxInstance(chatId);
  if (!sandbox) {
    return { text: '', error: 'No active sandbox. Create one first with e2b_sandbox_create.' };
  }

  try {
    const result = await sandbox.runCode(code, { language });
    const logs = result.logs;
    return {
      text: result.text || '',
      logs: logs.stdout.length || logs.stderr.length ? { stdout: logs.stdout, stderr: logs.stderr } : undefined,
      error: result.error ? `${result.error.name}: ${result.error.value}` : undefined,
    };
  } catch (err) {
    sandboxMap.delete(chatId);
    return {
      text: '',
      error: `❌ Sandbox mati atau timeout. Buat ulang dengan e2b_sandbox_create.`,
    };
  }
}

export async function installPackageInSandbox(chatId: number, packageName: string, manager: 'pip' | 'npm' = 'pip'): Promise<{ success: boolean; output: string; error?: string }> {
  const sandbox = getSandboxInstance(chatId);
  if (!sandbox) {
    return { success: false, output: '', error: 'No active sandbox. Create one first with e2b_sandbox_create.' };
  }

  try {
    const cmd = manager === 'npm' ? `!npm install ${packageName}` : `!pip install ${packageName}`;
    const result = await sandbox.runCode(cmd);
    return {
      success: !result.error,
      output: result.logs.stdout.join('\n') || result.text || '',
      error: result.error ? `${result.error.name}: ${result.error.value}` : undefined,
    };
  } catch (err) {
    sandboxMap.delete(chatId);
    return {
      success: false,
      output: '',
      error: `❌ Sandbox mati atau timeout. Buat ulang dengan e2b_sandbox_create.`,
    };
  }
}

export async function readFileFromSandbox(chatId: number, path: string): Promise<{ content: Buffer | null; error?: string }> {
  const sandbox = getSandboxInstance(chatId);
  if (!sandbox) {
    return { content: null, error: 'No active sandbox. Create one first with e2b_sandbox_create.' };
  }

  try {
    const content = await sandbox.files.read(path);
    return { content: Buffer.from(content) };
  } catch (err) {
    sandboxMap.delete(chatId);
    return {
      content: null,
      error: `❌ Sandbox mati atau timeout. Buat ulang dengan e2b_sandbox_create.`,
    };
  }
}

export async function writeFileToSandbox(chatId: number, path: string, content: string): Promise<{ success: boolean; error?: string }> {
  const sandbox = getSandboxInstance(chatId);
  if (!sandbox) {
    return { success: false, error: 'No active sandbox. Create one first with e2b_sandbox_create.' };
  }

  try {
    await sandbox.files.write(path, content);
    return { success: true };
  } catch (err) {
    sandboxMap.delete(chatId);
    return {
      success: false,
      error: `❌ Sandbox mati atau timeout. Buat ulang dengan e2b_sandbox_create.`,
    };
  }
}

export function killSandbox(chatId: number): { success: boolean; message: string } {
  const entry = sandboxMap.get(chatId);
  if (!entry) {
    return { success: false, message: 'No active sandbox to kill.' };
  }

  entry.sandbox.kill().catch(() => {});
  sandboxMap.delete(chatId);
  return { success: true, message: 'Sandbox terminated successfully.' };
}
