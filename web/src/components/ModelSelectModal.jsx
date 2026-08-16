import { useEffect, useMemo, useState } from 'react'
import { api } from '../api'

// ModelSelectModal ala 9router: picker model yang dikelompokkan per provider.
// Model diambil ONLINE dari catalog provider (/models) — tidak ada daftar model
// buatan sendiri. Klik chip = add, klik lagi = remove.
export default function ModelSelectModal({ isOpen, onClose, onSelect, onDeselect, addedModelValues = [], title = 'Select Model' }) {
  const [search, setSearch] = useState('')
  const [providers, setProviders] = useState([])
  const [statuses, setStatuses] = useState({})
  const [loading, setLoading] = useState(false)
  const [custom, setCustom] = useState('')

  useEffect(() => {
    if (!isOpen) return
    let cancelled = false
    setLoading(true)
    ;(async () => {
      try {
        const { ok, data } = await api.listProviders()
        if (!ok || cancelled) return
        const list = data.providers || []
        setProviders(list)
        const st = {}
        await Promise.all(list.map(async (p) => {
          try {
            const { ok: ok2, data: d } = await api.checkProvider({ providerId: p.id })
            if (ok2 && !cancelled) st[p.id] = { online: !!d.online, models: d.models || [], error: d.error || '' }
          } catch {}
        }))
        if (!cancelled) setStatuses(st)
      } catch {
      } finally {
        if (!cancelled) setLoading(false)
      }
    })()
    return () => { cancelled = true }
  }, [isOpen])

  const groups = useMemo(() => {
    const out = []
    for (const p of providers) {
      const st = statuses[p.id]
      if (!st) continue
      let models = st.models || []
      // Provider tanpa /models: placeholder "prefix/model-id" (dashed) agar tetap bisa dipilih.
      if (models.length === 0) {
        models = [{ id: p.prefix + '/model-id', name: p.prefix + '/model-id', isPlaceholder: true }]
      }
      const q = search.trim().toLowerCase()
      if (q) {
        const nameMatch = p.name.toLowerCase().includes(q)
        models = models.filter((m) => m.name.toLowerCase().includes(q) || m.id.toLowerCase().includes(q))
        if (models.length === 0 && !nameMatch) continue
      }
      out.push({ provider: p, models })
    }
    return out
  }, [providers, statuses, search])

  const toggle = (model) => {
    const value = model.value || model.name || model.id
    if (addedModelValues.includes(value)) {
      if (onDeselect) onDeselect(value)
    } else {
      onSelect(value)
    }
  }

  const addCustom = () => {
    const v = custom.trim()
    if (!v) return
    onSelect(v)
    setCustom('')
  }

  const isAdded = (v) => addedModelValues.includes(v)

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal" style={{ maxWidth: 620 }} onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <h3>{title}</h3>
          <button className="icon-btn" onClick={onClose} aria-label="Tutup">
            <span className="ms">close</span>
          </button>
        </div>
        <div className="modal-body">
          <div className="hint" style={{ marginBottom: 10 }}>
            <span className="ms" style={{ fontSize: 14, verticalAlign: -2 }}>info</span>{' '}
            Klik untuk menambahkan, klik lagi untuk menghapus. Model diambil online dari provider (/models).
          </div>

          <div className="flex" style={{ gap: 8, marginBottom: 12 }}>
            <input type="text" value={search} placeholder="Cari..." onChange={(e) => setSearch(e.target.value)} style={{ flex: 1 }} />
            <button className="btn btn-ghost btn-sm" onClick={() => setSearch('')} title="Bersihkan">
              <span className="ms">close</span>
            </button>
          </div>

          {loading ? (
            <div className="empty-state"><span className="spinner" /> Memuat model...</div>
          ) : groups.length === 0 ? (
            <div className="empty-state">
              <span className="ms">search_off</span>
              <p>Tidak ada model ditemukan</p>
              <span>Tambahkan provider di halaman Providers untuk melihat model-nya di sini.</span>
            </div>
          ) : (
            <div style={{ maxHeight: '55vh', overflowY: 'auto' }} className="flex-col">
              {groups.map(({ provider, models }) => (
                <div key={provider.id}>
                  <div className="flex" style={{ marginBottom: 6 }}>
                    <div className="card-ic" style={{ width: 24, height: 24 }}><span className="ms" style={{ fontSize: 14 }}>dns</span></div>
                    <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--primary)' }}>{provider.name}</div>
                    <span className="muted small">({models.length})</span>
                  </div>
                  <div className="flex" style={{ gap: 6, flexWrap: 'wrap', marginBottom: 12 }}>
                    {models.map((m) => {
                      const v = m.value || (provider.prefix + '/' + m.id)
                      const added = isAdded(v)
                      const isPh = m.isPlaceholder
                      return (
                        <button
                          key={v}
                          onClick={() => toggle({ ...m, value: v })}
                          title={isPh ? 'Pilih untuk dipakai, lalu edit model id di daftar' : undefined}
                          className={'chip' + (added ? ' badge-primary' : '')}
                          style={{
                            border: added ? '1px solid var(--primary)' : '1px solid var(--border-subtle)',
                            color: added ? 'var(--primary)' : isPh ? 'var(--text-muted)' : 'var(--text-main)',
                            fontStyle: isPh ? 'italic' : undefined,
                            borderStyle: isPh ? 'dashed' : 'solid',
                            cursor: 'pointer',
                          }}
                        >
                          {added && !isPh && <span className="ms" style={{ fontSize: 12 }}>check</span>}
                          {isPh && <span className="ms" style={{ fontSize: 12 }}>edit</span>}
                          {m.name || m.id}
                        </button>
                      )
                    })}
                  </div>
                </div>
              ))}
            </div>
          )}

          <div className="flex mt-16" style={{ gap: 8, borderTop: '1px solid var(--border-subtle)', paddingTop: 12 }}>
            <input
              type="text"
              value={custom}
              placeholder="prefix/model-id (ketik manual bila /models kosong)..."
              onChange={(e) => setCustom(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && addCustom()}
              style={{ flex: 1 }}
            />
            <button className="btn btn-secondary btn-sm" onClick={addCustom} disabled={!custom.trim()}>
              <span className="ms">add</span> Add
            </button>
          </div>
        </div>
        <div className="modal-foot">
          <button className="btn btn-secondary" onClick={onClose}>Selesai</button>
        </div>
      </div>
    </div>
  )
}