import { useCallback, useEffect, useRef, useState } from 'react'
import { api } from '../api'

const LEVEL_COLORS = {
  LOG: 'log-log',
  INFO: 'log-info',
  WARN: 'log-warn',
  ERROR: 'log-error',
  DEBUG: 'log-debug',
  FATAL: 'log-error',
  PANIC: 'log-error',
}

function colorLine(line, i) {
  const m = line.match(/\[(\w+)\]/)
  const level = m ? m[1].toUpperCase() : null
  const cls = LEVEL_COLORS[level] || 'log-default'
  return <div key={i} className={cls}>{line}</div>
}

export default function LogsPanel({ showToast }) {
  const [lines, setLines] = useState([])
  const [loading, setLoading] = useState(true)
  const [auto, setAuto] = useState(true)
  const ref = useRef(null)

  const load = useCallback(async () => {
    try {
      const { ok, data } = await api.getLogs()
      if (ok) setLines(data.lines || [])
    } catch {
      showToast('Gagal memuat log', false)
    } finally {
      setLoading(false)
    }
  }, [showToast])

  useEffect(() => { load() }, [load])

  useEffect(() => {
    if (!auto) return
    const t = setInterval(() => { load() }, 2000)
    return () => clearInterval(t)
  }, [auto, load])

  useEffect(() => {
    if (auto && ref.current) ref.current.scrollTop = ref.current.scrollHeight
  }, [lines, auto])

  const clear = async () => {
    try {
      const { ok, data } = await api.clearLogs()
      if (ok) { setLines([]); showToast('Log dibersihkan.') }
      else showToast(data.error || 'Gagal bersihkan log', false)
    } catch {
      showToast('Gagal bersihkan log', false)
    }
  }

  return (
    <div className="card">
      <div className="card-head">
        <div>
          <div className="card-title">Console</div>
          <div className="card-sub">Live output proses bot (auto-refresh 2 detik)</div>
        </div>
        <div className="flex">
          <label className="toggle toggle-sm" style={{ fontSize: 12, color: 'var(--text-muted)' }}>
            <button
              className={'toggle-track' + (auto ? ' on' : '')}
              onClick={() => setAuto((a) => !a)}
              aria-pressed={auto}
            >
              <span className="toggle-thumb" />
            </button>
            Live
          </label>
          <button className="btn btn-ghost btn-sm" onClick={clear}>
            <span className="ms">delete_sweep</span> Clear
          </button>
        </div>
      </div>
      <div className="card-body-sm">
        <div className="console">
          <div className="console-body" ref={ref}>
            {loading ? (
              <div className="console-empty"><span className="spinner" /> Memuat log...</div>
            ) : lines.length === 0 ? (
              <div className="console-empty">Belum ada log.</div>
            ) : (
              lines.map(colorLine)
            )}
          </div>
        </div>
      </div>
    </div>
  )
}