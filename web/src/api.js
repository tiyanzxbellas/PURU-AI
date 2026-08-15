const PATH = location.pathname.replace(/\/$/, '')
const API = PATH + '/api'

async function j(method, url, body) {
  const opts = { method, headers: {} }
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json'
    opts.body = JSON.stringify(body)
  }
  const r = await fetch(url, opts)
  let d = {}
  try { d = await r.json() } catch {}
  return { ok: r.ok, status: r.status, data: d }
}

export const api = {
  getConfig: () => j('GET', API + '/config'),
  saveConfig: (body) => j('POST', API + '/config', body),
  clearConfig: () => j('POST', API + '/config/clear'),
  listSkills: () => j('GET', API + '/skills/list'),
  searchSkills: (q) => j('POST', API + '/skills/search', { query: q }),
  installSkill: (target) => j('POST', API + '/skills/install', { target }),
  deleteSkill: (name) => j('POST', API + '/skills/delete', { name }),
  getMemory: () => j('GET', API + '/memory'),
  saveMemory: (content) => j('POST', API + '/memory', { content }),
  listFiles: (path) => j('GET', API + '/files/list?path=' + encodeURIComponent(path || '')),
  readFile: (path) => j('GET', API + '/files/read?path=' + encodeURIComponent(path)),
  writeFile: (path, content) => j('POST', API + '/files/write', { path, content }),
  deleteFile: (path, type) => j('POST', API + '/files/delete', { path, type }),
}
