import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, authenticationRequiredEvent, RequestError } from './api'
import { PageErrorBoundary } from './components/PageErrorBoundary'
import { RecoveryBanner, StatusDot } from './components/Common'
import { RailIcon, RailIconSprite } from './components/RailIcons'
import { ShellSlots } from './components/ShellSlots'
import { DashboardPage } from './pages/DashboardPage'
import { ConnectivityPage } from './pages/ConnectivityPage'
import { DevicesPage } from './pages/DevicesPage'
import { PairedDevicesPage } from './pages/PairedDevicesPage'
import { DiagnosticsPage } from './pages/DiagnosticsPage'
import { NetworkPage } from './pages/NetworkPage'
import { PoliciesPage } from './pages/PoliciesPage'
import { SourcesPage } from './pages/SourcesPage'
import { needsNetworkRecoveryWarning } from './recovery'
import { statusLabel } from './status'
import type { Overview } from './types'

type Page = 'dashboard' | 'network' | 'sources' | 'devices' | 'paired' | 'policies' | 'connectivity' | 'diagnostics'
type Theme = 'dark' | 'light'
type NetworkNavigationTarget = 'none' | 'control' | 'bottom'

// Two groups rather than one flat list of seven: watching the gateway and
// changing it are different tasks, and the separator keeps that readable even
// when only the icons are visible.
const navGroups = [
  [
    { id: 'dashboard', label: '总览', icon: 'dashboard' },
    { id: 'connectivity', label: '连通性', icon: 'connectivity' },
    { id: 'diagnostics', label: '诊断', icon: 'diagnostics' },
  ],
  [
    { id: 'network', label: '网络设置', icon: 'network' },
    { id: 'sources', label: '代理与规则源', icon: 'sources' },
    { id: 'devices', label: '设备', icon: 'devices' },
    { id: 'paired', label: '已配对设备', icon: 'paired' },
    { id: 'policies', label: '策略', icon: 'policies' },
  ],
] as const satisfies ReadonlyArray<ReadonlyArray<{ id: Page; label: string; icon: string }>>

const nav = navGroups.flat()

function currentPage(): Page {
  const candidate = window.location.pathname.split('/').filter(Boolean)[0] as Page | undefined
  return nav.some(item => item.id === candidate) ? candidate! : 'dashboard'
}

function initialTheme(): Theme {
  const stored = window.localStorage.getItem('opensurge-theme')
  if (stored === 'dark' || stored === 'light') return stored
  return typeof window.matchMedia === 'function' && window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
}

function focusGatewayControl(target: Exclude<NetworkNavigationTarget, 'none'>) {
  const control = document.getElementById('gateway-control')
  if (!(control instanceof HTMLButtonElement)) return
  const reducedMotion = typeof window.matchMedia === 'function' && window.matchMedia('(prefers-reduced-motion: reduce)').matches
  if (target === 'bottom') {
    window.scrollTo?.({ top: document.documentElement.scrollHeight, behavior: reducedMotion ? 'auto' : 'smooth' })
  } else {
    control.scrollIntoView?.({ behavior: reducedMotion ? 'auto' : 'smooth', block: 'center' })
  }
  if (!control.disabled) control.focus({ preventScroll: true })
}

function networkNavigationHash(target: NetworkNavigationTarget) {
  if (target === 'control') return '#gateway-control'
  if (target === 'bottom') return '#gateway-control-bottom'
  return ''
}

