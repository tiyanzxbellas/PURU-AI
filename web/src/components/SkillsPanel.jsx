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
        await alert({
          title: 'Gagal Install',
          message: data.error || 'Terjadi kesalahan saat menginstall skill.',
          confirmLabel: 'Tutup',
          danger: true,
        })
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
      <section className="card">
        <h2 className="panel-title">Skills <span className="pt-code">/skills</span></h2>
        <label>Search Skills</label>
        <div className="search-row">
          <input
            value={query}
            placeholder="keyword..."
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && search()}
          />
          <button className="btn btn-primary" onClick={search} disabled={searching || !query.trim()}>Search</button>
          <button className="btn btn-secondary" onClick={clearResults} disabled={!query && results === null}>Bersihkan</button>
        </div>
        <div style={{ marginTop: 8 }}>
          {searching && (
            <p style={{ color: '#47845E', fontSize: '.82rem' }}>
              <span className="spinner"></span> Mencari skill "{query}"...
            </p>
          )}
          {!searching && results !== null && results.length === 0 && (
            <p style={{ color: '#47845E', fontSize: '.82rem' }}>
              Tidak ada hasil untuk "{query}".
            </p>
          )}
          {!searching && results && results.map((r) => (
            <div key={r.target} className="skill-item">
              <div>
                <span className="skill-name">{r.displayName || r.slug}</span><br />
                <span className="skill-desc">
                  {(r.summary || '').substring(0, 100)}{' '}
                  <code>[{r.registry || ''}]</code>
                </span>
              </div>
              <button
                className="btn btn-success"
                style={{ padding: '4px 8px', fontSize: '.75rem', whiteSpace: 'nowrap' }}
                onClick={() => install(r.target)}
                disabled={!!installing}
              >
                {installing === r.target ? 'Memasang...' : 'Install'}
              </button>
            </div>
          ))}
        </div>
      </section>

      <section className="card">
        <label>Installed Skills</label>
        <div>
          {!loaded && (
            <p style={{ color: '#47845E', fontSize: '.82rem' }}>
              <span className="spinner"></span> Memuat daftar skill...
            </p>
          )}
          {loaded && installed.length === 0 && (
            <em style={{ color: '#47845E' }}>Belum ada skill terpasang.</em>
          )}
          {loaded && installed.map((s) => (
            <div key={s.name} className="skill-item">
              <div>
                <span className="skill-name">{s.name}</span><br />
                <span className="skill-desc">{s.description || ''}</span>
              </div>
              <button
                className="btn btn-danger"
                style={{ padding: '4px 8px', fontSize: '.75rem', whiteSpace: 'nowrap' }}
                onClick={() => del(s.name)}
                disabled={!!deleting}
              >
                {deleting === s.name ? 'Menghapus...' : 'Delete'}
              </button>
            </div>
          ))}
        </div>
      </section>

      {installing && (
        <div className="install-bar">
          <span className="spinner"></span> Menginstall skill "{installing}"...
        </div>
      )}
    </>
  )
}
