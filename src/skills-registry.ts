import * as vfs from './vfs.js';
import { parseFrontmatter, validateSkillName, buildSkillContent, splitFrontmatter } from './skills-loader.js';

export interface GitHubRef {
  owner: string;
  repo: string;
  ref: string;
  subPath: string;
  explicitRef: boolean;
}

export interface SearchResult {
  slug: string;
  displayName: string;
  summary: string;
  url: string;
}

export interface InstallResult {
  success: boolean;
  name?: string;
  error?: string;
  path?: string;
}

const GITHUB_API_BASE = 'https://api.github.com';
const RAW_GITHUB_BASE = 'https://raw.githubusercontent.com';
const SKILL_MARKDOWN = 'SKILL.md';
const MAX_SEARCH_RESULTS = 20;
const MAX_RETRIES = 3;

function parseGitHubRef(input: string): GitHubRef | null {
  const trimmed = input.trim();

  if (trimmed.startsWith('https://github.com/') || trimmed.startsWith('http://github.com/')) {
    try {
      const url = new URL(trimmed);
      const parts = url.pathname.split('/').filter(Boolean);

      if (parts.length < 2) return null;

      const ref: GitHubRef = {
        owner: parts[0],
        repo: parts[1],
        ref: 'main',
        subPath: '',
        explicitRef: false,
      };

      const treeIndex = parts.indexOf('tree');
      if (treeIndex !== -1 && treeIndex + 1 < parts.length) {
        ref.ref = parts[treeIndex + 1];
        ref.explicitRef = true;
        if (treeIndex + 2 < parts.length) {
          ref.subPath = parts.slice(treeIndex + 2).join('/');
        }
      }

      return ref;
    } catch {
      return null;
    }
  }

  const parts = trimmed.split('/').filter(Boolean);
  if (parts.length < 2) return null;

  const ref: GitHubRef = {
    owner: parts[0],
    repo: parts[1],
    ref: 'main',
    subPath: '',
    explicitRef: false,
  };

  if (parts.length > 2) {
    ref.subPath = parts.slice(2).join('/');
  }

  return ref;
}

async function fetchWithRetry(url: string, options?: RequestInit): Promise<Response> {
  let lastError: Error | null = null;

  for (let attempt = 1; attempt <= MAX_RETRIES; attempt++) {
    try {
      const response = await fetch(url, {
        ...options,
        signal: AbortSignal.timeout(15000),
      });
      return response;
    } catch (err) {
      lastError = err instanceof Error ? err : new Error(String(err));
      if (attempt < MAX_RETRIES) {
        await new Promise(resolve => setTimeout(resolve, 1000 * attempt));
      }
    }
  }

  throw lastError || new Error('Fetch failed');
}

async function fetchGitHubTree(owner: string, repo: string, ref: string): Promise<string[]> {
  const url = `${GITHUB_API_BASE}/repos/${owner}/${repo}/git/trees/${ref}?recursive=1`;
  const response = await fetchWithRetry(url);

  if (!response.ok) {
    throw new Error(`GitHub API error: ${response.status}`);
  }

  const data = await response.json() as any;
  const tree = data.tree || [];

  return tree
    .filter((item: any) => item.type === 'blob')
    .map((item: any) => item.path);
}

async function fetchRawFile(owner: string, repo: string, ref: string, filePath: string): Promise<string> {
  const url = `${RAW_GITHUB_BASE}/${owner}/${repo}/${ref}/${filePath}`;
  const response = await fetchWithRetry(url);

  if (!response.ok) {
    throw new Error(`Failed to fetch ${filePath}: ${response.status}`);
  }

  return response.text();
}

async function getDefaultBranch(owner: string, repo: string): Promise<string> {
  const url = `${GITHUB_API_BASE}/repos/${owner}/${repo}`;
  const response = await fetchWithRetry(url);

  if (!response.ok) {
    throw new Error(`GitHub API error: ${response.status}`);
  }

  const data = await response.json() as any;
  return data.default_branch || 'main';
}

export async function searchSkills(query: string): Promise<SearchResult[]> {
  const results: SearchResult[] = [];

  try {
    const searchQuery = encodeURIComponent(`skill ${query}`);
    const url = `${GITHUB_API_BASE}/search/repositories?q=${searchQuery}&per_page=${MAX_SEARCH_RESULTS}`;

    const response = await fetchWithRetry(url);

    if (!response.ok) {
      return results;
    }

    const data = await response.json() as any;
    const items = data.items || [];

    for (const item of items) {
      const name = item.name || '';
      const description = item.description || '';
      const htmlUrl = item.html_url || '';

      if (name.toLowerCase().includes('skill') || description.toLowerCase().includes('skill')) {
        results.push({
          slug: `${item.owner?.login || ''}/${name}`,
          displayName: name,
          summary: description.substring(0, 200),
          url: htmlUrl,
        });
      }
    }
  } catch (err) {
    console.error('Search failed:', err);
  }

  return results;
}

