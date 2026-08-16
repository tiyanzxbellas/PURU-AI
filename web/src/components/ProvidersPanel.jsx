import { useCallback, useEffect, useState } from 'react'
import { api } from '../api'

const API_TYPES = [
  { value: 'chat', label: 'Chat Completions' },
  { value: 'responses', label: 'Responses API' },
]

const DEFAULT_BASEURL = 'https://api.openai.com/v1'

function fullModel(prefix, id) { return prefix + '/' + id }

export default function ProvidersPanel({ config, relayUrl, onModelChange, showToast, confirm, alert }) {
  const [providers, setProviders] = useState([])
  const [loading, setLoading] = useState(true)
  const [statuses, setStatuses] = useState({}) // id -> {online, models, error}
  const [expanded, setExpanded] = useState(null)
  const [modal, setModal] = useState(null) // { provider?, form, checking, checkResult }

  const activeModel = config.model || ''

  const checkAll = useCallback(async (list) => {
    const next = {}
    await Promise.all(list.map(async (p) => {
      try {
        const { ok, data } = await api.checkProvider({ providerId: p.id })
        if (ok) next[p.id] = { online: !!data.online, models: data.models || [], error: data.error || '' }
        else next[p.id] = { online: false, models: [], error: data.error || 'check failed' }
      } catch {
        next[p.id] = { online: false, models: [], error: 'network error' }
      }
    }))
    setStatuses(next)
  }, [])

  const load = useCallback(async (opts) => {
    setLoading(true)
    try {
      const { ok, data } = await api.listProviders()
      if (ok) {
        const list = data.providers || []
        setProviders(list)
        setLoading(false)
        setExpanded(opts?.expand || null)
        // Status/model di-refresh asinkron tanpa membebani spinner (provider
        // offline bisa lambat timeout).
        checkAll(list)
      } else {
        setLoading(false)
      }
    } catch {
      setLoading(false)
      showToast('Gagal memuat providers', false)
    }
  }, [checkAll, showToast])

  useEffect(() => { load() }, [load])

  const refreshStatus = async (p) => {
    const { ok, data } = await api.checkProvider({ providerId: p.id, force: true })
    if (ok) {
      setStatuses((s) => ({ ...s, [p.id]: { online: !!data.online, models: data.models || [], error: data.error || '' } }))
      return { online: !!data.online, models: data.models || [], error: data.error || '' }
    }
    return { online: false, models: [], error: data.error || 'check failed' }
  }

  const handleDelete = async (p) => {
    const r = await confirm({
      title: 'Hapus Provider',
      message: `Provider "${p.name}" (${p.prefix}) akan dihapus. Model yang memakai prefix ini (di config default & combo) ikut dibersihkan. Lanjutkan?`,
      confirmLabel: 'Hapus',
      danger: true,
    })
    if (!r) return
    try {
      const { ok, data } = await api.deleteProvider(p.id)
      if (ok) {
        showToast('Provider "' + p.name + '" dihapus.')
        setExpanded(null)
        load()
      } else {
        showToast(data.error || 'Gagal menghapus provider', false)
      }
    } catch {
      showToast('Gagal menghapus provider', false)
    }
  }

  const openAdd = () => {
    setModal({
      form: {
        name: '', prefix: '',
        apiType: 'chat',
        baseUrl: DEFAULT_BASEURL,
        apiKey: '', proxyOn: false, headers: '',
      },
      checking: false, checkResult: null,
    })
  }

  const openEdit = (p) => {
    setModal({
      provider: p,
      form: {
        name: p.name || '', prefix: p.prefix || '',
        apiType: p.apiType || 'chat',
        baseUrl: p.baseUrl || '',
        apiKey: '', proxyOn: !!(p.proxyUrl || ''), headers: '',
      },
      checking: false, checkResult: null,
    })
  }

  const setForm = (k, v) => setModal((m) => ({ ...m, form: { ...m.form, [k]: v }, checkResult: null }))

  const parseHeaders = () => {
    const raw = (modal?.form.headers || '').trim()
    if (!raw) return {}
    try {
      const o = JSON.parse(raw)
      if (typeof o !== 'object' || Array.isArray(o)) throw new Error('must be an object')
      return o
    } catch (e) {
      return { __error__: 'Headers harus JSON object: ' + e.message }
    }
  }

  const proxyValue = () => (modal?.form.proxyOn ? (relayUrl || '') : '')

  const handleCheck = async () => {
    const f = modal.form
    const h = parseHeaders()
    if (h.__error__) { showToast(h.__error__, false); return }
    setModal((m) => ({ ...m, checking: true }))
    try {
      const { ok, data } = await api.checkProvider({
        baseUrl: f.baseUrl, apiType: f.apiType,
        apiKey: f.apiKey.trim(), headers: h, proxyUrl: proxyValue(),
      })
      setModal((m) => ({ ...m, checking: false, checkResult: ok ? data : { online: false, error: data.error || 'check failed' } }))
    } catch {
      setModal((m) => ({ ...m, checking: false, checkResult: { online: false, error: 'network error' } }))
    }
  }

  const handleSave = async () => {
    const f = modal.form
    if (!f.name.trim() || !f.prefix.trim() || !f.baseUrl.trim()) {
      showToast('Name, Prefix & Base URL wajib diisi', false)
      return
    }
    const h = parseHeaders()
    if (h.__error__) { showToast(h.__error__, false); return }
    const body = {
      name: f.name.trim(), prefix: f.prefix.trim(),
      baseUrl: f.baseUrl.trim(), apiKey: f.apiKey.trim(),
      proxyUrl: proxyValue(), headers: h,
    }
    if (!body.proxyUrl) delete body.proxyUrl
    if (modal.provider) body.id = modal.provider.id
    try {
      const { ok, data } = await api.saveProvider(body)
      if (ok) {
        showToast(modal.provider ? 'Provider "' + body.name + '" diperbarui' : 'Provider "' + body.name + '" ditambahkan')
        setModal(null)
        load({ expand: data.provider.id })
      } else {
        showToast(data.error || 'Gagal menyimpan provider', false)
      }
    } catch {
      showToast('Gagal menyimpan provider', false)
    }
  }

  const setDefault = async (p, modelId) => {
    const model = fullModel(p.prefix, modelId)
    await onModelChange(model)
  }

  return (
    <>
      <div className="card card-pad">
        <div className="flex" style={{ marginBottom: 16, flexWrap: 'wrap', gap: 8 }}>
          <div className="flex" style={{ minWidth: 0, gap: 10 }}>
            <div className="card-ic"><span className="ms">dns</span></div>
            <div>
              <div className="card-title">Providers</div>
              <div className="card-sub">PURU Gateway (built-in) + endpoint OpenAI-compatible milik Anda — model dibaca live dari /models</div>
            </div>
          </div>
          <div className="flex" style={{ marginLeft: 'auto', gap: 8 }}>
            <button className="btn btn-primary btn-sm" onClick={openAdd}>
              <span className="ms">add</span> Add OpenAI Compatible
            </button>
          </div>
        </div>

        {loading ? (
          <div className="empty-state"><span className="spinner" /> Memuat providers...</div>
        ) : providers.length === 0 ? (
          <div className="empty-state">
            <span className="ms">dns</span>
            <p>Belum ada provider</p>
            <span>Tambahkan endpoint OpenAI-compatible untuk melihat model online-nya (diambil dari /models).</span>
          </div>
        ) : (
          <div className="flex-col" style={{ gap: 12 }}>
            {providers.map((p) => {
              const st = statuses[p.id] || { online: false, models: [], error: '' }
              const isOpen = expanded === p.id
              const isActive = st.models.some((m) => activeModel === fullModel(p.prefix, m.id))
              return (
                <div key={p.id} className={'prov-card' + (isOpen ? '' : '')} style={{ flexDirection: 'column', gap: 10 }}>
                  <div className="flex" style={{ justifyContent: 'space-between', gap: 12, flexWrap: 'wrap', cursor: 'pointer' }} onClick={() => setExpanded(isOpen ? null : p.id)}>
                    <div className="flex" style={{ minWidth: 0, gap: 10 }}>
                      <div className="card-ic" style={{ background: 'rgba(229,106,74,0.12)' }}>
                        <span className="ms">{p.builtin ? 'smart_toy' : 'cloud'}</span>
                      </div>
                      <div style={{ minWidth: 0 }}>
                        <div className="row-title">
                          <span className="combo-name">{p.name}</span>
                          {p.builtin && <span className="badge badge-primary badge-dot">built-in</span>}
                          {p.hasApiKey && <span className="badge badge-neutral">key ✓</span>}
                        </div>
                        <div className="mt-8 flex" style={{ gap: 6, flexWrap: 'wrap' }}>
                          <span className="chip">{p.prefix}</span>
                          <code style={{ maxWidth: 260 }}>{p.baseUrl}</code>
                        </div>
                      </div>
                    </div>
                    <div className="flex" style={{ gap: 6 }}>
                      {st.online ? (
                        <span className="badge badge-success dot">{String(st.models?.length || 0)} models</span>
                      ) : (
                        <span className="badge badge-error dot">Failed</span>
                      )}
                      <button className="btn btn-ghost btn-sm" onClick={(e) => { e.stopPropagation(); refreshStatus(p) }} title="Refresh models">
                        <span className="ms">refresh</span>
                      </button>
                      {!p.builtin && (
                        <>
                          <button className="btn btn-ghost btn-sm" onClick={(e) => { e.stopPropagation(); openEdit(p) }} title="Edit provider">
                            <span className="ms">edit</span>
                          </button>
                          <button className="btn btn-ghost btn-sm" style={{ color: 'var(--danger)' }} onClick={(e) => { e.stopPropagation(); handleDelete(p) }} title="Hapus provider">
                            <span className="ms">delete</span>
                          </button>
                        </>
                      )}
                      <button className="btn btn-ghost btn-sm" onClick={() => setExpanded(isOpen ? null : p.id)} title={isOpen ? 'Tutup' : 'Lihat model'}>
                        <span className="ms">{isOpen ? 'expand_less' : 'expand_more'}</span>
                      </button>
                    </div>
                  </div>

                  {isOpen && (
                    <div style={{ borderTop: '1px solid var(--border-subtle)', paddingTop: 12 }}>
                      <div className="section-label">Available models (live from /models)</div>
                      {isActive && activeModel && <div className="hint mt-8">Default aktif: <code>{activeModel}</code></div>}
                      {st.models.length === 0 ? (
                        !st.online ? (
                          <div className="hint" style={{ marginTop: 8 }}>
                            Provider belum online{st.error ? ': ' + st.error : ''}. Model id tetap bisa dipakai — tulis <code>{p.prefix}/model-id</code> di combo.
                          </div>
                        ) : (
                          <div className="hint" style={{ marginTop: 8 }}>Provider online tetapi tidak mengembalikan daftar model (/models kosong).</div>
                        )
                      ) : (
                        <div className="flex mt-8" style={{ gap: 6, flexWrap: 'wrap' }}>
                          {st.models.map((m) => {
                            const f = fullModel(p.prefix, m.id)
                            const isDef = activeModel === f
                            return (
                              <button
                                key={m.id}
                                onClick={() => setDefault(p, m.id)}
                                title="Klik untuk set sebagai default"
                                className={'chip' + (isDef ? ' badge-primary' : '')}
                                style={isDef ? { border: '1px solid var(--primary)', color: 'var(--primary)' } : { cursor: 'pointer' }}
                              >
                                <span className="ms" style={{ fontSize: 12 }}>{isDef ? 'check_circle' : 'smart_toy'}</span>
                                {m.name || m.id}
                              </button>
                            )
                          })}
                        </div>
                      )}
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        )}
      </div>

      {modal && (
        <div className="modal-overlay" onClick={() => setModal(null)}>
          <div className="modal" style={{ maxWidth: 560 }} onClick={(e) => e.stopPropagation()}>
            <div className="modal-head">
              <h3>{modal.provider ? 'Edit Provider' : 'Add OpenAI Compatible'}</h3>
              <button className="icon-btn" onClick={() => setModal(null)} aria-label="Tutup">
                <span className="ms">close</span>
              </button>
            </div>
            {modal.provider?.builtin && (
              <div className="hint" style={{ margin: '0 20px 8px' }}>Provider built-in tidak bisa diubah — gunakan toggle Proxy Relay global di halaman System Prompt.</div>
            )}
            <div className="modal-body">
              <label className="field">Name</label>
              <input type="text" value={modal.form.name} placeholder="OpenAI Compatible (Prod)"
                onChange={(e) => setForm('name', e.target.value)} />

              <label className="field">Prefix</label>
              <input type="text" value={modal.form.prefix} placeholder="oc-prod"
                onChange={(e) => setForm('prefix', e.target.value)} />
              <div className="hint">Dipakai sebagai prefix model: <code>{modal.form.prefix || 'prefix'}/model-id</code></div>

              <label className="field">API Type</label>
              <select value={modal.form.apiType} onChange={(e) => setForm('apiType', e.target.value)}>
                {API_TYPES.map((a) => <option key={a.value} value={a.value}>{a.label}</option>)}
              </select>

              <label className="field">Base URL</label>
              <input type="text" value={modal.form.baseUrl}
                placeholder={DEFAULT_BASEURL}
                onChange={(e) => setForm('baseUrl', e.target.value)} />
              <div className="hint">Gunakan base URL yang berakhir /v1 untuk API OpenAI-compatible Anda.</div>

              <label className="field">API Key</label>
              <input type="password" value={modal.form.apiKey} placeholder={modal.provider?.hasApiKey ? '•••••••• tersimpan — kosongkan untuk biarkan' : 'sk-...'}
                onChange={(e) => setForm('apiKey', e.target.value)} />

              <label className="field">Proxy Relay (Vercel built-in)</label>
              <div className="flex" style={{ gap: 10, alignItems: 'center', flexWrap: 'wrap' }}>
                <button
                  className={'btn btn-sm ' + (modal.form.proxyOn ? 'btn-success' : 'btn-secondary')}
                  onClick={() => setForm('proxyOn', !modal.form.proxyOn)}
                >
                  <span className="ms">{modal.form.proxyOn ? 'toggle_on' : 'toggle_off'}</span> {modal.form.proxyOn ? 'ON' : 'OFF'}
                </button>
                <code className="muted small" style={{ wordBreak: 'break-all' }}>
                  {modal.form.proxyOn ? (relayUrl || 'relay tidak dikonfigurasi') : 'langsung ke endpoint'}
                </code>
              </div>
              <div className="hint">ON me-rutekan model provider lewat relay Vercel built-in; OFF langsung ke endpoint.</div>

              <label className="field">Extra Headers (JSON)</label>
              <textarea value={modal.form.headers} placeholder={'{ "x-opencode-client": "desktop" }'}
                onChange={(e) => setForm('headers', e.target.value)} />

              <div className="flex mt-12" style={{ gap: 8, alignItems: 'flex-start', flexWrap: 'wrap' }}>
                <button className="btn btn-secondary btn-sm" onClick={handleCheck} disabled={modal.checking || !modal.form.baseUrl.trim()}>
                  {modal.checking ? <span className="spinner" /> : <span className="ms">check</span>} Check
                </button>
                {modal.checkResult && (
                  <div className="flex" style={{ gap: 6, flexWrap: 'wrap', alignItems: 'flex-start' }}>
                    {modal.checkResult.online ? (
                      <>
                        <span className="badge badge-success">Online ({modal.checkResult.models?.length || 0} models)</span>
                        <div className="flex" style={{ gap: 4, flexWrap: 'wrap', marginTop: 4 }}>
                          {(modal.checkResult.models || []).slice(0, 8).map((m) => (
                            <span key={m.id} className="chip">{m.name || m.id}</span>
                          ))}
                          {(modal.checkResult.models || []).length > 8 && <span className="chip">+{modal.checkResult.models.length - 8}</span>}
                        </div>
                      </>
                    ) : (
                      <div className="flex-col" style={{ gap: 6, minWidth: 0, maxWidth: '100%' }}>
                        <span className="badge badge-error dot">Offline</span>
                        {modal.checkResult.error && (
                          <div className="hint" style={{ wordBreak: 'break-word' }}>
                            {modal.checkResult.error}
                          </div>
                        )}
                      </div>
                    )}
                  </div>
                )}
              </div>
            </div>
            <div className="modal-foot">
              <button className="btn btn-secondary" onClick={() => setModal(null)}>Cancel</button>
              <button className="btn btn-primary" onClick={handleSave} disabled={!modal.form.name.trim() || !modal.form.prefix.trim() || !modal.form.baseUrl.trim()}>
                {modal.provider ? 'Save' : 'Create'}
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  )
}