import * as vfs from './vfs.js';

export interface SkillMetadata {
  name: string;
  description: string;
  homepage?: string;
  metadata?: Record<string, any>;
}

export interface SkillInfo {
  name: string;
  path: string;
  description: string;
  homepage?: string;
  metadata?: Record<string, any>;
}

const NAME_PATTERN = /^[a-zA-Z0-9]+(-[a-zA-Z0-9]+)*$/;
const MAX_NAME_LENGTH = 64;
const MAX_DESCRIPTION_LENGTH = 1024;

export function validateSkillName(name: string): string | null {
  const trimmed = name.trim();
  if (!trimmed) return 'Nama skill harus diisi';
  if (trimmed.length > MAX_NAME_LENGTH) return `Nama skill maksimal ${MAX_NAME_LENGTH} karakter`;
  if (!NAME_PATTERN.test(trimmed)) return 'Nama skill hanya boleh berisi huruf, angka, dan hyphen';
  return null;
}

export function splitFrontmatter(content: string): { frontmatter: string; body: string } {
  const lines = content.split('\n');
  if (lines.length === 0 || lines[0].trim() !== '---') {
    return { frontmatter: '', body: content };
  }

  let endIndex = -1;
  for (let i = 1; i < lines.length; i++) {
    if (lines[i].trim() === '---') {
      endIndex = i;
      break;
    }
  }

  if (endIndex === -1) {
    return { frontmatter: '', body: content };
  }

  const frontmatter = lines.slice(1, endIndex).join('\n');
  const body = lines.slice(endIndex + 1).join('\n').trimStart();
  return { frontmatter, body };
}

