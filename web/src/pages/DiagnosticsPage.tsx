import { useCallback, useEffect, useRef, useState } from 'react'
import { api } from '../api'
import { Empty, PageHeader, StatusDot } from '../components/Common'
import { recoveryLabel, statusLabel } from '../status'
import { formatBytes } from '../trafficFormat'
import type { Diagnostics, DoctorCheck, Overview } from '../types'

type EvidenceTab = 'connections' | 'logs' | 'operations' | 'providers'

const evidenceTabs = [
  { id: 'connections', label: '实时连接' },
  { id: 'logs', label: '进程日志' },
  { id: 'operations', label: '操作记录' },
  { id: 'providers', label: 'Providers' },
] as const satisfies ReadonlyArray<{ id: EvidenceTab; label: string }>

// mihomo can hold thousands of connections; render a bounded window and always
// state how many were left out rather than truncating silently.
const connectionDisplayLimit = 100

// The connection table is the only section that churns second to second, so it
// gets the same cadence as the dashboard traffic poll; logs and operations move
// slowly enough that a slower tick keeps them honest without the extra load.
const connectionsRefreshMs = 2_000
const evidenceRefreshMs = 5_000

export function DiagnosticsPage({ overview }: { overview: Overview | null }) {
  const [details, setDetails] = useState<Diagnostics | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [fetchedAt, setFetchedAt] = useState('')
  const [tab, setTab] = useState<EvidenceTab>('connections')
  const [logSource, setLogSource] = useState('')
  const [copied, setCopied] = useState('')
  const mounted = useRef(true)
  const requestID = useRef(0)

  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false }
  }, [])

  // A refresh can race the revision-driven reload; only the newest request wins.
  // Background polls run silent: they must not drive the refresh button's label,
  // which would otherwise flicker on every tick.
  const load = useCallback(async ({ silent = false }: { silent?: boolean } = {}) => {
    const id = ++requestID.current
    if (!silent) setLoading(true)
    try {
      const value = await api.diagnostics()
      if (id !== requestID.current || !mounted.current) return
      setDetails(value)
      setError('')
      setFetchedAt(new Date().toLocaleTimeString())
    } catch (cause) {
      // Keep the previous snapshot on screen instead of blanking the page —
      // a stale reading with a visible warning beats silent zeroes.
      if (id !== requestID.current || !mounted.current) return
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      // Whoever raised the flag lowers it: gating this on the request id would
      // strand the button on 读取中… whenever a poll supersedes a manual refresh.
      if (!silent && mounted.current) setLoading(false)
    }
  }, [])

  useEffect(() => { void load() }, [load, overview?.revision, overview?.status.gateway])

  // Evidence is a live view, not a snapshot taken when the page opened. Polling
  // stops while the window is hidden so a backgrounded GUI stops driving
  // mihomo's API and the runtime log reads.
  useEffect(() => {
    const interval = tab === 'connections' ? connectionsRefreshMs : evidenceRefreshMs
    let timer = 0
    const stop = () => {
      window.clearInterval(timer)
      timer = 0
    }
    const start = () => {
      stop()
      timer = window.setInterval(() => void load({ silent: true }), interval)
    }
    const onVisibility = () => {
      if (document.visibilityState === 'hidden') {
        stop()
        return
      }
      // On return the screen is as stale as the pause was long; refill at once
      // rather than showing old numbers for another full interval.
      void load({ silent: true })
      start()
    }
    if (document.visibilityState !== 'hidden') start()
    document.addEventListener('visibilitychange', onVisibility)
    return () => {
      stop()
      document.removeEventListener('visibilitychange', onVisibility)
    }
  }, [load, tab])

  const doctor = overview?.doctor ?? []
  const failures = doctor.filter(check => !check.ok)
  const orderedDoctor = [...failures, ...doctor.filter(check => check.ok)]
  const providers = overview?.providers.proxy_providers ?? []
  const degradedProviders = providers.filter(provider => !provider.proxies.some(proxy => proxy.alive))
  const connections = details?.connections.connections ?? []
  const connectionTotal = details?.connections.total ?? connections.length
  // overview is polled independently and carries the same two fields, so prefer
  // it: reading them off `details` would show the evidence snapshot's age even
  // when a fresher value is already on hand.
  const recoveryStage = overview?.recovery.stage ?? details?.recovery.stage ?? 'idle'
  const logSources = Object.keys(details?.logs ?? {})
  const activeLog = logSources.includes(logSource) ? logSource : logSources[0] ?? ''
  const logLines = details?.logs[activeLog] ?? []

  // Optional-chaining the clipboard away would await `undefined` and report a
  // success that never happened, so treat a missing API as an explicit failure.
  const copy = async (name: string, text: string) => {
    try {
      if (!navigator.clipboard) throw new Error('clipboard unavailable')
      await navigator.clipboard.writeText(text)
      setCopied(name)
    } catch {
      setCopied(`${name}:failed`)
    }
    window.setTimeout(() => { if (mounted.current) setCopied('') }, 2400)
  }

  const copyLabel = (name: string, idle: string) => copied === name ? '已复制' : copied === `${name}:failed` ? '复制失败' : idle

  const refreshProvider = async (name: string) => {
    try {
      await api.refreshProvider(name)
      await load()
    } catch (cause) {
      if (mounted.current) setError(cause instanceof Error ? cause.message : String(cause))
    }
  }

  return <>
    <PageHeader
      eyebrow="DIAGNOSTICS"
      title="诊断、连接与 Provider"
      description="错误保持结构化；日志经过已知凭据脱敏，菜单栏只复制压缩摘要。"
      action={<button type="button" className="ghost-action" onClick={() => void load()} disabled={loading}>{loading ? '读取中…' : '刷新证据'}</button>}
    />

    <div className="diagnostics-workbench">
      <div className="diagnostics-rail">
        <article className={`diagnostics-verdict ${failures.length ? 'attention' : ''}`} aria-label="诊断结论">
          <span className="verdict-orb" aria-hidden="true">{failures.length ? '!' : '✓'}</span>
          <div className="verdict-copy">
            <small>诊断结论</small>
            <h2>{overview ? failures.length ? `${failures.length} 项未通过` : '基础检查通过' : '正在读取状态'}</h2>
            <p>网关{statusLabel(overview?.status.gateway, overview?.status.runtime_state)} · {recoveryLabel(recoveryStage)}</p>
          </div>
          <div className="verdict-actions">
            <button type="button" className="primary" onClick={() => void copy('summary', buildSummary(overview, details, recoveryStage))}>{copyLabel('summary', '复制诊断摘要')}</button>
          </div>
        </article>

        <section className="section">
          <div className="rail-head"><h2>Doctor</h2><span className={`pill ${failures.length ? 'bad' : 'ok'}`}>{failures.length ? `${failures.length} 项待处理` : `${doctor.length} 项通过`}</span></div>
          {orderedDoctor.length ? <div className="doctor-grid">
            {orderedDoctor.map(check => <div className={`check ${check.ok ? '' : 'bad'}`} key={check.name}>
              <span className={check.ok ? 'ok-mark' : 'bad-mark'} aria-hidden="true">{check.ok ? '✓' : '!'}</span>
              <div><strong>{check.name}</strong>{check.message && <small>{check.message}</small>}</div>
            </div>)}
          </div> : <Empty text="正在读取基础检查…" />}
          <dl className="rail-summary">
            <div><dt>恢复阶段</dt><dd>{recoveryLabel(recoveryStage)}</dd></div>
            <div><dt>活跃连接</dt><dd>{details ? `${connectionTotal} 条` : '—'}</dd></div>
            <div><dt>Providers</dt><dd>{providers.length ? `${providers.length} 个${degradedProviders.length ? ` · ${degradedProviders.length} 个降级` : ''}` : '—'}</dd></div>
            <div><dt>配置版本</dt><dd>{overview?.revision ?? details?.revision ?? '—'}</dd></div>
          </dl>
        </section>
      </div>

      <section className="diagnostics-evidence" aria-label="诊断证据">
        <header className="evidence-head">
          <div><h2>证据</h2><p>{freshnessLabel(tab, fetchedAt, Boolean(error))}</p></div>
          <div className="segmented" role="group" aria-label="选择证据类型">
            {evidenceTabs.map(item => <button key={item.id} type="button" aria-pressed={tab === item.id} onClick={() => setTab(item.id)}>{item.label}</button>)}
          </div>
        </header>

        <div className="evidence-body">
          {error && <div className="stale-bar" role="status">
            <span aria-hidden="true">!</span>
            <p><strong>证据数据获取失败</strong>{details ? `：自动刷新已中断，下方是 ${fetchedAt} 的快照。左侧结论来自 overview 轮询，仍是最新的。` : `：${error}`}</p>
            <button type="button" className="ghost-action" onClick={() => void load()} disabled={loading}>重试</button>
          </div>}

          {tab === 'connections' && (details ? <>
            {details.connection_error && <div className="notice warn">{details.connection_error}</div>}
            {connections.length ? <>
              <div className="conn-table-wrap">
                <table className="conn-table">
                  <thead><tr><th>命中规则</th><th>目标</th><th>出口链路</th><th className="conn-num">上传</th><th className="conn-num">下载</th></tr></thead>
                  <tbody>
                    {connections.slice(0, connectionDisplayLimit).map(connection => <tr key={connection.id}>
                      <td><code>{connection.rule || 'MATCH'}</code></td>
                      <td className="conn-target">{connectionTarget(connection) || '—'}</td>
                      <td className="conn-chain">{(connection.chains ?? []).join(' → ') || '—'}</td>
                      <td className="conn-num">{formatBytes(connection.upload)}</td>
                      <td className="conn-num">{formatBytes(connection.download)}</td>
                    </tr>)}
                  </tbody>
                </table>
              </div>
              <div className="conn-foot">
                <strong>合计 ↑ {formatBytes(details.connections.upload_total)} · ↓ {formatBytes(details.connections.download_total)}</strong>
                <span>{connectionTotal > Math.min(connections.length, connectionDisplayLimit) ? `按累计流量显示前 ${Math.min(connections.length, connectionDisplayLimit)} / 共 ${connectionTotal} 条` : `共 ${connectionTotal} 条`}</span>
              </div>
            </> : <Empty text={details.connection_error ? '连接数据不可用' : '当前没有活跃连接'} />}
          </> : <Empty text="正在读取连接…" />)}

          {tab === 'logs' && (logSources.length ? <>
            <div className="log-head">
              <div className="segmented" role="group" aria-label="选择日志来源">
                {logSources.map(name => <button key={name} type="button" aria-pressed={name === activeLog} onClick={() => setLogSource(name)}>{name}</button>)}
              </div>
              <button type="button" className="ghost-action" onClick={() => void copy('log', logLines.join('\n'))} disabled={!logLines.length}>{copyLabel('log', '复制本段日志')}</button>
            </div>
            <div className="log-pane" tabIndex={0} role="region" aria-label={`${activeLog} 日志`}>
              <pre>{logLines.join('\n') || '暂无日志输出'}</pre>
            </div>
          </> : <Empty text={details ? '暂无日志来源' : '正在读取日志…'} />)}

          {tab === 'operations' && (details ? details.operations.length ? <div className="evidence-rows">
            {details.operations.map(operation => <div className="row" key={operation.id}>
              <StatusDot status={operation.state === 'failed' ? 'degraded' : operation.state === 'succeeded' ? 'running' : 'stopped'} />
              <div className="grow">
                <strong>{operationKindLabel(operation.kind)} · {operationStateLabel(operation.state)}</strong>
                <small>{operation.id}{operation.error ? ` · ${operation.error}` : ''}</small>
              </div>
              <span className="stamp">{formatStamp(operation.updated_at)}</span>
            </div>)}
          </div> : <Empty text="尚无生命周期操作记录" /> : <Empty text="正在读取操作记录…" />)}

          {tab === 'providers' && (providers.length ? <div className="evidence-rows">
            {providers.map(provider => <div className="row" key={provider.name}>
              <StatusDot status={provider.proxies.some(proxy => proxy.alive) ? 'running' : 'degraded'} />
              <div className="grow"><strong>{provider.name}</strong><small>{provider.proxy_count} proxies · {provider.vehicle_type}</small></div>
              <button type="button" className="ghost-action" onClick={() => void refreshProvider(provider.name)}>刷新</button>
            </div>)}
          </div> : <Empty text="尚未配置 Proxy Provider" />)}
        </div>
      </section>
    </div>
  </>
}

