import { useCallback, useEffect, useMemo, useState } from 'react'
import { api } from '../api'
import { Empty, PageHeader } from '../components/Common'
import { OutletSummary } from '../components/OutletSummary'
import { PolicyNodeTable } from '../components/PolicyNodeTable'
import { useProxyHealth } from '../hooks/useProxyHealth'
import type { LocalRouting, LocalRoutingMode, Overview, ProxyGroup, ProxyHealthEntry } from '../types'

type PolicyScope = 'all' | 'global' | 'device'

const scopeOptions = [['global', '全局策略'], ['device', '设备策略'], ['all', '全部']] as const

const localModes: ReadonlyArray<{ id: LocalRoutingMode; label: string; description: string }> = [
  { id: 'rule', label: '按规则', description: '根据网站和网关规则自动分流' },
  { id: 'global', label: '固定出口', description: '本机公网流量统一使用当前全局策略' },
  { id: 'direct', label: '本机直连', description: '本机公网流量不使用代理' },
]

export function PoliciesPage({ overview, onChanged }: { overview: Overview | null; onChanged: () => Promise<void> }) {
  const [search, setSearch] = useState('')
  const [scope, setScope] = useState<PolicyScope>('global')
  const [selectedGroup, setSelectedGroup] = useState('')
  const { byName, testing, error, refresh, test } = useProxyHealth()
  const groups = overview?.policies ?? []

  const filteredGroups = useMemo(() => groups.filter(group => {
    const device = group.name.startsWith('device/')
    if (scope === 'global' && device) return false
    if (scope === 'device' && !device) return false
    const query = search.trim().toLowerCase()
    return !query || group.name.toLowerCase().includes(query) || group.options.some(option => option.toLowerCase().includes(query))
  }), [groups, scope, search])

  // Derived rather than stored, so changing scope or search cannot leave the
  // panel pointing at a group that is no longer in the rail.
  const activeGroup = filteredGroups.find(group => group.name === selectedGroup) ?? filteredGroups[0] ?? null

  const visibleNames = useMemo(() => [...new Set(filteredGroups.flatMap(group => group.options))], [filteredGroups])
  const testableNames = useMemo(() => visibleNames.filter(name => byName.get(name)?.probeable), [visibleNames, byName])
  const reachable = visibleNames.filter(name => byName.get(name)?.status === 'reachable').length
  const tested = visibleNames.filter(name => {
    const status = byName.get(name)?.status
    return status && status !== 'untested' && status !== 'not_applicable'
  }).length
  const untested = testableNames.length - tested

  const select = async (group: string, policy: string) => {
    await api.selectPolicy(group, policy)
    await Promise.all([onChanged(), refresh()])
  }

  return <>
    <PageHeader eyebrow="POLICIES" title="策略与节点健康" description="查看每个策略组的当前出口、节点延迟与可达性；Selector 节点点击后即时生效。" action={<button className="primary" type="button" disabled={!testableNames.length || testableNames.some(name => testing.has(name))} onClick={() => void test(testableNames)}>{testing.size ? `正在检测 ${testing.size} 个节点…` : '检测当前视图'}</button>} />

    <p className="evidence-note"><strong>检测范围：</strong>延迟由网关 Mac 上的 mihomo 访问探测地址得到；它不代表某台下游设备的 DHCP、DNS 或 TUN 路径已经完成端到端验收。</p>

    <LocalMacOutlet
      running={overview?.status.gateway === 'running'}
      healthByName={byName}
      testing={testing}
      onTest={test}
      onChanged={async () => { await onChanged(); await refresh() }}
    />

    <section className="policy-health-overview" aria-label="节点健康概览">
      <div><small>当前视图</small><strong>{filteredGroups.length}</strong><span>个策略组</span></div>
      <div><small>已检测</small><strong>{tested}</strong><span>个出口</span></div>
      <div><small>当前可达</small><strong>{reachable}</strong><span>个出口</span></div>
    </section>
    <div className="health-legend">
      <span><i className="legend-dot excellent" />快速 ≤250ms</span>
      <span><i className="legend-dot good" />可用 ≤650ms</span>
      <span><i className="legend-dot slow" />较慢 ≤1500ms</span>
      <span><i className="legend-dot unreachable" />不可达</span>
    </div>

    <section className="policy-toolbar">
      <label className="policy-search"><span className="sr-only">搜索策略组或节点</span><input type="search" value={search} placeholder="搜索策略组或节点" onChange={event => setSearch(event.target.value)} /></label>
      <div className="segmented" aria-label="策略组范围">
        {scopeOptions.map(([value, label]) => <button type="button" key={value} aria-pressed={scope === value} onClick={() => setScope(value)}>{label}</button>)}
      </div>
    </section>

    {error && <div className="notice warn" role="alert">节点健康暂不可用：{error}</div>}

    {untested > 0 && tested === 0 && <div className="untested-prompt">
      <span aria-hidden="true">◌</span>
      <div>
        <strong>当前视图有 {untested} 个节点尚未检测</strong>
        <small>打开页面时不会自动发起探测。检测一次之后，就可以按延迟排序挑出最快的可用节点。</small>
      </div>
      <button className="primary" type="button" disabled={testableNames.some(name => testing.has(name))} onClick={() => void test(testableNames)}>检测这 {testableNames.length} 个节点</button>
    </div>}

    {activeGroup ? <div className="policy-split">
      <nav className="group-rail" aria-label="策略组">
        <h2>策略组 · {filteredGroups.length}</h2>
        {filteredGroups.map(group => <GroupRailItem
          key={group.name}
          group={group}
          active={group.name === activeGroup.name}
          healthByName={byName}
          onSelect={() => setSelectedGroup(group.name)}
        />)}
      </nav>
      <PolicyNodeTable
        key={activeGroup.name}
        group={activeGroup}
        search={search.trim()}
        healthByName={byName}
        testing={testing}
        onTest={test}
        onSelect={policy => select(activeGroup.name, policy)}
      />
    </div> : <Empty text={groups.length ? '当前筛选没有匹配的策略组或节点' : 'mihomo 未运行或没有可选择的策略组'} />}
  </>
}

