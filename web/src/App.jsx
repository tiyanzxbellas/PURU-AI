import { useCallback, useEffect, useState } from 'react'
import { api } from './api'
import Drawer from './components/Drawer'
import Boot from './components/Boot'
import ConfigPanel from './components/ConfigPanel'
import ModelPanel from './components/ModelPanel'
import SkillsPanel from './components/SkillsPanel'
import FilesPanel from './components/FilesPanel'
import Modal from './components/Modal'

const SECTIONS = [
  { id: 'api', label: 'API Config' },
  { id: 'model', label: 'Model' },
  { id: 'skills', label: 'Skills' },
  { id: 'files', label: 'Files' },
]

export default function App() {
  const [section, setSection] = useState('api')
  const [menuOpen, setMenuOpen] = useState(false)
  const [toast, setToast] = useState(null)
  const [modal, setModal] = useState(null)
  const [modalResolve, setModalResolve] = useState(null)
  const [config, setConfig] = useState({
    baseUrl: '',
    model: '',
    systemPrompt: '',
  })
  const [apiKey, setApiKey] = useState('')

  const reloadConfig = useCallback(async () => {
    try {
      const { ok, data } = await api.getConfig()
      if (ok) {
        setConfig({
          baseUrl: data.baseUrl || '',
          model: data.model || '',
          systemPrompt: data.systemPrompt || '',
        })
        setApiKey(data.apiKey || '')
      }
    } catch {}
  }, [])

  useEffect(() => { reloadConfig() }, [reloadConfig])

  // ---- toast (feedback ringan, auto-dismiss) ----
  const showToast = useCallback((msg, ok = true) => {
    setToast({ msg, ok, key: Date.now() })
  }, [])
  useEffect(() => {
    if (!toast) return
    const t = setTimeout(() => setToast(null), 3200)
    return () => clearTimeout(t)
  }, [toast])

  // ---- custom modal (mengganti confirm/alert bawaan browser) ----
  const confirmModal = useCallback((opts) => new Promise((resolve) => {
    setModalResolve(() => resolve)
    setModal({ cancelLabel: 'Batal', danger: false, ...opts })
  }), [])

  const alertModal = useCallback((opts) => new Promise((resolve) => {
    setModalResolve(() => resolve)
    setModal({ cancelLabel: null, ...opts })
  }), [])

  const closeModal = useCallback((result) => {
    if (modalResolve) modalResolve(result)
    setModalResolve(null)
    setModal(null)
  }, [modalResolve])

  const saveConfig = useCallback(async (local) => {
    const body = {}
    if (local.baseUrl.trim()) body.baseUrl = local.baseUrl.trim()
    if (apiKey.trim()) body.apiKey = apiKey.trim()
    if (config.model.trim()) body.model = config.model.trim()
    body.systemPrompt = local.systemPrompt
    try {
      const { ok, data } = await api.saveConfig(body)
      if (ok) {
        showToast('Settings saved!')
        reloadConfig()
      } else {
        await alertModal({ title: 'Gagal Menyimpan', message: data.error || 'Terjadi kesalahan.', danger: true })
      }
    } catch (e) {
      await alertModal({ title: 'Error', message: 'Network error: ' + e.message, danger: true })
    }
  }, [apiKey, config.model, showToast, reloadConfig, alertModal])

  const clearConfig = useCallback(async () => {
    const ok = await confirmModal({
      title: 'Hapus Semua Pengaturan',
      message: 'Semua pengaturan API (base URL, API key, model, system prompt) akan dihapus. Lanjutkan?',
      confirmLabel: 'Hapus',
      danger: true,
    })
    if (!ok) return
    try {
      const { ok: r, data } = await api.clearConfig()
      if (r) {
        showToast('Pengaturan berhasil dihapus.')
        reloadConfig()
      } else {
        await alertModal({ title: 'Gagal', message: data.error || 'Terjadi kesalahan.', danger: true })
      }
    } catch (e) {
      await alertModal({ title: 'Error', message: 'Network error: ' + e.message, danger: true })
    }
  }, [confirmModal, alertModal, showToast, reloadConfig])

  // Simpan model ke backend langsung saat Terapkan/Pilih/Hapus di section Model.
  const onModelChange = useCallback(async (name) => {
    setConfig((c) => ({ ...c, model: name }))
    try {
      const { ok, data } = await api.saveConfig({ model: name })
      if (ok) {
        showToast(name ? 'Model "' + name + '" disimpan' : 'Model dikosongkan')
        reloadConfig()
      } else {
        showToast(data.error || 'Gagal menyimpan model', false)
      }
    } catch (e) {
      showToast('Gagal menyimpan model', false)
    }
  }, [showToast, reloadConfig])

  const selectSection = useCallback((id) => {
    setSection(id)
    setMenuOpen(false)
  }, [])

  return (
    <div className="deck">
      <div className="crt" aria-hidden="true" />
      <Boot />

      <header className="topbar">
        <button className="hamburger" onClick={() => setMenuOpen(true)} aria-label="Menu">
          &#9776;
        </button>
        <div className="brand">PURU<span className="brand-dot">·</span>AI</div>
        <div className="radar-wrap">
          <span className="radar" />
          <span className="radar-lbl">SIGNAL OK</span>
        </div>
      </header>

      <Drawer
        open={menuOpen}
        sections={SECTIONS}
        active={section}
        onSelect={selectSection}
        onClose={() => setMenuOpen(false)}
      />
      {menuOpen && <div className="overlay" onClick={() => setMenuOpen(false)} />}

      <main>
        {section === 'api' && (
          <ConfigPanel
            config={config}
            apiKey={apiKey}
            setApiKey={setApiKey}
            onSave={saveConfig}
            onClear={clearConfig}
          />
        )}

        {section === 'model' && (
          <ModelPanel
            model={config.model}
            onModelChange={onModelChange}
            showToast={showToast}
          />
        )}

        {section === 'skills' && (
          <SkillsPanel
            showToast={showToast}
            confirm={confirmModal}
            alert={alertModal}
          />
        )}

        {section === 'files' && (
          <FilesPanel
            showToast={showToast}
            confirm={confirmModal}
            alert={alertModal}
          />
        )}
      </main>

      <Modal modal={modal} onConfirm={() => closeModal(true)} onCancel={() => closeModal(false)} />

      {toast && (
        <div className={'toast ' + (toast.ok ? 'toast-ok' : 'toast-err')} key={toast.key}>
          {toast.msg}
        </div>
      )}

      <footer className="foot-stamp">SESSION 0xA1 &mdash; RTDB LIVE &middot; 4 CHANNELS &middot; CRT_TERMINAL</footer>
    </div>
  )
}
