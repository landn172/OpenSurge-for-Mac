import { describe, expect, it } from 'vitest'
import {
  needsNetworkRecoveryWarning,
  planBlockersApply,
  recoveryActionLabel,
  recoveryLabel,
  recoveryNextAction,
  recoveryTimeline,
} from './recovery'

const ALL_STAGES = [
  'idle', 'prepared', 'mac_static', 'router_dhcp_disabled_confirmed',
  'gateway_active', 'client_validated', 'client_validation_skipped',
  'gateway_stopped_waiting_router_dhcp', 'router_dhcp_restored',
  'complete', 'complete_static',
]

describe('recovery stage vocabulary', () => {
  it('names every stage', () => {
    for (const stage of ALL_STAGES) {
      expect(recoveryLabel(stage), stage).not.toBe(stage)
    }
  })

  it('keeps the dangerous stopped recovery stage explicit', () => {
    expect(recoveryLabel('gateway_stopped_waiting_router_dhcp')).toContain('等待恢复路由器 DHCP')
  })

  it('falls back to the raw stage when a build does not know it', () => {
    expect(recoveryLabel('stage_from_a_newer_build')).toBe('stage_from_a_newer_build')
  })
})

describe('cross-page recovery warning', () => {
  it('distinguishes a saved recovery card from changed network state', () => {
    expect(needsNetworkRecoveryWarning('prepared')).toBe(false)
    expect(needsNetworkRecoveryWarning('mac_static')).toBe(true)
  })

  it('treats active takeover as steady state and post-stop as recovery', () => {
    expect(needsNetworkRecoveryWarning('gateway_active')).toBe(false)
    expect(needsNetworkRecoveryWarning('client_validated')).toBe(false)
    expect(needsNetworkRecoveryWarning('client_validation_skipped')).toBe(false)
    expect(needsNetworkRecoveryWarning('gateway_stopped_waiting_router_dhcp')).toBe(true)
    expect(needsNetworkRecoveryWarning('router_dhcp_restored')).toBe(true)
    expect(needsNetworkRecoveryWarning('complete_static')).toBe(false)
  })

  it('warns for exactly the stages that changed the network without a running takeover', () => {
    const warning = ALL_STAGES.filter(needsNetworkRecoveryWarning)
    expect(warning).toEqual([
      'mac_static',
      'router_dhcp_disabled_confirmed',
      'gateway_stopped_waiting_router_dhcp',
      'router_dhcp_restored',
    ])
  })

  it('warns for an unrecognised stage rather than calling it safe', () => {
    expect(needsNetworkRecoveryWarning('stage_from_a_newer_build')).toBe(true)
  })
})

describe('takeover plan blockers', () => {
  it('constrains only the stages before the gateway runs', () => {
    expect(ALL_STAGES.filter(planBlockersApply)).toEqual([
      'idle',
      'prepared',
      'mac_static',
      'router_dhcp_disabled_confirmed',
      'complete',
      'complete_static',
    ])
  })

  it('never locks the post-stop recovery actions', () => {
    expect(planBlockersApply('gateway_stopped_waiting_router_dhcp')).toBe(false)
    expect(planBlockersApply('router_dhcp_restored')).toBe(false)
  })
})

describe('the next step a stage offers', () => {
  it('maps every stage to exactly one action', () => {
    const actions = Object.fromEntries(ALL_STAGES.map(stage => [stage, recoveryNextAction(stage)]))
    expect(actions).toEqual({
      idle: 'prepare',
      prepared: 'apply_static',
      mac_static: 'probe_dhcp',
      router_dhcp_disabled_confirmed: 'start_gateway',
      gateway_active: 'validate_client',
      client_validated: 'stop_gateway',
      client_validation_skipped: 'stop_gateway',
      gateway_stopped_waiting_router_dhcp: 'confirm_router_restored',
      router_dhcp_restored: 'restore_mac_dhcp',
      complete: 'prepare',
      complete_static: 'prepare',
    })
  })

  it('offers no action for an unrecognised stage', () => {
    expect(recoveryNextAction('stage_from_a_newer_build')).toBeNull()
  })

  it('labels every action', () => {
    for (const stage of ALL_STAGES) {
      expect(recoveryActionLabel(stage), stage).not.toBe(recoveryLabel(stage))
    }
  })
})

describe('the timeline', () => {
  it('shows the checkpoint the operator actually reached', () => {
    expect(recoveryTimeline(false, 'gateway_active')).toContain('client_validated')
    expect(recoveryTimeline(false, 'gateway_active')).not.toContain('client_validation_skipped')
    expect(recoveryTimeline(true, 'gateway_active')).toContain('client_validation_skipped')
    expect(recoveryTimeline(true, 'gateway_active')).not.toContain('client_validated')
  })

  it('ends on the completion the flow actually took', () => {
    expect(recoveryTimeline(false, 'complete_static')).toContain('complete_static')
    expect(recoveryTimeline(false, 'complete')).toContain('complete')
    expect(recoveryTimeline(false, 'mac_static')).toContain('complete')
  })

  it('places the current stage in order', () => {
    const stages = recoveryTimeline(false, 'mac_static')
    expect(stages.indexOf('mac_static')).toBe(1)
    expect(stages.indexOf('gateway_active')).toBeGreaterThan(stages.indexOf('router_dhcp_disabled_confirmed'))
  })

  it('does not show idle as a step', () => {
    expect(recoveryTimeline(false, 'idle')).not.toContain('idle')
  })
})
