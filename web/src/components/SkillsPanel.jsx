import { useCallback, useEffect, useState } from 'react'
import { api } from '../api'

export default function SkillsPanel({ showStatus }) {
  const [installed, setInstalled] = useState([])
  const [loaded, setLoaded] = useState(false)
  const [query, setQuery] = useState('')
  const [results, setResults] = useState(null)
  const [searching, setSearching] = useState(false)
  const [installing, setInstalling] = useState('')

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
    } catch {
      setResults([])
    } finally {
      setSearching(false)
    }
  }

  const install = async (target) => {
    setInstalling(target)
    try {
      const { ok, data } = await api.installSkill(target)
      if (ok) {
        showStatus('Skill "' + data.name + '" installed!')
        loadSkills()
      } else {
        showStatus('Install failed: ' + (data.error || 'unknown'), false)
      }
    } catch (e) {
      showStatus('Network error: ' + e.message, false)
    } finally {
      setInstalling('')
    }
  }

  const del = async (name) => {
    if (!window.confirm('Delete skill "' + name + '"?')) return
    try {
      const { ok, data } = await api.deleteSkill(name)
      if (ok) {
        showStatus('Skill "' + name + '" deleted.')
        loadSkills()
      } else {
        showStatus(data.error || 'Delete failed', false)
      }
    } catch (e) {
      showStatus('Network error: ' + e.message, false)
    }
  }

  return (
    <>
      <section className="card">
        <h2 className="panel-title">Skills</h2>
        <label>Search Skills</label>
        <div className="search-row">
          <input
            value={query}
            placeholder="keyword..."
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && search()}
          />
          <button className="btn btn-primary" onClick={search}>Search</button>
        </div>
        <div style={{ marginTop: 8 }}>
          {searching && (
            <p style={{ color: '#64748b', fontSize: '.82rem' }}>
              <span className="spinner"></span> Searching...
            </p>
          )}
          {!searching && results !== null && results.length === 0 && (
            <p style={{ color: '#64748b', fontSize: '.82rem' }}>No results.</p>
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
                style={{ padding: '4px 8px', fontSize: '.75rem' }}
                onClick={() => install(r.target)}
              >
                Install
              </button>
            </div>
          ))}
        </div>
      </section>

      <section className="card">
        <label>Installed Skills</label>
        <div>
          {!loaded && (
            <em style={{ color: '#64748b' }}>Loading...</em>
          )}
          {loaded && installed.length === 0 && (
            <em style={{ color: '#64748b' }}>No skills installed.</em>
          )}
          {loaded && installed.map((s) => (
            <div key={s.name} className="skill-item">
              <div>
                <span className="skill-name">{s.name}</span><br />
                <span className="skill-desc">{s.description || ''}</span>
              </div>
              <button
                className="btn btn-danger"
                style={{ padding: '4px 8px', fontSize: '.75rem' }}
                onClick={() => del(s.name)}
              >
                Delete
              </button>
            </div>
          ))}
        </div>
      </section>

      {installing && (
        <div className="install-bar">
          {'Installing ' + installing + '...'}
        </div>
      )}
    </>
  )
}