function GroupRailItem({ group, active, healthByName, onSelect }: {
  group: ProxyGroup
  active: boolean
  healthByName: Map<string, ProxyHealthEntry>
  onSelect: () => void
}) {
  const probeable = group.options.filter(option => healthByName.get(option)?.probeable)
  const reachable = probeable.filter(option => healthByName.get(option)?.status === 'reachable').length
  return <button
    type="button"
    className={`group-item ${active ? 'active' : ''}`}
    aria-label={`策略组 ${group.name}`}
    aria-current={active ? 'true' : undefined}
    onClick={onSelect}
  >
    <strong>{group.name}</strong>
    <span className="group-item-kind">{group.type}</span>
    <small>当前 {group.selected || '未选择'}</small>
    <span className="group-item-ratio">{probeable.length ? `${reachable} / ${probeable.length} 可达` : '无可检测候选'}</span>
  </button>
}

function LocalMacOutlet({ running, healthByName, testing, onTest, onChanged }: {
  running: boolean
  healthByName: Map<string, ProxyHealthEntry>
  testing: Set<string>
  onTest: (names: string[]) => Promise<void>
  onChanged: () => Promise<void>
}) {
  const [routing, setRouting] = useState<LocalRouting | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const refresh = useCallback(async () => {
    if (!running) {
      setRouting(null)
      setError('')
      return
    }
    try {
      setRouting(await api.localRouting())
      setError('')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    }
  }, [running])

  useEffect(() => { void refresh() }, [refresh])

  const applyMode = async (mode: LocalRoutingMode) => {
    setBusy(true); setError('')
    try {
      setRouting(await api.setLocalRouting(mode))
      await onChanged()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
      await refresh()
    } finally {
      setBusy(false)
    }
  }

  const select = async (policy: string) => {
    if (!routing) return
    const updated = await api.setLocalRouting(routing.mode, policy)
    setRouting(updated)
    await onChanged()
  }

  // The selection is written to the mac-global selector under every mode, but
  // only `global` routes local traffic through it — so say which one applies now
  // instead of making the operator open the devices page to find out.
  const pending = Boolean(routing) && routing?.mode !== 'global'

  return <section className="local-outlet-strip" aria-labelledby="policy-local-mac-title">
    <div className="local-outlet-kicker"><small>THIS MAC</small><h2 id="policy-local-mac-title">本机全局策略组</h2></div>
    <div className="local-mode-switch" role="group" aria-label="这台 Mac 的出口方式">
      {localModes.map(mode => <button
        key={mode.id}
        type="button"
        aria-pressed={routing?.mode === mode.id}
        title={mode.description}
        disabled={!running || !routing || busy || (mode.id === 'global' && !routing.available_modes.includes('global'))}
        onClick={() => void applyMode(mode.id)}
      >{mode.label}</button>)}
    </div>
    <div className="local-outlet-pick">
      {routing?.global_group
        ? <OutletSummary
            title="本机全局出口"
            ariaLabel={`本机全局策略组 当前策略 ${routing.global_group.selected}`}
            group={routing.global_group}
            healthByName={healthByName}
            testing={testing}
            onTest={onTest}
            onSelect={select}
          />
        : <button className="outlet-summary unavailable" type="button" disabled><span className="outlet-summary-copy"><small>本机全局出口</small><strong>{running ? '正在读取…' : '启动网关后可用'}</strong></span></button>}
    </div>
    {pending && <p className="mode-mismatch" role="status">
      <span aria-hidden="true">!</span>
      <span>当前是「{localModes.find(mode => mode.id === routing?.mode)?.label}」模式，这里选的全局出口<strong>会被保存但暂不生效</strong>；切换到「固定出口」后立即启用。</span>
    </p>}
    {error && <div className="notice warn local-outlet-error" role="alert">{error}</div>}
  </section>
}
