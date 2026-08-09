import { useState } from 'react'
import type { DeviceTraffic, DeviceTrafficRow, TrafficHistoryPoint } from '../types'
import { formatBytes, formatRate } from '../trafficFormat'
import { deviceKey, gatewayLocalDeviceKey } from '../hooks/useDeviceTraffic'
import { Empty, StatusDot } from './Common'
import { TrafficChart } from './TrafficChart'
import { TrafficTrendCard } from './TrafficTrendCard'

type DeviceTrafficPanelProps = {
  gateway?: string
  traffic: DeviceTraffic | null
  history: TrafficHistoryPoint[]
  error: string
}

/**
 * Rows carry their own download sparkline, so the shape of a device's traffic
 * is visible without opening anything; expanding a row reveals the full
 * two-direction trend inline rather than in a side panel, which keeps the
 * detail next to the row it belongs to.
 */
export function DeviceTrafficPanel({ gateway, traffic, history, error }: DeviceTrafficPanelProps) {
  const [openKey, setOpenKey] = useState('')
  const visibleDevices = traffic ? [traffic.gateway_local, ...traffic.devices] : []

  return <section className="section device-section">
    <div className="device-section-head">
      <div><h2>活跃设备</h2><p>实时速度来自相邻连接样本；累计值仅覆盖当前活跃会话</p></div>
    </div>

    {error && !traffic ? <Empty text={`暂时无法读取设备流量：${error}`} /> : <>
      {traffic?.connection_error && <div className="notice warn">{gateway === 'running' || gateway === 'degraded' ? 'mihomo 连接数据暂时不可用；已有设备清单仍会显示。' : '网关未运行；DHCP 租约或已应用静态登记仍会显示，启动后才有活跃连接流量。'}</div>}

      {visibleDevices.length ? <div className="device-list" aria-label="活跃设备流量">
        <div className="device-row device-row-head">
          <span>设备</span><span>IP</span><span className="device-numeric">连接</span><span>↑ 当前</span><span>↓ 当前</span><span>近 60 秒</span><span>主出口</span><span />
        </div>
        {visibleDevices.map(device => {
          const key = trafficRowKey(device)
          const name = deviceName(device)
          const expanded = key === openKey
          const local = device.identity_source === 'gateway_local'
          const online = local ? gatewayActive(gateway) : device.online
          return <div className={expanded ? 'device-entry open' : 'device-entry'} key={key}>
            <button
              className="device-row"
              type="button"
              aria-label={`查看 ${name} ${device.ip} 流量趋势`}
              aria-expanded={expanded}
              onClick={() => setOpenKey(current => current === key ? '' : key)}
            >
              <span className="device-identity"><StatusDot status={online ? 'running' : 'stopped'} /><span><strong>{name}</strong><small>{deviceIdentityDetail(device, gateway)}</small></span></span>
              <span className="device-ip"><code>{device.ip || '—'}</code></span>
              <span className="device-numeric">{device.active_connections}</span>
              <RateCell rate={device.upload_rate} total={device.upload} />
              <RateCell rate={device.download_rate} total={device.download} />
              <TrafficChart history={history} deviceKey={key} label={`${name}近 60 秒下载趋势`} spark className="device-spark" />
              <span className="device-egress" title={device.primary_egress}><strong>{compactEgress(device.primary_egress)}</strong><small>{device.primary_egress || '暂无出口'}</small></span>
              <span className="device-caret" aria-hidden="true">⌄</span>
            </button>
            {expanded && <div className="device-detail">
              <TrafficTrendCard
                title={`${name} 流量趋势`}
                subtitle={`${device.ip} · ${device.primary_egress || '暂无出口信息'}`}
                history={history}
                deviceKey={key}
                className="device-trend-card"
              />
              <dl className="device-detail-facts">
                <div><dt>身份来源</dt><dd>{identitySourceLabel(device)}</dd></div>
                <div><dt>MAC</dt><dd className="mono">{device.mac || 'MAC 待识别'}</dd></div>
                <div><dt>累计上传</dt><dd>{formatBytes(device.upload)}</dd></div>
                <div><dt>累计下载</dt><dd>{formatBytes(device.download)}</dd></div>
              </dl>
            </div>}
          </div>
        })}
      </div> : <Empty text={traffic ? '暂无 DHCP、静态登记或当前流量观察到的 LAN 设备' : '正在读取设备流量…'} />}

      {traffic && <div className="device-summary">
        <strong>合计 {traffic.totals.devices} 台设备接入 · {traffic.totals.active_connections} 个连接 · ↑ {formatRate(traffic.totals.upload_rate)} · ↓ {formatRate(traffic.totals.download_rate)}</strong>
        {traffic.unidentified_device_connections > 0 && <small>其中 {traffic.unidentified_device_connections} 个待识别设备连接，仅确认了当前 LAN 源 IP。</small>}
        {traffic.unclassified_connections > 0 && <small>另有 {traffic.unclassified_connections} 个连接无法判断来源，请在诊断中查看。</small>}
      </div>}
      {error && traffic && <small className="device-refresh-error">刷新失败：{error}</small>}
    </>}
  </section>
}

function RateCell({ rate = 0, total = 0 }: { rate?: number; total?: number }) {
  return <span className="device-rate"><strong>{formatRate(rate)}</strong><small>累计 {formatBytes(total)}</small></span>
}

function identitySourceLabel(device: DeviceTrafficRow) {
  switch (device.identity_source) {
  case 'gateway_local': return '网关本机'
  case 'dhcp_lease': return 'DHCP 已验证'
  case 'registered_static': return '静态登记'
  case 'observed_traffic': return '流量已观察'
  default: return '身份来源未标记'
  }
}

function deviceName(device: DeviceTrafficRow) {
  if (device.identity_source === 'gateway_local') return '本机 Mac'
  if (device.name) return device.name
  if (device.hostname) return device.hostname
  if (!device.mac) return `当前设备 ${device.ip}`
  const parts = device.mac.toLowerCase().split(':')
  return `未知设备 ${parts.length > 3 ? `${parts.slice(0, 3).join(':')}:…` : device.mac.toLowerCase()}`
}

function deviceIdentityDetail(device: DeviceTrafficRow, gateway?: string) {
  if (device.identity_source === 'gateway_local') return gatewayLocalDetail(device, gateway)
  const source = identitySourceLabel(device)
  return device.mac ? `${source} · ${device.mac}` : `${source} · MAC 待识别`
}

function gatewayLocalDetail(device: DeviceTrafficRow, gateway?: string) {
  if (!gatewayActive(gateway)) return '网关本机 · 网关未运行'
  switch (device.transport) {
  case 'tun':
    return '网关本机 · TUN'
  case 'explicit_proxy':
    return '网关本机 · 显式代理'
  case 'tun_and_explicit_proxy':
    return '网关本机 · TUN / 显式代理'
  case 'other':
    return '网关本机 · 本机流量'
  default:
    return '网关本机 · 暂无活跃连接'
  }
}

function gatewayActive(gateway?: string) {
  return gateway?.startsWith('running') === true || gateway === 'degraded'
}

function trafficRowKey(device: DeviceTrafficRow) {
  return device.identity_source === 'gateway_local' ? gatewayLocalDeviceKey : deviceKey(device.mac, device.ip)
}

function compactEgress(egress?: string) {
  if (!egress) return '—'
  const parts = egress.split(' → ').map(part => part.trim()).filter(Boolean)
  return parts.at(-1) ?? egress
}
