import { useCallback, useEffect, useState } from 'react'
import { api } from '../api'
import ModelSelectModal from './ModelSelectModal'

const STRATEGIES = [
  { value: 'fallback', label: 'Fallback — try in order' },
  { value: 'round-robin', label: 'Round Robin — rotate' },
  { value: 'fusion', label: 'Fusion — panel + judge' },
]

const EMPTY = { id: '', name: '', models: [] }

export default function CombosPanel({ showToast, confirm, alert }) {
  const [combos, setCombos] = useState([])
  const [active, setActive] = useState('')
  const [loading, setLoading] = useState(true)
  const [draft, setDraft] = useState(null)
  const [strategy, setStrategy] = useState('fallback')
  const [models, setModels] = useState([])
  const [pickerOpen, setPickerOpen] = useState(false)
  const [saving, setSaving] = useState(false)

  const load = useCallback(async () => {
    try {
      const { ok, data } = await api.listCombos()
      if (ok) {
        setCombos(data.combos || [])
        setActive(data.active || '')
      }
    } catch {
      showToast('Gagal memuat combos', false)
    } finally {
      setLoading(false)
    }
  }, [showToast])

  useEffect(() => { load() }, [load])

  const openNew = () => { setDraft(EMPTY); setModels([]); setStrategy('fallback'); setPickerOpen(false) }
  const openEdit = (c) => { setDraft(c); setModels([...(c.models || [])]); setStrategy(c.strategy || 'fallback'); setPickerOpen(false) }

  const removeModel = (m) => setModels(models.filter((x) => x !== m))

  const save = async () => {
    if (!draft) return
    const name = (draft.name || '').trim()
    if (!name) { showToast('Nama combo wajib diisi', false); return }
    setSaving(true)
    try {
      const { ok, data } = await api.saveCombo({ id: draft.id, name, models, strategy })
      if (ok) {
        showToast('Combo "' + name + '" disimpan')
        setDraft(null)
        load()
      } else {
        showToast(data.error || 'Gagal menyimpan combo', false)
      }
    } catch {
      showToast('Gagal menyimpan combo', false)
    } finally {
      setSaving(false)
    }
  }

  const del = async (c) => {
    const r = await confirm({
      title: 'Hapus Combo',
      message: 'Combo "' + c.name + '" akan dihapus. Lanjutkan?',
      confirmLabel: 'Hapus',
      danger: true,
    })
    if (!r) return
    try {
      const { ok, data } = await api.deleteCombo(c.id)
      if (ok) {
        showToast('Combo "' + c.name + '" dihapus.')
        load()
      } else {
        showToast(data.error || 'Gagal menghapus combo', false)
      }
    } catch {
      showToast('Gagal menghapus combo', false)
    }
  }

  const activate = async (c) => {
    try {
      const { ok, data } = await api.activateCombo(c.id)
      if (ok) {
        setActive(data.active || '')
        showToast(data.active ? 'Combo "' + c.name + '" AKTIF' : 'Combo dinonaktifkan')
      } else {
        showToast(data.error || 'Gagal mengaktifkan combo', false)
      }
    } catch {
      showToast('Gagal mengaktifkan combo', false)
    }
  }

  const deactivate = async () => {
    try {
      const { ok, data } = await api.activateCombo('')
      if (ok) { setActive(data.active || ''); showToast('Combo dinonaktifkan') }
    } catch {
      showToast('Gagal menonaktifkan combo', false)
    }
  }

  return (
    <>
      <div className="card card-pad">
        <div className="flex" style={{ marginBottom: 12 }}>
          <div className="card-ic"><span className="ms">layers</span></div>
          <div>
            <div className="card-title">Model Combos</div>
            <div className="card-sub">Fallback: coba model berurutan saat gagal · Round Robin: rotasi · Fusion: panel + judge</div>
          </div>
          <div style={{ marginLeft: 'auto', display: 'flex', gap: 8 }}>
            {active && (
              <button className="btn btn-secondary btn-sm" onClick={deactivate}>Nonaktifkan</button>
            )}
            <button className="btn btn-primary btn-sm" onClick={openNew}>
              <span className="ms">add</span> Create Combo
            </button>
          </div>
        </div>

        {active && (
          <div className="hint" style={{ marginBottom: 12 }}>
            Combo aktif: <code>{combos.find((c) => c.id === active)?.name || active}</code> — model dipilih sesuai strategi combo.
          </div>
        )}

        {loading ? (
          <div className="empty-state"><span className="spinner" /> Memuat combos...</div>
        ) : combos.length === 0 ? (
          <div className="empty-state">
            <span className="ms">layers</span>
            <p>Belum ada combo</p>
            <span>Buat combo untuk mengelompokkan beberapa model provider di bawah satu nama.</span>
          </div>
        ) : (
          <div className="flex-col" style={{ gap: 10 }}>
            {combos.map((c) => (
              <div key={c.id} className="card" style={{ padding: 14 }}>
                <div className="flex" style={{ justifyContent: 'space-between', gap: 12, flexWrap: 'wrap' }}>
                  <div className="flex" style={{ gap: 10, minWidth: 0, flex: 1 }}>
                    <div className="card-ic"><span className="ms">layers</span></div>
                    <div style={{ minWidth: 0 }}>
                      <div className="row-title">
                        <span className="combo-name">{c.name}</span>
                        {active === c.id && <span className="badge badge-primary badge-dot">AKTIF</span>}
                      </div>
                      <div className="mt-8 flex" style={{ gap: 6, flexWrap: 'wrap' }}>
                        {(c.models || []).map((m) => <span key={m} className="chip">{m}</span>)}
                        {(c.models || []).length === 0 && <span className="muted small">no models</span>}
                      </div>
                    </div>
                  </div>
                  <div className="row-actions">
                    <span className="badge badge-neutral">{c.strategy || 'fallback'}</span>
                    {active === c.id ? (
                      <button className="btn btn-secondary btn-sm" onClick={deactivate}>Aktif</button>
                    ) : (
                      <button className="btn btn-success btn-sm" onClick={() => activate(c)}>Aktifkan</button>
                    )}
                    <button className="btn btn-ghost btn-sm" onClick={() => openEdit(c)}>
                      <span className="ms">edit</span>
                    </button>
                    <button className="btn btn-ghost btn-sm" style={{ color: 'var(--danger)' }} onClick={() => del(c)}>
                      <span className="ms">delete</span>
                    </button>
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      {draft && (
        <div className="modal-overlay" onClick={() => setDraft(null)}>
          <div className="modal" style={{ maxWidth: 560 }} onClick={(e) => e.stopPropagation()}>
            <div className="modal-head">
              <h3>{draft.id ? 'Edit Combo' : 'Create Combo'}</h3>
              <button className="icon-btn" onClick={() => setDraft(null)} aria-label="Tutup">
                <span className="ms">close</span>
              </button>
            </div>
            <div className="modal-body">
              <label className="field">Combo Name</label>
              <input
                type="text"
                value={draft.name || ''}
                placeholder="my-combo"
                onChange={(e) => setDraft({ ...draft, name: e.target.value })}
              />
              <div className="hint">Hanya huruf, angka, -, _ dan .</div>

              <label className="field">Strategi</label>
              <select value={strategy} onChange={(e) => setStrategy(e.target.value)}>
                {STRATEGIES.map((s) => <option key={s.value} value={s.value}>{s.label}</option>)}
              </select>
              <div className="hint">Fusion saat ini memperlakukan panel sebagai fallback (judge belum tersedia).</div>

              <label className="field">Models</label>
              {models.length === 0 ? (
                <div className="empty-state" style={{ padding: 16 }}>
                  <span className="ms" style={{ fontSize: 24 }}>layers</span>
                  <p>Belum ada model ditambahkan</p>
                </div>
              ) : (
                <div className="flex" style={{ gap: 6, flexWrap: 'wrap' }}>
                  {models.map((m) => (
                    <span key={m} className="chip">
                      {m}
                      <button className="btn btn-ghost btn-sm" style={{ padding: 0, minWidth: 16 }} onClick={() => removeModel(m)} title="Hapus model">
                        <span className="ms" style={{ fontSize: 12, color: 'var(--danger)' }}>close</span>
                      </button>
                    </span>
                  ))}
                </div>
              )}
              <button className="btn-outline-brand mt-12" onClick={() => setPickerOpen(true)}>
                <span className="ms">add</span> Add Model
              </button>
            </div>
            <div className="modal-foot">
              <button className="btn btn-secondary" onClick={() => setDraft(null)}>Cancel</button>
              <button className="btn btn-primary" onClick={save} disabled={saving || !(draft.name || '').trim()}>
                {saving ? 'Saving...' : draft.id ? 'Save' : 'Create'}
              </button>
            </div>
          </div>
        </div>
      )}

      {pickerOpen && (
        <ModelSelectModal
          isOpen={pickerOpen}
          onClose={() => setPickerOpen(false)}
          onSelect={(v) => setModels((ms) => (ms.includes(v) ? ms : [...ms, v]))}
          onDeselect={(v) => setModels((ms) => ms.filter((m) => m !== v))}
          addedModelValues={models}
          title="Add Model to Combo"
        />
      )}
    </>
  )
}