// @vitest-environment jsdom
import { cleanup, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { TrafficHistoryPoint } from '../types'
import { TrafficTrendCard } from './TrafficTrendCard'

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

describe('traffic card motion', () => {
  it('updates total-trend numbers immediately while its curves remain animated', () => {
    disableAnimationFrames()
    const initial = [point('2026-07-24T00:00:00Z', 1_000, 2_000)]
    const { rerender } = render(<TrafficTrendCard title="流量趋势" subtitle="测试" history={initial} />)
    const initialPath = document.querySelector('path.trend-line.upload')?.getAttribute('d')

    rerender(<TrafficTrendCard
      title="流量趋势"
      subtitle="测试"
      history={[...initial, point('2026-07-24T00:00:02Z', 9_000, 8_000)]}
    />)

    expect(screen.getByText('↑ 9 kB/s')).toBeTruthy()
    expect(screen.getByText('↓ 8 kB/s')).toBeTruthy()
    expect(document.querySelector('path.trend-line.upload')?.getAttribute('d')).toBe(initialPath)
  })
})

function disableAnimationFrames() {
  vi.stubGlobal('requestAnimationFrame', vi.fn(() => 1))
  vi.stubGlobal('cancelAnimationFrame', vi.fn())
  vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: false })))
}

function point(sampled_at: string, upload: number, download: number): TrafficHistoryPoint {
  return { sampled_at, upload, download, devices: {} }
}
