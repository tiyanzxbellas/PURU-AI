import { useEffect, useState } from 'react'

const STORAGE_KEY = 'puru_models'

function loadSaved() {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    const arr = raw ? JSON.parse(raw) : []
    return Array.isArray(arr) ? arr.filter((m) => typeof m === 'string' && m.trim()) : []
  } catch {
    return []
  }
}

export default function ModelPanel({ model, onModelChange, showToast }) {
  const [input, setInput] = useState('')
  const [saved, setSaved] = useState([])

  useEffect(() => { setSaved(loadSaved()) }, [])

  const persist = (list) => {
    setSaved(list)
    localStorage.setItem(STORAGE_KEY, JSON.stringify(list))
  }

  const apply = () => {
    const name = input.trim()
    if (!name) return
    if (!saved.includes(name)) {
      persist([name, ...saved])
      showToast('Model "' + name + '" ditambahkan ke daftar')
    }
    setInput('')
    onModelChange(name)
  }

  const pick = (name) => onModelChange(name)

  const remove = (name) => {
    persist(saved.filter((m) => m !== name))
    if (model === name) onModelChange('')
    showToast('Model "' + name + '" dihapus dari daftar')
  }

  return (
    <section className="card">
      <h2 className="panel-title">Model <span className="pt-code">/model</span></h2>
      <label>Nama Model</label>
      <div className="model-row">
        <input
          value={input}
          placeholder="mis. gpt-4o"
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && apply()}
          style={{ flex: 1 }}
        />
        <button className="btn btn-success" onClick={apply} disabled={!input.trim()}>Terapkan</button>
      </div>
      <div className="hint">
        Terapkan/Pilih langsung menyimpan model ke config bot. Daftar model tersimpan di localStorage browser ini.
      </div>
      <label>Daftar Model Siap Pakai</label>
      {saved.length === 0 ? (
        <em style={{ color: '#47845E', fontSize: '.85rem' }}>
          Belum ada model. Masukkan nama lalu klik Terapkan.
        </em>
      ) : saved.map((m) => (
        <div key={m} className={'skill-item' + (m === model ? ' selected' : '')}>
          <div>
            <span className="skill-name">{m}</span>
            {m === model && <span className="skill-desc"> (aktif)</span>}
          </div>
          <div style={{ whiteSpace: 'nowrap' }}>
            <button
              className="btn btn-primary"
              style={{ padding: '4px 8px', fontSize: '.75rem' }}
              onClick={() => pick(m)}
            >
              Pilih
            </button>
            <button
              className="btn btn-danger"
              style={{ padding: '4px 8px', fontSize: '.75rem' }}
              onClick={() => remove(m)}
            >
              Hapus
            </button>
          </div>
        </div>
      ))}
    </section>
  )
}
