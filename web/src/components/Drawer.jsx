export default function Drawer({ open, sections, active, onSelect, onClose }) {
  return (
    <div className={'drawer' + (open ? ' open' : '')}>
      <div className="drawer-head">
        <span>PURU/OS&mdash;CH</span>
        <button className="drawer-close" onClick={onClose} aria-label="Tutup">&times;</button>
      </div>
      <nav className="drawer-nav">
        {sections.map((s, i) => (
          <a
            key={s.id}
            href="javascript:void(0)"
            className={'nav-item' + (active === s.id ? ' active' : '')}
            onClick={() => onSelect(s.id)}
          >
            <span className="drawer-num">{String(i + 1).padStart(2, '0')}</span>
            <span>{s.label}</span>
          </a>
        ))}
      </nav>
    </div>
  )
}
