import { useCallback, useEffect, useState } from 'react'
import { api } from '../api'

const fmt = (n) => new Intl.NumberFormat().format(n || 0)

function timeAgo(iso) {
  if (!iso) return ''
  const diff = Math.floor((Date.now() - new Date(iso).getTime()) / 1000)
  if (diff < 60) return diff + 's ago'
  if (diff < 3600) return Math.floor(diff / 60) + 'm ago'
  if (diff < 86400) return Math.floor(diff / 3600) + 'h ago'
  return Math.floor(diff / 86400) + 'd ago'
}

export default function UsagePanel({ showToast }) {
  const [summary, setSummary] = useState(null)
  const [records, setRecords] = useState([])
  const [loading, setLoading] = useState(true)

  const load = useCallback(async () => {
    try {
      const { ok, data } = await api.getUsage()
      if (ok) {
        setSummary(data.summary || { totalRequests: 0, totalInput: 0, totalOutput: 0 })
        setRecords(data.records || [])
      }
    } catch {
      showToast('Gagal memuat usage', false)
    } finally {
      setLoading(false)
    }
  }, [showToast])

  useEffect(() => { load() }, [load])

  const clearUsage = async () => {
    try {
      const { ok, data } = await api.clearUsage()
      if (ok) { showToast('History usage dihapus.'); load() }
      else showToast(data.error || 'Gagal menghapus usage', false)
    } catch {
      showToast('Gagal menghapus usage', false)
    }
  }

  const maxIn = Math.max(1, ...records.map((r) => r.input || 0))
  const maxOut = Math.max(1, ...records.map((r) => r.output || 0))

  return (
    <>
      <div className="stat-grid">
        <div className="card stat-card">
          <span className="stat-label">Total Requests</span>
          <span className="stat-value">{loading ? '…' : fmt(summary && summary.totalRequests)}</span>
        </div>
        <div className="card stat-card">
          <span className="stat-label">Total Tokens</span>
          <span className="stat-value brand">{loading ? '…' : fmt((summary && summary.totalInput || 0) + (summary && summary.totalOutput || 0))}</span>
        </div>
        <div className="card stat-card">
          <span className="stat-label">Input Tokens</span>
          <span className="stat-value info">{loading ? '…' : fmt(summary && summary.totalInput)}</span>
        </div>
        <div className="card stat-card">
          <span className="stat-label">Output Tokens</span>
          <span className="stat-value good">{loading ? '…' : fmt(summary && summary.totalOutput)}</span>
        </div>
      </div>

      <div className="card">
        <div className="card-head">
          <div>
            <div className="card-title">Recent Requests</div>
            <div className="card-sub">Token input/output per request model (maks 500 histori)</div>
          </div>
          <button className="btn btn-ghost btn-sm" onClick={clearUsage}>
            <span className="ms">delete_sweep</span> Clear
          </button>
        </div>
        <div className="table-wrap">
          {loading ? (
            <div className="empty-state"><span className="spinner" /> Memuat usage...</div>
          ) : records.length === 0 ? (
            <div className="empty-state">
              <span className="ms">bar_chart</span>
              <p>Belum ada request.</p>
              <span>Token usage tercatat setelah bot menjawab pesan di Telegram.</span>
            </div>
          ) : (
            <table className="tbl">
              <thead>
                <tr>
                  <th></th>
                  <th>Model</th>
                  <th>Provider</th>
                  <th className="num">In / Out</th>
                  <th className="num">Total</th>
                  <th className="num">When</th>
                </tr>
              </thead>
              <tbody>
                {records.map((r, i) => (
                  <tr key={i}>
                    <td><span className="dot-cell dot-ok" /></td>
                    <td className="tbl-label" style={{ maxWidth: 220 }} title={r.model}>{r.model}</td>
                    <td><span className="chip">{r.provider || '-'}</span></td>
                    <td className="num">
                      <span style={{ color: 'var(--primary)' }}>{fmt(r.input)}↑</span>
                      {' '}
                      <span style={{ color: 'var(--success)' }}>{fmt(r.output)}↓</span>
                    </td>
                    <td className="num muted">{fmt((r.input || 0) + (r.output || 0))}</td>
                    <td className="num muted">{timeAgo(r.at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>

      {/* Simple spark bars: input vs output per request (last 20) */}
      {!loading && records.length > 0 && (
        <div className="card card-pad">
          <div className="card-title" style={{ marginBottom: 12 }}>Token Distribution (20 request terakhir)</div>
          <div className="flex" style={{ gap: 2, alignItems: 'flex-end', height: 100 }}>
            {records.slice(0, 20).map((r, i) => (
              <div key={i} className="flex-col" style={{ flex: 1, gap: 2 }} title={r.model}>
                <div style={{ height: Math.max(2, (r.input || 0) / maxIn * 60), background: 'var(--primary)', borderRadius: 2, width: '100%' }} />
                <div style={{ height: Math.max(2, (r.output || 0) / maxOut * 30), background: 'var(--success)', borderRadius: 2, width: '100%' }} />
              </div>
            ))}
          </div>
        </div>
      )}
    </>
  )
}