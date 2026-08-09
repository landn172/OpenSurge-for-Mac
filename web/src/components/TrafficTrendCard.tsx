import type { TrafficHistoryPoint, TrafficRates } from '../types'
import { formatRate } from '../trafficFormat'
import { chartWindowLabels, TrafficChart } from './TrafficChart'

type TrafficTrendCardProps = {
  title: string
  subtitle: string
  history: TrafficHistoryPoint[]
  deviceKey?: string
  className?: string
}

const zeroRates: TrafficRates = { upload: 0, download: 0 }

export function TrafficTrendCard({ title, subtitle, history, deviceKey, className = '' }: TrafficTrendCardProps) {
  const current = (deviceKey ? history.at(-1)?.devices[deviceKey] : history.at(-1)) ?? zeroRates
  const window = chartWindowLabels(history)

  return <article className={`traffic-trend-card ${className}`.trim()}>
    <header className="trend-card-header"><div><small>LIVE TRAFFIC</small><h2>{title}</h2><p>{subtitle}</p></div><div className="trend-live-dot"><span />实时</div></header>
    <div className="trend-current">
      <span className="upload"><i />↑ {formatRate(current.upload)}</span>
      <span className="download"><i />↓ {formatRate(current.download)}</span>
    </div>
    <TrafficChart history={history} deviceKey={deviceKey} label={`${title}最近 60 秒上传下载趋势`} />
    <div className="trend-axis"><span>{window.start}</span><span>近 60 秒</span><span>{window.end}</span></div>
  </article>
}