export function parseSimpleYAML(yamlContent: string): Record<string, string> {
  const result: Record<string, string> = {};
  const lines = yamlContent.split('\n');

  for (const line of lines) {
    const match = line.match(/^(\w+):\s*(.+)$/);
    if (match) {
      const [, key, value] = match;
      result[key] = value.replace(/^["']|["']$/g, '').trim();
    }
  }

  return result;
}

export function parseFrontmatter(content: string): SkillMetadata {
  const { frontmatter, body } = splitFrontmatter(content);

  const dirMatch = body.match(/^#\s+(.+)/m);
  const descMatch = body.match(/^#\s+.+\n\n(.+)/m);

  let name = '';
  let description = '';

  if (dirMatch) {
    const title = dirMatch[1].trim();
    if (NAME_PATTERN.test(title) && title.length <= MAX_NAME_LENGTH) {
      name = title;
    }
  }

  if (descMatch) {
    description = descMatch[1].trim().split('\n')[0];
  }

  if (!frontmatter) {
    return { name, description };
  }

  const yamlMeta = parseSimpleYAML(frontmatter);

  if (yamlMeta.name && NAME_PATTERN.test(yamlMeta.name)) {
    name = yamlMeta.name;
  }

  if (yamlMeta.description) {
    description = yamlMeta.description;
  }

  const homepage = yamlMeta.homepage || undefined;

  let metadata: Record<string, any> | undefined;
  if (yamlMeta.metadata) {
    try {
      metadata = JSON.parse(yamlMeta.metadata);
    } catch {
      // Ignore invalid JSON
    }
  }

  return { name, description, homepage, metadata };
}

export async function listSkills(chatId: number): Promise<SkillInfo[]> {
  const skills: SkillInfo[] = [];
  const seen = new Set<string>();

  const entries = await vfs.listDirectory(chatId, 'skills');

  for (const entry of entries) {
    if (!entry.name) continue;

    let content: string | null = null;
    let skillPath: string;

    if (entry.type === 'dir') {
      skillPath = `skills/${entry.name}/SKILL.md`;
      content = await vfs.readFile(chatId, skillPath);
    } else if (entry.type === 'file' && entry.name.endsWith('.md')) {
      skillPath = `skills/${entry.name}`;
      content = await vfs.readFile(chatId, skillPath);
    } else {
      continue;
    }

    if (content === null) continue;

    const metadata = parseFrontmatter(content);
    const skillName = metadata.name || entry.name.replace(/\.md$/, '');

    if (seen.has(skillName)) continue;
    seen.add(skillName);

    if (metadata.description.length > MAX_DESCRIPTION_LENGTH) {
      metadata.description = metadata.description.substring(0, MAX_DESCRIPTION_LENGTH) + '...';
    }

    skills.push({
      name: skillName,
      path: skillPath,
      description: metadata.description,
      homepage: metadata.homepage,
      metadata: metadata.metadata,
    });
  }

  return skills;
}

export async function loadSkill(chatId: number, name: string): Promise<string | null> {
  const validationError = validateSkillName(name);
  if (validationError) return null;

  let content = await vfs.readFile(chatId, `skills/${name}/SKILL.md`);

  if (content === null) {
    content = await vfs.readFile(chatId, `skills/${name}.md`);
  }

  if (content === null) return null;

  const { body } = splitFrontmatter(content);
  return body;
}

export async function loadSkillWithMetadata(chatId: number, name: string): Promise<{ content: string; metadata: SkillMetadata } | null> {
  const validationError = validateSkillName(name);
  if (validationError) return null;

  let content = await vfs.readFile(chatId, `skills/${name}/SKILL.md`);
  let skillPath = `skills/${name}/SKILL.md`;

  if (content === null) {
    content = await vfs.readFile(chatId, `skills/${name}.md`);
    skillPath = `skills/${name}.md`;
  }

  if (content === null) return null;

  const metadata = parseFrontmatter(content);
  const { body } = splitFrontmatter(content);

  return { content: body, metadata };
}

export async function listSkillFiles(chatId: number, name: string): Promise<string[]> {
  const validationError = validateSkillName(name);
  if (validationError) return [];

  const dirEntries = await vfs.listDirectory(chatId, `skills/${name}`);
  if (dirEntries.length > 0) {
    return dirEntries.map(e => e.name).filter(Boolean);
  }

  const fileContent = await vfs.readFile(chatId, `skills/${name}.md`);
  if (fileContent !== null) {
    return [`${name}.md`];
  }

  return [];
}

export async function buildSkillsSummary(chatId: number): Promise<string> {
  const skills = await listSkills(chatId);

  if (skills.length === 0) return '';

  const lines: string[] = ['<skills>'];

  for (const skill of skills) {
    const escapedName = escapeXML(skill.name);
    const escapedDesc = escapeXML(skill.description);
    const escapedPath = escapeXML(skill.path);

    lines.push('  <skill>');
    lines.push(`    <name>${escapedName}</name>`);
    lines.push(`    <description>${escapedDesc}</description>`);
    lines.push(`    <location>${escapedPath}</location>`);
    if (skill.homepage) {
      lines.push(`    <homepage>${escapeXML(skill.homepage)}</homepage>`);
    }
    lines.push('  </skill>');
  }

  lines.push('</skills>');

  return lines.join('\n');
}

function escapeXML(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&apos;');
}

export async function deleteSkill(chatId: number, name: string): Promise<boolean> {
  const validationError = validateSkillName(name);
  if (validationError) return false;

  const skillDir = `skills/${name}`;
  const dirEntries = await vfs.listDirectory(chatId, skillDir);

  let deleted = false;
  for (const entry of dirEntries) {
    if (entry.name) {
      const result = await vfs.deleteFile(chatId, `${skillDir}/${entry.name}`);
      if (result) deleted = true;
    }
  }

  if (deleted) {
    await vfs.deleteFile(chatId, skillDir);
    return true;
  }

  const oldFile = `skills/${name}.md`;
  const oldContent = await vfs.readFile(chatId, oldFile);
  if (oldContent !== null) {
    await vfs.deleteFile(chatId, oldFile);
    return true;
  }

  return false;
}

export async function skillExists(chatId: number, name: string): Promise<boolean> {
  const content = await vfs.readFile(chatId, `skills/${name}/SKILL.md`);
  if (content !== null) return true;

  const oldContent = await vfs.readFile(chatId, `skills/${name}.md`);
  return oldContent !== null;
}

export function buildSkillContent(name: string, description: string, body: string, homepage?: string, metadata?: Record<string, any>): string {
  const lines: string[] = ['---'];

  lines.push(`name: ${name}`);
  lines.push(`description: "${description.replace(/"/g, '\\"')}"`);

  if (homepage) {
    lines.push(`homepage: ${homepage}`);
  }

  if (metadata) {
    lines.push(`metadata: ${JSON.stringify(metadata)}`);
  }

  lines.push('---');
  lines.push('');
  lines.push(`# ${name}`);
  lines.push('');
  lines.push(body);

  return lines.join('\n');
}
