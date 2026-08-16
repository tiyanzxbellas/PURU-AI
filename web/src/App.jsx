import { useCallback, useEffect, useState } from 'react'
import { api } from './api'
import Sidebar from './components/Sidebar'
import Header from './components/Header'
import ConfigPanel from './components/ConfigPanel'
import ProvidersPanel from './components/ProvidersPanel'
import CombosPanel from './components/CombosPanel'
import UsagePanel from './components/UsagePanel'
import LogsPanel from './components/LogsPanel'
import SkillsPanel from './components/SkillsPanel'
import FilesPanel from './components/FilesPanel'
import Modal from './components/Modal'

export default function App() {
  const [section, setSection] = useState('providers')
  const [menuOpen, setMenuOpen] = useState(false)
  const [toasts, setToasts] = useState([])
  const [modal, setModal] = useState(null)
  const [modalResolve, setModalResolve] = useState(null)
  const [theme, setTheme] = useState(() => {
    try { return localStorage.getItem('puru_theme') || 'dark' } catch { return 'dark' }
  })
  const [config, setConfig] = useState({ baseUrl: '', model: '', systemPrompt: '', proxyUrl: '', relayUrl: '' })

  useEffect(() => {
    document.documentElement.classList.toggle('dark', theme === 'dark')
    try { localStorage.setItem('puru_theme', theme) } catch {}
  }, [theme])

  const reloadConfig = useCallback(async () => {
    try {
      const { ok, data } = await api.getConfig()
      if (ok) {
        setConfig({
          baseUrl: data.baseUrl || '',
          model: data.model || '',
          systemPrompt: data.systemPrompt || '',
          proxyUrl: data.proxyUrl || '',
          relayUrl: data.relayUrl || '',
        })
      }
    } catch {}
  }, [])

  useEffect(() => { reloadConfig() }, [reloadConfig])

  const pushToast = useCallback((msg, type = 'success') => {
    const id = Date.now() + Math.random()
    setToasts((t) => [...t, { id, msg, type }])
    setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), 3200)
  }, [])

  const showToast = (msg, ok = true) => pushToast(msg, ok ? 'success' : 'error')

  const confirmModal = useCallback((opts) => new Promise((resolve) => {
    setModalResolve(() => resolve)
    setModal({ cancelLabel: 'Batal', ...opts })
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
    } catch {
      showToast('Gagal menyimpan model', false)
    }
  }, [showToast, reloadConfig])

  const onToggleProxy = useCallback(async (enabled) => {
    // The built-in Vercel relay is ON by default (global relayUrl). Toggling
    // OFF writes settings.proxyUrl = "" (force-direct), toggling ON restores
    // the built-in relay URL. Per-user; system prompt is not touched.
    const proxyUrl = enabled ? (config.relayUrl || '') : ''
    try {
      const { ok, data } = await api.saveConfig({ proxyUrl })
      if (ok) {
        showToast(enabled ? 'Proxy relay ON' : 'Proxy relay OFF (langsung)')
        reloadConfig()
      } else {
        showToast(data.error || 'Gagal mengubah proxy relay', false)
      }
    } catch {
      showToast('Gagal mengubah proxy relay', false)
    }
  }, [config.relayUrl, showToast, reloadConfig])

  const saveConfig = useCallback(async (local) => {
    const body = { systemPrompt: local.systemPrompt }
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
  }, [showToast, reloadConfig, alertModal])

  const selectSection = useCallback((id) => {
    setSection(id)
    setMenuOpen(false)
  }, [])

  return (
    <div className="layout">
      {menuOpen && (
        <div className="mobile-overlay" onClick={() => setMenuOpen(false)} />
      )}
      <Sidebar active={section} onSelect={selectSection} open={menuOpen} onClose={() => setMenuOpen(false)} />

      <main className="main">
        <Header
          section={section}
          onMenu={() => setMenuOpen(true)}
          theme={theme}
          onToggleTheme={() => setTheme((t) => (t === 'dark' ? 'light' : 'dark'))}
        />

        <div className="page">
          <div className="page-inner">
            <div className="page-stack">
              {section === 'providers' && (
                <ProvidersPanel config={config} relayUrl={config.relayUrl} onModelChange={onModelChange} showToast={showToast} confirm={confirmModal} alert={alertModal} />
              )}

              {section === 'combos' && <CombosPanel showToast={showToast} confirm={confirmModal} alert={alertModal} />}

              {section === 'usage' && <UsagePanel showToast={showToast} />}

              {section === 'logs' && <LogsPanel showToast={showToast} />}

              {section === 'role' && (
                <ConfigPanel config={config} onSave={saveConfig} onToggleProxy={onToggleProxy} />
              )}

              {section === 'skills' && (
                <SkillsPanel showToast={showToast} confirm={confirmModal} alert={alertModal} />
              )}

              {section === 'files' && (
                <FilesPanel showToast={showToast} confirm={confirmModal} alert={alertModal} />
              )}
            </div>
          </div>
        </div>
      </main>

      <Modal modal={modal} onConfirm={() => closeModal(true)} onCancel={() => closeModal(false)} />

      {toasts.length > 0 && (
        <div className="toast-wrap">
          {toasts.map((t) => (
            <div key={t.id} className={'toast toast-' + t.type}>
              <span className="ms">{t.type === 'success' ? 'check_circle' : t.type === 'error' ? 'error' : 'info'}</span>
              <div>{t.msg}</div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}