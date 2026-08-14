import { useEffect, useState } from 'react'

export default function ConfigPanel({ config, apiKey, setApiKey, onSave, onClear }) {
  const [local, setLocal] = useState({
    baseUrl: config.baseUrl,
    systemPrompt: config.systemPrompt,
  })

  useEffect(() => {
    setLocal({ baseUrl: config.baseUrl, systemPrompt: config.systemPrompt })
  }, [config.baseUrl, config.systemPrompt])

  const set = (k, v) => setLocal((s) => ({ ...s, [k]: v }))

  const handleSave = () => {
    onSave({ ...local })
  }

  return (
    <section className="card">
      <h2 className="panel-title">API Config</h2>
      <label>Base URL</label>
      <input
        value={local.baseUrl}
        placeholder="https://api.openai.com/v1"
        onChange={(e) => set('baseUrl', e.target.value)}
      />
      <label>API Key</label>
      <input
        type="password"
        value={apiKey}
        placeholder={config.hasApiKey ? 'Current key set (leave empty to keep)' : 'sk-...'}
        onChange={(e) => setApiKey(e.target.value)}
      />
      <label>System Prompt / Role</label>
      <textarea
        value={local.systemPrompt}
        placeholder="Kamu adalah asisten yang ramah..."
        onChange={(e) => set('systemPrompt', e.target.value)}
      />
      <div style={{ marginTop: 10 }}>
        <button className="btn btn-primary" onClick={handleSave}>Save</button>
        <button className="btn btn-danger" onClick={onClear}>Clear All</button>
      </div>
    </section>
  )
}
