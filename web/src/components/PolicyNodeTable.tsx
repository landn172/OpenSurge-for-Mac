import { useMemo, useState } from 'react'
import { testedAgo } from '../proxyHealth'
import type { ProxyGroup, ProxyHealthEntry } from '../types'
import { Empty } from './Common'
import { ProxyHealthBadge } from './ProxyHealthBadge'

type SortKey = 'default' | 'name' | 'delay' | 'tested'
type SortDirection = 'asc' | 'desc'

type PolicyNodeTableProps = {
  group: ProxyGroup
  search: string
  healthByName: Map<string, ProxyHealthEntry>
  testing: Set<string>
  onTest: (names: string[]) => Promise<void>
  onSelect: (policy: string) => Promise<void>
}

export function PolicyNodeTable({ group, search, healthByName, testing, onTest, onSelect }: PolicyNodeTableProps) {
  const [switching, setSwitching] = useState('')
  const [error, setError] = useState('')
  // Picking the fastest usable node is what this table is for, so it opens
  // sorted by latency; untested nodes rank last and keep mihomo's own order.
  const [sort, setSort] = useState<SortKey>('delay')
  const [direction, setDirection] = useState<SortDirection>('asc')

  const manual = ['selector', 'select'].includes(group.type.toLowerCase())
  const probeable = useMemo(() => group.options.filter(option => healthByName.get(option)?.probeable), [group.options, healthByName])
  const reachable = probeable.filter(option => healthByName.get(option)?.status === 'reachable').length

  const options = useMemo(() => {
    const query = search.trim().toLowerCase()
    const matched = !query || group.name.toLowerCase().includes(query)
      ? group.options
      : group.options.filter(option => option.toLowerCase().includes(query))
    return sortOptions(matched, sort, direction, healthByName)
  }, [group.name, group.options, search, sort, direction, healthByName])

  const applySort = (key: Exclude<SortKey, 'default'>) => {
    if (sort !== key) {
      setSort(key)
      // Latency is the reason this table exists: first click should surface the
      // fastest node, while the text columns read naturally A→Z.
      setDirection('asc')
      return
    }
    if (direction === 'asc') { setDirection('desc'); return }
    setSort('default')
    setDirection('asc')
  }

  const select = async (policy: string) => {
    if (!manual || policy === group.selected) return
    setSwitching(policy); setError('')
    try { await onSelect(policy) }
    catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)) }
    finally { setSwitching('') }
  }

  return <section className="node-panel" aria-label={`${group.name} 的节点`}>
    <header className="node-panel-head">
      <div>
        <span className="group-kicker"><span>{group.type}</span>{group.name.startsWith('device/') && <span>设备策略</span>}</span>
        <h2>{group.name}</h2>
        <p>当前出口 <strong>{group.selected || '未选择'}</strong>{manual ? ' · 点击行即时切换' : ' · 自动策略组，候选仅供查看'}</p>
      </div>
      <div className="node-panel-actions">
        <span className="group-ratio"><strong>{reachable}</strong> / {probeable.length} 可达</span>
        <button type="button" className="ghost-action" disabled={!probeable.length || probeable.some(name => testing.has(name))} onClick={() => void onTest(probeable)}>检测本组 {probeable.length} 个节点</button>
      </div>
    </header>

    <div className="node-panel-body">
      {error && <div className="notice warn" role="alert">{error}</div>}
      {options.length ? <>
        <div className="node-grid">
          <div className="node-grid-head">
            <span><span className="sr-only">选中状态</span></span>
            <SortHeader label="节点" column="name" sort={sort} direction={direction} onSort={applySort} />
            <span>协议</span>
            <SortHeader label="延迟" column="delay" sort={sort} direction={direction} onSort={applySort} />
            <SortHeader label="上次检测" column="tested" sort={sort} direction={direction} onSort={applySort} />
          </div>
          {options.map(option => {
            const health = healthByName.get(option)
            const selected = option === group.selected
            const chained = health?.selected && health.selected !== option ? health.selected : ''
            return <button
              className={`node-row ${selected ? 'selected' : ''}`}
              type="button"
              key={option}
              aria-label={`${group.name} 选择 ${option}`}
              aria-pressed={selected}
              disabled={!manual || Boolean(switching)}
              onClick={() => void select(option)}
            >
              <span className="node-pick" aria-hidden="true">{selected ? '✓' : ''}</span>
              <span className="node-name"><strong>{option}</strong>{(chained || health?.provider) && <small>{chained ? `当前链路 → ${chained}` : health?.provider}</small>}</span>
              <span className="node-protocols">{health?.type && <span className="protocol-chip">{health.type}</span>}{health?.udp && <span className="protocol-chip">UDP</span>}</span>
              <span className="node-delay"><ProxyHealthBadge health={health} testing={testing.has(option)} compact /></span>
              <span className="node-tested">{testedAgo(health?.tested_at)}</span>
              {switching === option && <span className="switch-overlay">切换中…</span>}
            </button>
          })}
        </div>
        <div className="node-grid-foot">
          <span>{sortLabel(sort, direction)} · {options.length} / {group.options.length} 个节点</span>
          <span>DIRECT 等内置出口不参与延迟检测</span>
        </div>
      </> : <Empty text="这个策略组中没有匹配的节点" />}
    </div>
  </section>
}

