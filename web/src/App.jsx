import { useCallback, useEffect, useState } from 'react'
import { api } from './api'
import Drawer from './components/Drawer'
import ConfigPanel from './components/ConfigPanel'
import ModelPanel from './components/ModelPanel'
import SkillsPanel from './components/SkillsPanel'

const SECTIONS = [
  { id: 'api', label: 'API Config' },
  { id: 'model', label: 'Model' },
  { id: 'skills', label: 'Skills' },
]

export default function App() {
  const [section, setSection] = useState('api')
  const [menuOpen, setMenuOpen] = useState(false)
  const [status, setStatus] = useState({ msg: '', ok: true })
  const [config, setConfig] = useState({
    baseUrl: '',
    model: '',
    systemPrompt: '',
    hasApiKey: false,
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
          hasApiKey: !!data.hasApiKey,
        })
        setApiKey('')
      }
    } catch {}
  }, [])

  useEffect(() => { reloadConfig() }, [reloadConfig])

  const showStatus = useCallback((msg, ok = true) => {
    setStatus({ msg, ok })
    setTimeout(() => setStatus({ msg: '', ok: true }), 3000)
  }, [])

  const saveConfig = useCallback(async (local) => {
    const body = {}
    if (local.baseUrl.trim()) body.baseUrl = local.baseUrl.trim()
    if (apiKey.trim()) body.apiKey = apiKey.trim()
    if (config.model.trim()) body.model = config.model.trim()
    body.systemPrompt = local.systemPrompt
    try {
      const { ok, data } = await api.saveConfig(body)
      if (ok) { showStatus('Settings saved!'); reloadConfig() }
      else showStatus(data.error || 'Save failed', false)
    } catch (e) { showStatus('Network error: ' + e.message, false) }
  }, [apiKey, config.model, showStatus, reloadConfig])

  const clearConfig = useCallback(async () => {
    if (!window.confirm('Hapus semua pengaturan API?')) return
    try {
      const { ok, data } = await api.clearConfig()
      if (ok) { showStatus('Settings cleared.'); reloadConfig() }
      else showStatus(data.error || 'Clear failed', false)
    } catch (e) { showStatus('Network error: ' + e.message, false) }
  }, [showStatus, reloadConfig])

  const selectSection = useCallback((id) => {
    setSection(id)
    setMenuOpen(false)
  }, [])

  return (
    <>
      <header className="topbar">
        <button className="hamburger" onClick={() => setMenuOpen(true)} aria-label="Menu">
          &#9776;
        </button>
        <div className="brand">PURU-AI Settings</div>
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
        <div className={'msg ' + (status.msg ? (status.ok ? 'msg-ok' : 'msg-err') : '')}>
          {status.msg}
        </div>

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
            setModel={(v) => setConfig((c) => ({ ...c, model: v }))}
          />
        )}

        {section === 'skills' && (
          <SkillsPanel showStatus={showStatus} />
        )}
      </main>
    </>
  )
}