// The host/port fallback across mihomo's metadata keys is resolved server-side;
// the table only has to join the two halves.
function connectionTarget(connection: { host?: string; port?: string }) {
  if (!connection.host) return ''
  return connection.port ? `${connection.host}:${connection.port}` : connection.host
}

// Say how fresh the panel is and how often it updates, so a still number reads
// as "nothing changed" rather than "this page stopped loading".
function freshnessLabel(tab: EvidenceTab, fetchedAt: string, failed: boolean) {
  if (!fetchedAt) return '正在读取证据…'
  if (failed) return `自动刷新已中断 · 数据停留在 ${fetchedAt}`
  const seconds = (tab === 'connections' ? connectionsRefreshMs : evidenceRefreshMs) / 1000
  return `每 ${seconds} 秒自动刷新 · 最后更新 ${fetchedAt}`
}

function operationKindLabel(kind: string) {
  return ({ start: '启动网关', stop: '停止网关', reload: '重载配置', 'restart-mihomo': '重启 mihomo' } as Record<string, string>)[kind] ?? kind
}

function operationStateLabel(state: string) {
  return ({ running: '进行中', succeeded: '成功', failed: '失败' } as Record<string, string>)[state] ?? state
}

// Operations are kept for 20 entries, which can span days — a bare clock time
// would read as "just now" for a failure from last week.
function formatStamp(value?: string) {
  if (!value) return ''
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return value
  const sameDay = parsed.toDateString() === new Date().toDateString()
  return sameDay ? parsed.toLocaleTimeString() : `${parsed.toLocaleDateString()} ${parsed.toLocaleTimeString()}`
}

