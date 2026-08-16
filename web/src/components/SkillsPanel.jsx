import { useCallback, useEffect, useState } from 'react'
import { api } from '../api'

export default function SkillsPanel({ showToast, confirm, alert }) {
  const [installed, setInstalled] = useState([])
  const [loaded, setLoaded] = useState(false)
  const [query, setQuery] = useState('')
  const [results, setResults] = useState(null)
  const [searching, setSearching] = useState(false)
  const [installing, setInstalling] = useState('')
  const [deleting, setDeleting] = useState('')

  const loadSkills = useCallback(async () => {
    try {
      const { ok, data } = await api.listSkills()
      if (ok) setInstalled(data.skills || [])
    } catch {} finally { setLoaded(true) }
  }, [])

  useEffect(() => { loadSkills() }, [loadSkills])

  const search = async () => {
    if (!query.trim()) return
    setSearching(true); setResults([])
    try {
      const { ok, data } = await api.searchSkills(query)
      setResults(ok ? (data.results || []).slice(0, 10) : [])
      if (!ok) showToast('Pencarian gagal', false)
    } catch {
      setResults([])
      showToast('Pencarian gagal', false)
    } finally {
      setSearching(false)
    }
  }

  const clearResults = () => {
    setQuery('')
    setResults(null)
  }

  const install = async (target) => {
    setInstalling(target)
    try {
      const { ok, data } = await api.installSkill(target)
      if (ok) {
        await alert({
          title: 'Skill Terpasang',
          message: 'Skill "' + data.name + '" berhasil di-install.',
          confirmLabel: 'OK',
        })
        loadSkills()
      } else {
        await alert({ title: 'Gagal Install', message: data.error || 'Terjadi kesalahan saat menginstall skill.', confirmLabel: 'Tutup', danger: true })
      }
    } catch (e) {
      await alert({ title: 'Error', message: 'Network error: ' + e.message, confirmLabel: 'Tutup', danger: true })
    } finally {
      setInstalling('')
    }
  }

  const del = async (name) => {
    const ok = await confirm({
      title: 'Hapus Skill',
      message: 'Skill "' + name + '" akan dihapus beserta seluruh file-nya. Lanjutkan?',
      confirmLabel: 'Hapus',
      danger: true,
    })
    if (!ok) return
    setDeleting(name)
    try {
      const { ok: r, data } = await api.deleteSkill(name)
      if (r) {
        showToast('Skill "' + name + '" dihapus.')
        loadSkills()
      } else {
        await alert({ title: 'Gagal', message: data.error || 'Terjadi kesalahan.', confirmLabel: 'Tutup', danger: true })
      }
    } catch (e) {
      await alert({ title: 'Error', message: 'Network error: ' + e.message, confirmLabel: 'Tutup', danger: true })
    } finally {
      setDeleting('')
    }
  }

  return (
    <>
      <div className="card">
        <div className="card-head">
          <div className="flex">
            <div className="card-ic"><span className="ms">extension</span></div>
            <div>
              <div className="card-title">Agent Skills</div>
              <div className="card-sub">Cari di registry, install & kelola skill bot</div>
            </div>
          </div>
        </div>
        <div className="card-body">
          <label className="field">Search Skills</label>
          <div className="flex" style={{ gap: 8 }}>
            <input
              type="text"
              value={query}
              placeholder="keyword..."
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && search()}
            />
            <button className="btn btn-primary" onClick={search} disabled={searching || !query.trim()}>
              {searching ? <span className="spinner" /> : <span className="ms">search</span>} Search
            </button>
            <button className="btn btn-secondary" onClick={clearResults} disabled={!query && results === null}>Bersihkan</button>
          </div>

          <div className="mt-16">
            {searching && <div className="muted small"><span className="spinner" /> Mencari skill &quot;{query}&quot;...</div>}
            {!searching && results !== null && results.length === 0 && (
              <div className="muted small">Tidak ada hasil untuk &quot;{query}&quot;.</div>
            )}
            {!searching && results && results.map((r) => (
              <div key={r.target} className="row">
                <div className="row-main">
                  <div className="row-title"><span className="row-name">{r.displayName || r.slug}</span></div>
                  <div className="row-desc">{(r.summary || '').substring(0, 100)}{' '}<code>[{r.registry || ''}]</code></div>
                </div>
                <div className="row-actions">
                  <button className="btn btn-success btn-sm" onClick={() => install(r.target)} disabled={!!installing}>
                    {installing === r.target ? 'Memasang...' : 'Install'}
                  </button>
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>

      <div className="card">
        <div className="card-head">
          <div>
            <div className="card-title">Installed Skills</div>
            <div className="card-sub">{loaded ? installed.length + ' terpasang' : 'Memuat...'}</div>
          </div>
        </div>
        <div className="card-body-sm">
          {!loaded && <div className="muted small"><span className="spinner" /> Memuat daftar skill...</div>}
          {loaded && installed.length === 0 && <div className="empty-state"><span className="ms">extension</span><span>Belum ada skill terpasang.</span></div>}
          {loaded && installed.map((s) => (
            <div key={s.name} className="row">
              <div className="row-main">
                <div className="row-title"><span className="row-name">{s.name}</span></div>
                <div className="row-desc">{s.description || ''}</div>
              </div>
              <div className="row-actions">
                <button className="btn btn-ghost btn-sm" style={{ color: 'var(--danger)' }} onClick={() => del(s.name)} disabled={!!deleting}>
                  <span className="ms">delete</span> {deleting === s.name ? 'Menghapus...' : 'Delete'}
                </button>
              </div>
            </div>
          ))}
        </div>
      </div>

      {installing && (
        <div className="toast-wrap" style={{ top: 'auto', bottom: 16, right: 16 }}>
          <div className="toast toast-info">
            <span className="ms">downloading</span>
            <div>Menginstall skill &quot;{installing}&quot;...</div>
          </div>
        </div>
      )}
    </>
  )
}