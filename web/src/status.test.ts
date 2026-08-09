import { describe, expect, it } from 'vitest'
import { statusLabel } from './status'

describe('status labels', () => {
  it('does not confuse an unreachable control service with a stopped gateway', () => {
    expect(statusLabel()).toBe('无法连接')
    expect(statusLabel('stopped')).toBe('已停止')
  })

  it('reports a runtime left by the previous boot as needing cleanup', () => {
    expect(statusLabel('degraded', 'interrupted')).toBe('重启后待清理')
  })
})
