import { recoveryLabel } from '../recovery'
import type { Overview } from '../types'
import { StatusDot } from './Common'

/**
 * The four facts an operator checks before touching anything, then the five
 * services as one readiness track. The old layout put the services in a
 * five-across strip where a single stopped service looked the same as four,
 * so state now carries a colour and a word, not only position.
 */
export function GatewayStatusCard({ overview }: { overview: Overview | null }) {
  const status = overview?.status
  const running = status?.gateway === 'running'
  const configState = overview?.drift ? running ? '待重载' : '下次启动应用' : '已同步'
  const services = [
    { label: status?.dhcp_enabled === false ? 'DNS' : 'DHCP / DNS', state: status?.dhcp },
    { label: 'mihomo', state: status?.mihomo },
    { label: status?.tun_interface ? `TUN · ${status.tun_interface}` : 'TUN', state: status?.tun },
    { label: 'PF Anchor', state: status?.pf_anchor },
    { label: 'IPv4 转发', state: status?.forwarding },
  ]
  const ready = services.filter(service => isReady(service.state)).length

  return <article className="gateway-status" aria-label="网关状态">
    {/* The running/stopped word belongs to the sticky command bar and is on
        screen at all times, so repeating it here would only make the same
        state look like two separate readings. */}
    <header className="gateway-status-head">
      <div>
        <small>GATEWAY</small>
        <h2>网关状态</h2>
      </div>
      <p className="gateway-status-where">{status?.interface ?? '—'} · {status?.lan_ip ?? '等待状态'}</p>
    </header>

    <dl className="gateway-facts">
      <div><dt>接管模式</dt><dd>{topologyLabel(overview?.topology)}</dd></div>
      <div className={overview?.drift ? 'attention' : ''}><dt>配置状态</dt><dd>{configState}</dd></div>
      <div><dt>恢复阶段</dt><dd>{recoveryLabel(overview?.recovery.stage ?? 'idle')}</dd></div>
      <div><dt>配置版本</dt><dd className="mono">{overview?.revision?.slice(0, 10) ?? '—'}</dd></div>
    </dl>

    <div className="readiness">
      <div className="readiness-head">
        <h3>核心服务</h3>
        <span>{overview ? `${ready} / ${services.length} 已就绪` : '正在读取状态'}</span>
      </div>
      <div className="readiness-track">
        {services.map(service => <span className="readiness-step" key={service.label}>
          <StatusDot status={service.state ?? 'stopped'} />
          <strong>{service.label}</strong>
          <small>{service.state ?? '—'}</small>
        </span>)}
      </div>
    </div>
  </article>
}

function isReady(state?: string) {
  return state?.startsWith('running') === true || state === 'ready' || state === 'loaded' || state === 'enabled'
}

function topologyLabel(topology?: string) {
  if (topology === 'same_wifi_dhcp') return '局域网 DHCP 接管'
  if (topology === 'same_lan') return '旁路由模式'
  if (topology === 'isolated_lan') return '独立下游 LAN'
  return 'IPv4 网关'
}