export function App() {
  const [page, setPage] = useState<Page>(currentPage)
  const [overview, setOverview] = useState<Overview | null>(null)
  const [error, setError] = useState('')
  const [authenticationRequired, setAuthenticationRequired] = useState(false)
  const [theme, setTheme] = useState<Theme>(initialTheme)
  const [devicesDirty, setDevicesDirty] = useState(false)
  // Command-bar slots. Callback refs rather than useRef so the first commit
  // that mounts them re-renders the pages holding the portals.
  const [titleSlot, setTitleSlot] = useState<HTMLElement | null>(null)
  const [actionSlot, setActionSlot] = useState<HTMLElement | null>(null)
  const pageRef = useRef(page)
  const devicesDirtyRef = useRef(devicesDirty)
  pageRef.current = page
  devicesDirtyRef.current = devicesDirty

  const slots = useMemo(() => ({ title: titleSlot, action: actionSlot }), [titleSlot, actionSlot])

  useEffect(() => {
    document.documentElement.dataset.theme = theme
    window.localStorage.setItem('opensurge-theme', theme)
  }, [theme])

  const refresh = useCallback(async () => {
    try {
      setOverview(await api.overview())
      setError('')
    } catch (cause) {
      if (cause instanceof RequestError && cause.status === 401) {
        setAuthenticationRequired(true)
        setError('')
        return
      }
      setError(cause instanceof Error ? cause.message : String(cause))
    }
  }, [])

  useEffect(() => {
    const requireAuthentication = () => {
      setAuthenticationRequired(true)
      setError('')
    }
    window.addEventListener(authenticationRequiredEvent, requireAuthentication)
    return () => window.removeEventListener(authenticationRequiredEvent, requireAuthentication)
  }, [])

  useEffect(() => {
    if (authenticationRequired) return
    void refresh()
    const timer = window.setInterval(() => void refresh(), 8000)
    const events = typeof EventSource === 'undefined' ? null : new EventSource('/api/v1/events')
    events?.addEventListener('state', () => void refresh())
    const onPop = () => {
      const next = currentPage()
      if (pageRef.current === 'devices' && next !== 'devices' && devicesDirtyRef.current && !window.confirm('设备页还有尚未保存的修改，确定离开并放弃这些修改吗？')) {
        history.pushState({}, '', '/devices')
        return
      }
      if (pageRef.current === 'devices' && next !== 'devices') setDevicesDirty(false)
      setPage(next)
    }
    window.addEventListener('popstate', onPop)
    return () => {
      window.clearInterval(timer)
      events?.close()
      window.removeEventListener('popstate', onPop)
    }
  }, [authenticationRequired, refresh])

  const go = (next: Page, networkTarget: NetworkNavigationTarget = 'none') => {
    if (next === page) {
      if (networkTarget !== 'none') {
        history.replaceState({}, '', `/${next}${networkNavigationHash(networkTarget)}`)
        focusGatewayControl(networkTarget)
      }
      return
    }
    if (page === 'devices' && next !== 'devices' && devicesDirty && !window.confirm('设备页还有尚未保存的修改，确定离开并放弃这些修改吗？')) return
    if (page === 'devices' && next !== 'devices') setDevicesDirty(false)
    history.pushState({}, '', `/${next}${networkNavigationHash(networkTarget)}`)
    setPage(next)
  }

  return <ShellSlots.Provider value={slots}>
    <div className="app-shell">
      <RailIconSprite />
      <nav className="rail" aria-label="OpenSurge sections">
        <img className="rail-mark" src="/opensurge-icon.png" alt="" aria-hidden="true" />
        {navGroups.map((group, index) => <div className="rail-group" key={group[0].id}>
          {index > 0 && <span className="rail-sep" aria-hidden="true" />}
          {group.map(item => <button
            key={item.id}
            type="button"
            className={page === item.id ? 'rail-item active' : 'rail-item'}
            aria-label={item.label}
            aria-current={page === item.id ? 'page' : undefined}
            onClick={() => go(item.id)}
          >
            <RailIcon name={item.icon} />
            <span className="rail-tip" aria-hidden="true">{item.label}</span>
          </button>)}
        </div>)}
        <div className="rail-foot">
          <button
            type="button"
            className="rail-item"
            aria-pressed={theme === 'light'}
            aria-label={theme === 'dark' ? '切换为浅色模式' : '切换为深色模式'}
            onClick={() => setTheme(current => current === 'dark' ? 'light' : 'dark')}
          >
            <RailIcon name="theme" />
            <span className="rail-tip" aria-hidden="true">{theme === 'dark' ? '浅色模式' : '深色模式'}</span>
          </button>
        </div>
      </nav>

      <div className="stage">
        <header className="cmdbar">
          <div className="cmd-title" ref={setTitleSlot} />
          <div className="status-chip">
            <StatusDot status={overview?.status.gateway ?? 'unreachable'} />
            <strong>{statusLabel(overview?.status.gateway, overview?.status.runtime_state)}</strong>
            <code>{overview?.status.lan_ip || 'Control API'}</code>
          </div>
          <div className="cmd-action" ref={setActionSlot} />
        </header>

        <main className="workspace">
          {authenticationRequired ? <section className="session-expired" role="alert"><span aria-hidden="true">!</span><div><h1>Web GUI 与 OpenSurge 的安全连接已过期</h1><p>请点击 macOS 菜单栏中的 OpenSurge 图标，然后选择“打开 OpenSurge 面板”。</p></div></section> : <>
            {overview?.recovery.required && needsNetworkRecoveryWarning(overview.recovery.stage) && <RecoveryBanner recovery={overview.recovery.stage} onOpen={() => go('network', 'control')} />}
            {error && <div className="error-banner" role="alert"><span>!</span><p>{error}</p><button onClick={() => void refresh()}>重试</button></div>}
            <PageErrorBoundary key={page}>
              {page === 'dashboard' && <DashboardPage overview={overview} onOpenNetwork={action => go('network', action === 'cleanup' ? 'control' : action === 'stop' ? 'bottom' : 'none')} />}
              {page === 'network' && <NetworkPage overview={overview} onChanged={refresh} onNavigate={() => go('devices')} />}
              {page === 'sources' && <SourcesPage overview={overview} onChanged={refresh} />}
              {page === 'devices' && <DevicesPage overview={overview} onChanged={refresh} onNavigate={go} onDirtyChange={setDevicesDirty} />}
              {page === 'paired' && <PairedDevicesPage />}
              {page === 'policies' && <PoliciesPage overview={overview} onChanged={refresh} />}
              {page === 'connectivity' && <ConnectivityPage overview={overview} onChanged={refresh} />}
              {page === 'diagnostics' && <DiagnosticsPage overview={overview} />}
            </PageErrorBoundary>
          </>}
        </main>
      </div>
    </div>
  </ShellSlots.Provider>
}
