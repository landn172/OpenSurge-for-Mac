import { connectivityStatusLabel, connectivityTone, routeLabel } from '../connectivity'
import type { ConnectivityResult, ConnectivityTarget } from '../types'

type ConnectivityTargetCardProps = {
  target: ConnectivityTarget
  result?: ConnectivityResult
  testing: boolean
  enforceBaseline: boolean
}

// Anything at or beyond this reads as "slow" rather than as a number worth
// comparing, so the bar saturates instead of compressing everything else.
const barCeilingMs = 800

export function ConnectivityTargetCard({ target, result, testing, enforceBaseline }: ConnectivityTargetCardProps) {
  const tone = connectivityTone(result, testing)
  const expected = enforceBaseline ? target.expected_route : 'any'
  const mismatch = Boolean(result && expected !== 'any' && result.route !== 'unknown' && result.route !== expected)
  const failed = Boolean(result) && tone === 'unreachable'
  const chain = failed ? '未建立连接' : result?.chain.length ? result.chain.join(' → ') : routeLabel(result?.route)

  return <article className={`target-row ${tone} ${mismatch ? 'route-mismatch' : ''}`}>
    <span className="target-name">
      <span className="target-glyph" aria-hidden="true">{target.symbol}</span>
      <span><strong>{target.name}</strong><small>{new URL(target.url).hostname}</small></span>
    </span>

    {/* One encoding, not two: the old eight-segment meter repeated the number
        beside it. The bar is only there to make the numbers comparable. */}
    <span className={`target-delay ${tone}`}>
      <b>{connectivityStatusLabel(result, testing)}</b>
      <span className="delay-bar"><i style={{ width: `${barWidth(result, tone)}%` }} /></span>
    </span>

    <span className="target-expect">预期<b>{expected === 'any' ? '仅观察' : expected === 'direct' ? 'DIRECT' : '代理链路'}</b></span>

    <span className={`target-actual ${mismatch ? 'mismatch' : ''} ${failed ? 'failed' : ''}`}>
      <small>实际链路</small>
      <b>{chain}</b>
      {mismatch && <span className="mismatch-badge">路径不符</span>}
    </span>

    {result && <details className="target-details">
      <summary>查看检测证据</summary>
      <div className="target-evidence">
        <p><span>命中规则</span><strong>{result.rule || '未采集'}{result.rule_payload ? ` · ${result.rule_payload}` : ''}</strong></p>
        <p><span>HTTP 状态</span><strong>{result.http_status || '—'}</strong></p>
        <p><span>检测时间</span><strong>{new Date(result.tested_at).toLocaleTimeString()}</strong></p>
        <div className="sample-list">{result.samples.map((sample, index) => <span key={`${sample.status}-${index}`} title={sample.error}><b>第 {index + 1} 轮</b>{sample.delay_ms ? `${sample.delay_ms} ms` : connectivitySampleLabel(sample.status)}</span>)}</div>
      </div>
    </details>}
  </article>
}

function barWidth(result: ConnectivityResult | undefined, tone: string) {
  if (!result || tone === 'untested') return 0
  if (tone === 'unreachable') return 100
  if (tone === 'testing') return 35
  const delay = result.median_ms ?? 0
  if (!delay) return 6
  return Math.max(6, Math.min(100, Math.round((delay / barCeilingMs) * 100)))
}

function connectivitySampleLabel(status: string) {
  if (status === 'timeout') return '超时'
  if (status === 'dns_error') return 'DNS 失败'
  if (status === 'tls_error') return 'TLS 失败'
  if (status === 'reachable') return '可达'
  return '失败'
}
