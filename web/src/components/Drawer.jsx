export default function Drawer({ open, sections, active, onSelect, onClose }) {
  return (
    <div className={'drawer' + (open ? ' open' : '')}>
      <div className="drawer-head">
        <span>Menu</span>
        <button className="drawer-close" onClick={onClose} aria-label="Tutup">&times;</button>
      </div>
      <nav className="drawer-nav">
        {sections.map((s) => (
          <a
            key={s.id}
            href="javascript:void(0)"
            className={'nav-item' + (active === s.id ? ' active' : '')}
            onClick={() => onSelect(s.id)}
          >
            {s.label}
          </a>
        ))}
      </nav>
    </div>
  )
}