export async function installFromGitHub(chatId: number, url: string): Promise<InstallResult> {
  const ref = parseGitHubRef(url);

  if (!ref) {
    return { success: false, error: 'URL GitHub tidak valid' };
  }

  if (!ref.explicitRef) {
    try {
      ref.ref = await getDefaultBranch(ref.owner, ref.repo);
    } catch (err) {
      const errorMsg = err instanceof Error ? err.message : String(err);
      return { success: false, error: `Gagal resolve branch: ${errorMsg}` };
    }
  }

  try {
    const filePaths = await fetchGitHubTree(ref.owner, ref.repo, ref.ref);

    const filteredPaths = ref.subPath
      ? filePaths.filter(p => p.startsWith(ref.subPath + '/') || p === ref.subPath)
      : filePaths;

    const skillMdPath = filteredPaths.find(p => {
      const fileName = p.split('/').pop();
      return fileName === SKILL_MARKDOWN;
    });

    if (!skillMdPath) {
      return { success: false, error: 'SKILL.md tidak ditemukan di repository' };
    }

    const skillRoot = skillMdPath.includes('/')
      ? skillMdPath.split('/').slice(0, -1).join('/')
      : '';

    const rootPaths = skillRoot
      ? filteredPaths.filter(p => p.startsWith(skillRoot + '/'))
      : filteredPaths;

    const skillMdContent = await fetchRawFile(ref.owner, ref.repo, ref.ref, skillMdPath);
    const metadata = parseFrontmatter(skillMdContent);
    const skillName = metadata.name || ref.repo;

    const validationError = validateSkillName(skillName);
    if (validationError) {
      return { success: false, error: validationError };
    }

    const existing = await vfs.readFile(chatId, `skills/${skillName}/SKILL.md`);
    if (existing !== null) {
      return { success: false, error: `Skill "${skillName}" sudah terinstall` };
    }

    for (const filePath of rootPaths) {
      const content = await fetchRawFile(ref.owner, ref.repo, ref.ref, filePath);
      const relativePath = skillRoot
        ? filePath.slice(skillRoot.length + 1)
        : filePath;
      const vfsPath = `skills/${skillName}/${relativePath}`;
      await vfs.writeFile(chatId, vfsPath, content);
    }

    return {
      success: true,
      name: skillName,
      path: `skills/${skillName}/SKILL.md`,
    };
  } catch (err) {
    const errorMsg = err instanceof Error ? err.message : String(err);
    return { success: false, error: `Gagal install skill: ${errorMsg}` };
  }
}

export async function installFromContent(chatId: number, name: string, description: string, body: string): Promise<InstallResult> {
  const validationError = validateSkillName(name);
  if (validationError) {
    return { success: false, error: validationError };
  }

  const existing = await vfs.readFile(chatId, `skills/${name}/SKILL.md`);
  if (existing !== null) {
    return { success: false, error: `Skill "${name}" sudah terinstall` };
  }

  const content = buildSkillContent(name, description, body);
  const skillPath = `skills/${name}/SKILL.md`;
  await vfs.writeFile(chatId, skillPath, content);

  return {
    success: true,
    name,
    path: skillPath,
  };
}

export async function updateSkill(chatId: number, name: string, description: string, body: string): Promise<InstallResult> {
  const validationError = validateSkillName(name);
  if (validationError) {
    return { success: false, error: validationError };
  }

  const existing = await vfs.readFile(chatId, `skills/${name}/SKILL.md`);
  if (existing === null) {
    return { success: false, error: `Skill "${name}" tidak ditemukan` };
  }

  const content = buildSkillContent(name, description, body);
  const skillPath = `skills/${name}/SKILL.md`;
  await vfs.writeFile(chatId, skillPath, content);

  return {
    success: true,
    name,
    path: skillPath,
  };
}

export async function migrateOldSkills(chatId: number): Promise<{ migrated: number; errors: string[] }> {
  const result = { migrated: 0, errors: [] as string[] };

  try {
    const entries = await vfs.listDirectory(chatId, 'skills');

    for (const entry of entries) {
      if (!entry.name) continue;

      if (entry.type === 'file' && entry.name.endsWith('.md')) {
        const oldName = entry.name.replace(/\.md$/, '');
        const oldPath = `skills/${entry.name}`;
        const newPath = `skills/${oldName}/SKILL.md`;

        const content = await vfs.readFile(chatId, oldPath);
        if (content === null) continue;

        const existingNew = await vfs.readFile(chatId, newPath);
        if (existingNew !== null) {
          result.errors.push(`Skill "${oldName}" sudah ada di format baru`);
          continue;
        }

        const metadata = parseFrontmatter(content);
        const skillName = metadata.name || oldName;
        const description = metadata.description || `Skill: ${skillName}`;
        const { body } = splitFrontmatter(content);

        const newContent = buildSkillContent(skillName, description, body);
        await vfs.writeFile(chatId, newPath, newContent);
        await vfs.deleteFile(chatId, oldPath);

        result.migrated++;
      }
    }
  } catch (err) {
    result.errors.push(`Error migrating skills: ${err}`);
  }

  return result;
}
