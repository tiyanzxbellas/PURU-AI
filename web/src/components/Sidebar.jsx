const NAV = {
  main: [
    { id: 'providers', label: 'Providers', icon: 'dns' },
    { id: 'combos', label: 'Combos', icon: 'layers' },
    { id: 'usage', label: 'Usage', icon: 'bar_chart' },
    { id: 'logs', label: 'Server Logs', icon: 'terminal' },
  ],
  system: [
    { id: 'role', label: 'System Prompt', icon: 'record_voice_over' },
    { id: 'skills', label: 'Skills', icon: 'extension' },
    { id: 'files', label: 'Files', icon: 'folder_open' },
  ],
}

export default function Sidebar({ active, onSelect, open, onClose }) {
  const renderItem = (s) => (
    <button
      key={s.id}
      className={'nav-item' + (active === s.id ? ' active' : '')}
      onClick={() => { onSelect(s.id); onClose && onClose() }}
    >
      <span className="ms">{s.icon}</span>
      <span>{s.label}</span>
    </button>
  )

  return (
    <aside className={'sidebar' + (open ? ' open' : '')}>
      <div className="traffic-lights">
        <span className="tl red" />
        <span className="tl yellow" />
        <span className="tl green" />
      </div>
      <div className="sidebar-brand">
        <div className="brand-row">
          <div className="brand-logo"><span className="ms">hub</span></div>
          <div>
            <div className="brand-name">PURU·AI</div>
            <div className="brand-ver">Control Terminal</div>
          </div>
        </div>
      </div>
      <nav className="sidebar-nav">
        <div className="nav-section">General</div>
        {NAV.main.map(renderItem)}
        <div className="nav-section">System</div>
        {NAV.system.map(renderItem)}
      </nav>
    </aside>
  )
}