function buildSummary(overview: Overview | null, details: Diagnostics | null, recoveryStage: string) {
  const status = overview?.status
  const doctor = overview?.doctor ?? []
  const failures = doctor.filter(check => !check.ok)
  const lines = [
    'OpenSurge for Mac 诊断摘要',
    `采集时间: ${new Date().toISOString()}`,
    `网关: ${statusLabel(status?.gateway, status?.runtime_state)} · 接口 ${status?.interface ?? '—'} · ${status?.lan_ip ?? '—'}`,
    `恢复阶段: ${recoveryStage}`,
    `配置版本: ${details?.revision ?? overview?.revision ?? '—'}`,
    '',
    `Doctor (${failures.length}/${doctor.length} 未通过)`,
    ...doctor.map(summariseCheck),
  ]
  if (details) {
    lines.push(
      '',
      `连接: ${details.connections.connections.length} 条 · 上传 ${formatBytes(details.connections.upload_total)} · 下载 ${formatBytes(details.connections.download_total)}`,
    )
    if (details.connection_error) lines.push(`连接读取错误: ${details.connection_error}`)
    const failedOperations = details.operations.filter(operation => operation.state === 'failed')
    if (failedOperations.length) {
      lines.push('', '失败的操作:')
      lines.push(...failedOperations.map(operation => `  ${operation.kind} · ${operation.updated_at}${operation.error ? ` · ${operation.error}` : ''}`))
    }
  }
  return lines.join('\n')
}

function summariseCheck(check: DoctorCheck) {
  return `  [${check.ok ? 'OK' : '!!'}] ${check.name}${check.message ? ` — ${check.message}` : ''}`
}
