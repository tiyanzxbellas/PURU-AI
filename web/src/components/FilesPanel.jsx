import { useCallback, useEffect, useState } from 'react'
import { api } from '../api'

const join = (parts) => parts.join('/')

export default function FilesPanel({ showToast, confirm, alert }) {
  const [mem, setMem] = useState({ loaded: false, content: '', saving: false })
  const [cwd, setCwd] = useState([])
  const [entries, setEntries] = useState([])
  const [loading, setLoading] = useState(false)
  const [file, setFile] = useState(null)
  const [saving, setSaving] = useState(false)
  const [deleting, setDeleting] = useState('')

  const loadMemory = useCallback(async () => {
    try {
      const { ok, data } = await api.getMemory()
      if (ok) setMem((m) => ({ ...m, content: data.exists ? data.content : '', loaded: true }))
    } catch {
      setMem((m) => ({ ...m, loaded: true }))
    }
  }, [])

  const loadDir = useCallback(async (dir, silent) => {
    if (!silent) setLoading(true)
    try {
      const { ok, data } = await api.listFiles(join(dir))
      if (ok) {
        setEntries(data.entries || [])
        setCwd(dir)
      } else {
        showToast('Gagal memuat folder', false)
      }
    } catch {
      showToast('Gagal memuat folder', false)
    } finally {
      setLoading(false)
    }
  }, [showToast])

  useEffect(() => { loadMemory() }, [loadMemory])
  useEffect(() => { loadDir([], true) }, [loadDir])

  const openFile = async (name) => {
    const path = cwd.length ? join([...cwd, name]) : name
    try {
      const { ok, data } = await api.readFile(path)
      if (ok) setFile({ path, content: data.content })
      else showToast(data.error || 'Gagal membaca file', false)
    } catch {
      showToast('Gagal membaca file', false)
    }
  }

  const saveFile = async () => {
    if (!file) return
    setSaving(true)
    try {
      const { ok, data } = await api.writeFile(file.path, file.content)
      if (ok) {
        showToast('File "' + file.path + '" disimpan.')
        loadDir(cwd, true)
      } else {
        showToast(data.error || 'Gagal menyimpan file', false)
      }
    } catch {
      showToast('Gagal menyimpan file', false)
    } finally {
      setSaving(false)
    }
  }

  const saveMemory = async () => {
    setMem((m) => ({ ...m, saving: true }))
    try {
      const { ok, data } = await api.saveMemory(mem.content)
      if (ok) {
        showToast(mem.content.trim() ? 'MEMORY.md disimpan.' : 'MEMORY.md dihapus.')
        loadMemory()
      } else {
        showToast(data.error || 'Gagal menyimpan MEMORY.md', false)
      }
    } catch {
      showToast('Gagal menyimpan MEMORY.md', false)
    } finally {
      setMem((m) => ({ ...m, saving: false }))
    }
  }

  const del = async (e) => {
    const isDir = e.type === 'dir'
    const path = cwd.length ? join([...cwd, e.name]) : e.name
    const ok = await confirm({
      title: isDir ? 'Hapus Folder' : 'Hapus File',
      message: (isDir
        ? 'Folder "' + path + '" beserta seluruh isinya akan dihapus. Lanjutkan?'
        : 'File "' + path + '" akan dihapus. Lanjutkan?'),
      confirmLabel: 'Hapus',
      danger: true,
    })
    if (!ok) return
    setDeleting(path)
    try {
      const { ok: r, data } = await api.deleteFile(path, isDir ? 'dir' : 'file')
      if (r) {
        showToast((isDir ? 'Folder "' : 'File "') + path + '" dihapus.')
        if (file && file.path === path) setFile(null)
        loadDir(cwd, true)
      } else {
        await alert({ title: 'Gagal', message: data.error || 'Terjadi kesalahan.', confirmLabel: 'Tutup', danger: true })
      }
    } catch (e) {
      await alert({ title: 'Error', message: 'Network error: ' + e.message, confirmLabel: 'Tutup', danger: true })
    } finally {
      setDeleting('')
    }
  }

  const navTo = (dir) => {
    if (file) setFile(null)
    loadDir(dir)
  }

  const sorted = [...entries].sort((a, b) => {
    if (a.type !== b.type) return a.type === 'dir' ? -1 : 1
    return a.name < b.name ? -1 : a.name > b.name ? 1 : 0
  })

  const crumbs = [
    { name: '~', path: [] },
    ...cwd.map((seg, i) => ({ name: seg, path: cwd.slice(0, i + 1) })),
  ]
  return (
    <>
      <div className="card">
        <div className="card-head">
          <div className="flex">
            <div className="card-ic"><span className="ms">memory</span></div>
            <div>
              <div className="card-title">Memory (MEMORY.md)</div>
              <div className="card-sub">Kosongkan lalu Simpan untuk menghapus; bot tetap auto-update</div>
            </div>
          </div>
        </div>
        <div className="card-body">
          {!mem.loaded ? (
            <div className="muted small"><span className="spinner" /> Memuat MEMORY.md...</div>
          ) : (
            <>
              <textarea rows={7} value={mem.content} placeholder="(belum ada isi — auto-update bot akan mengisinya)" onChange={(e) => setMem((m) => ({ ...m, content: e.target.value }))} />
              <div className="flex mt-16" style={{ justifyContent: 'flex-end' }}>
                <button className="btn btn-primary" onClick={saveMemory} disabled={mem.saving}>
                  <span className="ms">save</span> {mem.saving ? 'Menyimpan...' : 'Simpan'}
                </button>
              </div>
            </>
          )}
        </div>
      </div>

      <div className="card">
        <div className="card-head">
          <div className="flex">
            <div className="card-ic"><span className="ms">folder_open</span></div>
            <div>
              <div className="card-title">File VFS</div>
              <div className="card-sub">Virtual file system per-user di RTDB</div>
            </div>
          </div>
        </div>
        <div className="card-body-sm">
          <div className="flex" style={{ flexWrap: 'wrap', gap: 6, padding: '2px 4px 10px' }}>
            {crumbs.map((c, i) => (
              <span key={i} className="flex" style={{ gap: 6 }}>
                {i > 0 && <span className="muted small">/</span>}
                <button
                  className={'chip' + (i === crumbs.length - 1 ? '' : '')}
                  style={{ border: 'none', cursor: i === crumbs.length - 1 ? 'default' : 'pointer' }}
                  onClick={() => i < crumbs.length - 1 && navTo(c.path)}
                >
                  <span className="ms" style={{ fontSize: 12 }}>{i === crumbs.length - 1 ? 'folder' : 'chevron_right'}</span>
                  {c.name}
                </button>
              </span>
            ))}
          </div>

          {loading && <div className="muted small"><span className="spinner" /> Memuat folder...</div>}
          {!loading && entries.length === 0 && <div className="empty-state"><span className="ms">folder_open</span><span>Folder kosong.</span></div>}
          {!loading && sorted.map((e) => {
            const path = cwd.length ? join([...cwd, e.name]) : e.name
            return (
              <div key={path} className="row">
                <button
                  className="chip"
                  style={{ border: 'none', flex: 1, textAlign: 'left', color: 'var(--text-main)' }}
                  onClick={() => (e.type === 'dir' ? navTo([...cwd, e.name]) : openFile(e.name))}
                >
                  <span className="ms" style={{ fontSize: 15, color: e.type === 'dir' ? 'var(--info)' : 'var(--text-muted)' }}>
                    {e.type === 'dir' ? 'folder' : 'description'}
                  </span>
                  {e.name}
                </button>
                <div className="row-actions">
                  <button className="btn btn-ghost btn-sm" style={{ color: 'var(--danger)' }} onClick={() => del(e)} disabled={!!deleting}>
                    <span className="ms">delete</span> {deleting === path ? 'Menghapus...' : 'Hapus'}
                  </button>
                </div>
              </div>
            )
          })}
        </div>

        {file && (
          <div className="card-body" style={{ borderTop: '1px solid var(--border-subtle)' }}>
            <label className="field">{file.path}</label>
            <textarea rows={10} value={file.content} onChange={(e) => setFile((f) => ({ ...f, content: e.target.value }))} />
            <div className="flex mt-16" style={{ justifyContent: 'flex-end' }}>
              <button className="btn btn-secondary" onClick={() => setFile(null)}>Tutup</button>
              <button className="btn btn-primary" onClick={saveFile} disabled={saving}>
                <span className="ms">save</span> {saving ? 'Menyimpan...' : 'Simpan'}
              </button>
            </div>
          </div>
        )}
      </div>
    </>
  )
}