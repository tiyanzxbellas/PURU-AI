import { useEffect, useState } from 'react'

export default function ConfigPanel({ config, onSave, onToggleProxy }) {
  const [local, setLocal] = useState({ systemPrompt: config.systemPrompt })

  useEffect(() => {
    setLocal({ systemPrompt: config.systemPrompt })
  }, [config.systemPrompt])

  const proxyOn = !!(config.proxyUrl || '')

  return (
    <div className="card">
      <div className="card-head">
        <div className="flex">
          <div className="card-ic"><span className="ms">record_voice_over</span></div>
          <div>
            <div className="card-title">System Prompt / Role</div>
            <div className="card-sub">Instruksi tambahan untuk agent bot chat ini (tidak termasuk model config)</div>
          </div>
        </div>
      </div>
      <div className="card-body">
        <label className="field">System Prompt / Role</label>
        <textarea
          value={local.systemPrompt}
          placeholder="Kamu adalah asisten yang ramah..."
          onChange={(e) => setLocal({ systemPrompt: e.target.value })}
        />
        <div className="hint">
          Di-append ke system prompt bawaan dengan header "# User-defined instructions". Kosongkan lalu Save untuk menghapusnya.
        </div>
        <div className="flex mt-16" style={{ justifyContent: 'flex-end' }}>
          <button className="btn btn-primary" onClick={() => onSave({ ...local })}>
            Save
          </button>
        </div>
      </div>

      <div className="card-head" style={{ borderTop: '1px solid var(--border-subtle)' }}>
        <div className="flex">
          <div className="card-ic"><span className="ms">swap_vert</span></div>
          <div>
            <div className="card-title">Proxy Relay</div>
            <div className="card-sub">Vercel relay built-in — ON memakai relay, OFF langsung ke endpoint model</div>
          </div>
        </div>
      </div>
      <div className="card-body">
        <div className="flex" style={{ gap: 10, alignItems: 'center', flexWrap: 'wrap' }}>
          <button
            className={'btn btn-sm ' + (proxyOn ? 'btn-success' : 'btn-secondary')}
            onClick={() => onToggleProxy(!proxyOn)}
            title="Proxy relay ON/OFF"
          >
            <span className="ms">{proxyOn ? 'toggle_on' : 'toggle_off'}</span> Proxy {proxyOn ? 'ON' : 'OFF'}
          </button>
          <code className="muted small" style={{ wordBreak: 'break-all' }}>{config.relayUrl || config.proxyUrl || 'relay tidak dikonfigurasi'}</code>
        </div>
        <div className="hint">
          {proxyOn
            ? 'Semua request model dirutekan lewat relay Vercel (auran IP asli). Klik untuk mematikan.'
            : 'Proxy mati — request model langsung ke endpoint. Klik untuk mengaktifkan relay built-in.'}
        </div>
      </div>
    </div>
  )
}