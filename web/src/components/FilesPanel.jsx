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
      <section className="card">
        <h2 className="panel-title">Memory (MEMORY.md) <span className="pt-code">/mem</span></h2>
        {!mem.loaded ? (
          <p style={{ color: '#47845E', fontSize: '.82rem' }}>
            <span className="spinner"></span> Memuat MEMORY.md...
          </p>
        ) : (
          <>
            <textarea
              rows={7}
              value={mem.content}
              placeholder="(belum ada isi — auto-update bot akan mengisinya)"
              onChange={(e) => setMem((m) => ({ ...m, content: e.target.value }))}
            />
            <div className="hint">Kosongkan lalu Simpan untuk menghapus MEMORY.md. Bot tetap bisa meng-update-nya otomatis.</div>
            <button className="btn btn-primary" onClick={saveMemory} disabled={mem.saving}>
              {mem.saving ? 'Menyimpan...' : 'Simpan'}
            </button>
          </>
        )}
      </section>

      <section className="card">
        <h2 className="panel-title">File VFS <span className="pt-code">/fs</span></h2>

        <div className="crumb-row">
          {crumbs.map((c, i) => (
            <span key={i}>
              <a
                href="javascript:void(0)"
                className={i === crumbs.length - 1 ? 'crumb crumb-cur' : 'crumb'}
                onClick={() => navTo(c.path)}
              >
                {c.name}
              </a>
              {i < crumbs.length - 1 && <span className="crumb-sep">/</span>}
            </span>
          ))}
        </div>

        <div className="file-list">
          {loading && (
            <p style={{ color: '#47845E', fontSize: '.82rem' }}>
              <span className="spinner"></span> Memuat folder...
            </p>
          )}
          {!loading && entries.length === 0 && (
            <em style={{ color: '#47845E' }}>Folder kosong.</em>
          )}
          {!loading && sorted.map((e) => {
            const path = cwd.length ? join([...cwd, e.name]) : e.name
            return (
              <div key={path} className="file-row">
                <button
                  className="file-name"
                  onClick={() => (e.type === 'dir' ? navTo([...cwd, e.name]) : openFile(e.name))}
                >
                  <span className={e.type === 'dir' ? 'file-ico dir' : 'file-ico'}>{e.type === 'dir' ? '▸' : ''}</span>
                  {e.name}
                </button>
                <button
                  className="btn btn-danger"
                  style={{ padding: '4px 8px', fontSize: '.75rem', whiteSpace: 'nowrap' }}
                  onClick={() => del(e)}
                  disabled={!!deleting}
                >
                  {deleting === path ? 'Menghapus...' : 'Hapus'}
                </button>
              </div>
            )
          })}
        </div>

        {file && (
          <div style={{ marginTop: 10 }}>
            <label>{file.path}</label>
            <textarea
              rows={10}
              value={file.content}
              onChange={(e) => setFile((f) => ({ ...f, content: e.target.value }))}
            />
            <button className="btn btn-primary" onClick={saveFile} disabled={saving}>
              {saving ? 'Menyimpan...' : 'Simpan'}
            </button>
            <button className="btn btn-secondary" onClick={() => setFile(null)}>Tutup</button>
          </div>
        )}
      </section>
    </>
  )
}