function SortHeader({ label, column, sort, direction, onSort }: {
  label: string
  column: Exclude<SortKey, 'default'>
  sort: SortKey
  direction: SortDirection
  onSort: (key: Exclude<SortKey, 'default'>) => void
}) {
  const active = sort === column
  const state = active ? direction === 'asc' ? '当前升序' : '当前降序' : '未排序'
  return <span>
    <button type="button" className={`sort-header ${active ? 'active' : ''}`} aria-label={`按${label}排序，${state}`} onClick={() => onSort(column)}>
      {label}<span className="sort-mark" aria-hidden="true">{active ? direction === 'asc' ? '▲' : '▼' : '⇅'}</span>
    </button>
  </span>
}

function sortOptions(options: string[], sort: SortKey, direction: SortDirection, healthByName: Map<string, ProxyHealthEntry>) {
  if (sort === 'default') return options
  // Array.prototype.sort is stable, so equal keys keep mihomo's own ordering.
  return [...options].sort((left, right) => {
    if (sort === 'name') {
      const result = left.localeCompare(right, 'zh-Hans-CN')
      return direction === 'asc' ? result : -result
    }
    const leftRank = sort === 'delay' ? delayRank(healthByName.get(left)) : testedRank(healthByName.get(left))
    const rightRank = sort === 'delay' ? delayRank(healthByName.get(right)) : testedRank(healthByName.get(right))
    // Untested and unreachable nodes carry no measurement, so they stay at the
    // bottom in both directions instead of flooding the top when reversed.
    const leftMissing = !Number.isFinite(leftRank)
    const rightMissing = !Number.isFinite(rightRank)
    if (leftMissing !== rightMissing) return leftMissing ? 1 : -1
    if (leftMissing) return 0
    return direction === 'asc' ? leftRank - rightRank : rightRank - leftRank
  })
}

function delayRank(health?: ProxyHealthEntry) {
  return health?.status === 'reachable' ? health.delay_ms ?? 0 : Number.POSITIVE_INFINITY
}

function testedRank(health?: ProxyHealthEntry) {
  const parsed = health?.tested_at ? Date.parse(health.tested_at) : Number.NaN
  return Number.isNaN(parsed) ? Number.POSITIVE_INFINITY : -parsed
}

function sortLabel(sort: SortKey, direction: SortDirection) {
  if (sort === 'default') return '按策略组原始顺序'
  const key = sort === 'name' ? '按名称' : sort === 'delay' ? '按延迟' : '按检测时间'
  return `${key}${direction === 'asc' ? '升序' : '降序'}`
}
