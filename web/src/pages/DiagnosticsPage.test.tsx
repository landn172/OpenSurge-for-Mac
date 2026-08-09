// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Diagnostics, Overview } from '../types'

vi.mock('../api', () => ({
  api: {
    diagnostics: vi.fn(),
    refreshProvider: vi.fn(),
  },
}))

import { api } from '../api'
import { DiagnosticsPage } from './DiagnosticsPage'

function diagnostics(overrides: Partial<Diagnostics> = {}): Diagnostics {
  return {
    schema_version: 1,
    revision: 'evidence-revision',
    connections: {
      upload_total: 1024,
      download_total: 2048,
      total: 512,
      connections: [{ id: 'conn-1', upload: 10, download: 20, rule: 'GeoSite', chains: ['DIRECT'], host: 'example.com', port: '443' }],
    },
    logs: { mihomo: ['started'] },
    operations: [],
    recovery: { required: false, stage: 'idle' } as Diagnostics['recovery'],
    ...overrides,
  }
}

const overview = {
  revision: 'overview-revision',
  status: { gateway: 'running', runtime_state: 'active' },
  doctor: [{ name: 'dnsmasq', ok: true }],
  providers: { proxy_providers: [], rule_providers: [] },
  recovery: { required: false, stage: 'idle' },
} as unknown as Overview

function setVisibility(state: 'visible' | 'hidden') {
  Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => state })
  act(() => { document.dispatchEvent(new Event('visibilitychange')) })
}

// The mount effect resolves on a microtask; give React a turn before asserting.
async function settle() {
  await act(async () => { await Promise.resolve() })
}

describe('DiagnosticsPage', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    setVisibility('visible')
    vi.mocked(api.diagnostics).mockResolvedValue(diagnostics())
  })

  afterEach(() => { cleanup(); vi.clearAllMocks(); vi.useRealTimers() })

  it('polls the evidence panel every 2s on the connections tab and slows down elsewhere', async () => {
    render(<DiagnosticsPage overview={overview} />)
    await settle()
    expect(api.diagnostics).toHaveBeenCalledTimes(1)

    await act(async () => { await vi.advanceTimersByTimeAsync(2_000) })
    expect(api.diagnostics).toHaveBeenCalledTimes(2)
    expect(screen.getByText(/每 2 秒自动刷新/)).toBeTruthy()

    fireEvent.click(screen.getByRole('button', { name: '操作记录' }))
    await settle()
    const afterSwitch = vi.mocked(api.diagnostics).mock.calls.length

    // The slower cadence must actually be slower: nothing at 2s, one tick at 5s.
    await act(async () => { await vi.advanceTimersByTimeAsync(2_000) })
    expect(api.diagnostics).toHaveBeenCalledTimes(afterSwitch)
    await act(async () => { await vi.advanceTimersByTimeAsync(3_000) })
    expect(api.diagnostics).toHaveBeenCalledTimes(afterSwitch + 1)
    expect(screen.getByText(/每 5 秒自动刷新/)).toBeTruthy()
  })

  it('pauses while the window is hidden and refills as soon as it returns', async () => {
    render(<DiagnosticsPage overview={overview} />)
    await settle()
    const beforeHide = vi.mocked(api.diagnostics).mock.calls.length

    setVisibility('hidden')
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    expect(api.diagnostics).toHaveBeenCalledTimes(beforeHide)

    setVisibility('visible')
    await settle()
    expect(api.diagnostics).toHaveBeenCalledTimes(beforeHide + 1)
  })

  it('keeps the refresh button idle during background polls but not during a manual one', async () => {
    let releaseManual = () => {}
    vi.mocked(api.diagnostics)
      .mockResolvedValueOnce(diagnostics())
      .mockImplementationOnce(() => new Promise(() => {}))
      .mockImplementationOnce(() => new Promise(resolve => { releaseManual = () => resolve(diagnostics()) }))

    render(<DiagnosticsPage overview={overview} />)
    await settle()

    // Second call is the poll; it hangs, and the button must not react to it.
    await act(async () => { await vi.advanceTimersByTimeAsync(2_000) })
    expect(screen.getByRole('button', { name: '刷新证据' })).toBeTruthy()

    fireEvent.click(screen.getByRole('button', { name: '刷新证据' }))
    await settle()
    expect(screen.getByRole('button', { name: '读取中…' })).toBeTruthy()

    await act(async () => { releaseManual() })
    expect(screen.getByRole('button', { name: '刷新证据' })).toBeTruthy()
  })

  it('reports the real connection count and prefers overview for shared fields', async () => {
    render(<DiagnosticsPage overview={overview} />)
    await settle()

    // 512 connections exist; one row came back in the capped window.
    expect(screen.getByText('512 条')).toBeTruthy()
    expect(screen.getByText(/按累计流量显示前 1 \/ 共 512 条/)).toBeTruthy()
    expect(screen.getByText('example.com:443')).toBeTruthy()
    expect(screen.getByText('overview-revision')).toBeTruthy()
    expect(screen.queryByText('evidence-revision')).toBeNull()
  })
})
