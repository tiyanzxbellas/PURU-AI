import { useEffect, useState } from 'react'

const BOOT = [
  '> PURU·AI CONTROL TERMINAL v2.0',
  '> RTDB LINK ...................... [OK]',
  '> AUTH CHANNEL ................... [VERIFIED]',
  '> FS.VFS ......................... [MOUNTED]',
  '> MEM // AGENT ................... [ARMED]',
  '> 4 CHANNELS ..................... [READY]',
].join('\n')

const BOOTED_KEY = 'puru_booted'

function canBoot() {
  if (window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches) return false
  try {
    return sessionStorage.getItem(BOOTED_KEY) !== '1'
  } catch {
    return true
  }
}

export default function Boot() {
  const [done, setDone] = useState(false)
  const [text, setText] = useState('')

  useEffect(() => {
    if (!canBoot()) {
      setDone(true)
      return
    }
    let i = 0
    const iv = setInterval(() => {
      i++
      setText(BOOT.slice(0, i))
      if (i >= BOOT.length) {
        clearInterval(iv)
        setTimeout(() => {
          try { sessionStorage.setItem(BOOTED_KEY, '1') } catch {}
          setDone(true)
        }, 520)
      }
    }, 15)
    return () => clearInterval(iv)
  }, [])

  if (done) return null
  return (
    <div className="boot" role="status" aria-live="off">
      <pre className="boot-out">{text}▮</pre>
    </div>
  )
}