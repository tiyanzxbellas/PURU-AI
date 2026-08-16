const PAGES = {
  providers: { title: 'Providers', sub: 'PURU Gateway built-in & OpenAI-compatible endpoints · live /models catalog', icon: 'dns' },
  combos: { title: 'Combos', sub: 'Group models from providers under one name with a strategy', icon: 'layers' },
  usage: { title: 'Usage & Tokens', sub: 'Token consumption & request history', icon: 'bar_chart' },
  logs: { title: 'Server Logs', sub: 'Live console output from the bot process', icon: 'terminal' },
  role: { title: 'System Prompt', sub: 'Custom role / instructions for the agent', icon: 'record_voice_over' },
  skills: { title: 'Agent Skills', sub: 'Search, install & manage skills', icon: 'extension' },
  files: { title: 'Files', sub: 'Virtual file system & memory', icon: 'folder_open' },
}

export default function Header({ section, onMenu, theme, onToggleTheme }) {
  const page = PAGES[section] || PAGES.providers
  return (
    <header className="header">
      <div className="header-left">
        <button className="menu-btn" onClick={onMenu} aria-label="Menu">
          <span className="ms">menu</span>
        </button>
        <div className="page-title">
          <span className="ms">{page.icon}</span>
          <div>
            <h1>{page.title}</h1>
            <div className="page-sub">{page.sub}</div>
          </div>
        </div>
      </div>
      <div className="header-right">
        <button
          className="icon-btn"
          onClick={onToggleTheme}
          aria-label="Toggle theme"
          title={theme === 'dark' ? 'Mode terang' : 'Mode gelap'}
        >
          <span className="ms">{theme === 'dark' ? 'light_mode' : 'dark_mode'}</span>
        </button>
      </div>
    </header>
  )
}