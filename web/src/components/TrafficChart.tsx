import { useId } from 'react'
import type { TrafficHistoryPoint, TrafficRates } from '../types'
import { buildSmoothChart } from '../trafficChart'
import { useAnimatedTrafficSeries } from '../hooks/useAnimatedTrafficSeries'

type TrafficChartProps = {
  history: TrafficHistoryPoint[]
  /** Restrict the series to one device instead of the whole gateway. */
  deviceKey?: string
  label: string
  /** Download-only, no axis and no fills below the line: for a table row. */
  spark?: boolean
  className?: string
}

const zeroRates: TrafficRates = { upload: 0, download: 0 }

/**
 * The upload and download curves share one vertical scale so the two lines can
 * be compared by eye; both are filled, because an outline alone reads as a
 * decoration rather than as data.
 */
export function TrafficChart({ history, deviceKey, label, spark = false, className = '' }: TrafficChartProps) {
  const gradientID = useId().replace(/:/g, '')
  const target = history.map(point => deviceKey ? point.devices[deviceKey] ?? zeroRates : point)
  const samples = useAnimatedTrafficSeries(target, `${deviceKey ?? 'gateway'}:${history.at(-1)?.sampled_at ?? 'empty'}`)
  const upload = samples.map(point => point.upload)
  const download = samples.map(point => point.download)
  const maximum = Math.max(...upload, ...download, 1)
  const uploadChart = buildSmoothChart(upload, maximum, 8, 46)
  const downloadChart = buildSmoothChart(download, maximum, 8, 46)

  return <div className={`trend-chart ${className}`.trim()}>
    <svg viewBox="0 0 100 52" preserveAspectRatio="none" role="img" aria-label={label}>
      <defs>
        <linearGradient id={`${gradientID}-up`} x1="0" y1="0" x2="0" y2="1"><stop offset="0" stopColor="var(--up)" stopOpacity=".3" /><stop offset="1" stopColor="var(--up)" stopOpacity="0" /></linearGradient>
        <linearGradient id={`${gradientID}-down`} x1="0" y1="0" x2="0" y2="1"><stop offset="0" stopColor="var(--down)" stopOpacity=".28" /><stop offset="1" stopColor="var(--down)" stopOpacity="0" /></linearGradient>
      </defs>
      {!spark && <line x1="0" y1="27" x2="100" y2="27" className="chart-grid-line" />}
      <path d={downloadChart.areaPath} fill={`url(#${gradientID}-down)`} />
      {!spark && <path d={uploadChart.areaPath} fill={`url(#${gradientID}-up)`} />}
      <path d={downloadChart.linePath} className="trend-line download" />
      {!spark && <path d={uploadChart.linePath} className="trend-line upload" />}
    </svg>
  </div>
}

export function chartWindowLabels(history: TrafficHistoryPoint[]) {
  const first = history[0]?.sampled_at
  const last = history.at(-1)?.sampled_at
  return { start: first ? formatSampleTime(first) : '等待采样', end: last ? formatSampleTime(last) : '现在' }
}

function formatSampleTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}
