import type { DeviceTraffic, TrafficHistoryPoint, TrafficRates } from '../types'
import { formatRate } from '../trafficFormat'
import { chartWindowLabels, TrafficChart } from './TrafficChart'

/**
 * One reading of what the gateway is doing right now: both directions, how the
 * active connections break down, and the last sixty seconds as a single shared
 * chart. Replaces the four separate tiles, which split one story across four
 * boxes and made download and upload impossible to compare.
 */
export function NowPanel({ rates, traffic, history }: { rates: TrafficRates; traffic: DeviceTraffic | null; history: TrafficHistoryPoint[] }) {
  const local = traffic?.gateway_local.active_connections ?? 0
  const unidentified = traffic?.unidentified_device_connections ?? 0
  const attributed = Math.max(0, (traffic?.totals.active_connections ?? 0) - unidentified)
  const unclassified = traffic?.unclassified_connections ?? 0
  const total = local + attributed + unidentified + unclassified
  const window = chartWindowLabels(history)
  const download = splitRate(rates.download)
  const upload = splitRate(rates.upload)

  return <section className="now-panel" aria-label="网关实时流量">
    <div className="now-head">
      <div>
        <small>RIGHT NOW</small>
        <h2>现在的流量</h2>
        <p>网关全部 mihomo 活跃连接 · 近 60 秒内存采样</p>
      </div>
      <span className="live-pill"><i />LIVE</span>
    </div>

    <div className="now-readings">
      <div className="now-reading download">
        <span className="now-reading-label"><i aria-hidden="true">↓</i>下载</span>
        <span className="now-reading-value"><strong>{download.amount}</strong><span>{download.unit}</span></span>
      </div>
      <div className="now-reading upload">
        <span className="now-reading-label"><i aria-hidden="true">↑</i>上传</span>
        <span className="now-reading-value"><strong>{upload.amount}</strong><span>{upload.unit}</span></span>
      </div>
      <dl className="now-connections">
        <div><dt>活跃连接</dt><dd className="now-total">{total}</dd></div>
        <div><dt>本机连接</dt><dd>{local}</dd></div>
        <div><dt>已归属设备连接</dt><dd>{attributed}</dd></div>
        <div className={unidentified ? 'attention' : ''}><dt>待识别设备连接</dt><dd>{unidentified}</dd></div>
      </dl>
    </div>

    <TrafficChart history={history} label="网关最近 60 秒上传下载趋势" className="now-chart" />
    <div className="trend-axis now-axis"><span>{window.start}</span><span>近 60 秒</span><span>{window.end}</span></div>
  </section>
}

function splitRate(value: number) {
  const [amount, unit = 'B/s'] = formatRate(value).split(' ')
  return { amount, unit }
}
