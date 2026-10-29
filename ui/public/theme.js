(() => {
  const apply = () => {
    let theme = 'system'
    try { theme = localStorage.getItem('moneybag-theme') || 'system' } catch { /* Storage may be unavailable. */ }
    document.documentElement.classList.toggle('dark', theme === 'dark' || (theme !== 'light' && matchMedia('(prefers-color-scheme: dark)').matches))
  }
  apply()
  matchMedia('(prefers-color-scheme: dark)').addEventListener('change', apply)
})()
