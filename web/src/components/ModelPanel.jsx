import { useState } from 'react'
import { api } from '../api'

export default function ModelPanel({ model, setModel }) {
  const [loading, setLoading] = useState(false)
  const [models, setModels] = useState([])
  const [hint, setHint] = useState('')

  const loadModels = async () => {
    setLoading(true); setModels([]); setHint('')
    try {
      const { ok, status, data } = await api.getModels()
      if (!ok) { setHint(data.error || ('HTTP ' + status)); return }
      if (!data.models || data.models.length === 0) {
        setHint('API tidak mengembalikan daftar model.')
        return
      }
      setModels(data.models)
      setHint(data.models.length + ' model tersedia di ' + data.baseUrl)
    } catch (e) {
      setHint('Network error: ' + e.message)
    } finally {
      setLoading(false)
    }
  }

  return (
    <section className="card">
      <h2 className="panel-title">Model</h2>
      <label>Daftar Model dari API</label>
      <div className="model-row">
        <input
          value={model}
          placeholder="gpt-4o"
          onChange={(e) => setModel(e.target.value)}
          style={{ flex: 1 }}
        />
        <button className="btn btn-secondary" onClick={loadModels} disabled={loading}>
          {loading ? 'Loading...' : 'Load Models'}
        </button>
      </div>
      <select
        value={models.includes(model) ? model : ''}
        onChange={(e) => { if (e.target.value) setModel(e.target.value) }}
      >
        <option value="">-- pilih model dari daftar --</option>
        {models.map((m) => (
          <option key={m} value={m}>{m}</option>
        ))}
      </select>
      <div className="hint">{hint}</div>
    </section>
  )
}
