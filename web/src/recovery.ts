// The same-LAN DHCP takeover stage vocabulary. Everything the GUI needs to know
// about a recovery stage -- its name, the step it offers next, whether start-time
// plan blockers still apply, and whether it warrants the cross-page warning --
// lives in one table here rather than spread across the network page.
//
// This mirrors internal/controlapi/recovery_flow.go, which is the authority.
// Nothing keeps the two tables in step automatically; when a stage changes,
// both are edited by hand.

export type RecoveryAction =
  | 'prepare'
  | 'apply_static'
  | 'probe_dhcp'
  | 'start_gateway'
  | 'validate_client'
  | 'stop_gateway'
  | 'confirm_router_restored'
  | 'restore_mac_dhcp'

type StageRule = {
  label: string
  /** The step the primary button performs at this stage. */
  action?: RecoveryAction
  actionLabel?: string
  /**
   * Whether this stage warrants the cross-page warning. Reserved for stages
   * where the network was already changed but the gateway is not the intended
   * steady state -- interrupted setup, and the post-stop path that needs
   * operator action.
   */
  warns?: boolean
  /** Whether start-time takeover plan blockers still constrain this stage. */
  planBlockers?: boolean
}

const PREPARE_LABEL = '保存网络快照与离线恢复卡'
const STOP_LABEL = '停止 OpenSurge'

const STAGES: Record<string, StageRule> = {
  idle: { label: '尚未开始', action: 'prepare', actionLabel: PREPARE_LABEL, planBlockers: true },
  prepared: { label: '恢复资料已准备', action: 'apply_static', actionLabel: '将 Mac 切换为固定 IPv4', planBlockers: true },
  mac_static: { label: 'Mac 已使用固定 IPv4', action: 'probe_dhcp', actionLabel: '已关闭路由器 DHCP，执行 OFFER 探测', warns: true, planBlockers: true },
  router_dhcp_disabled_confirmed: { label: '路由器 DHCP 已关闭', action: 'start_gateway', actionLabel: '启动 OpenSurge', warns: true, planBlockers: true },
  gateway_active: { label: 'OpenSurge 已接管', action: 'validate_client', actionLabel: '验证客户端 DHCP、DNS 与 TUN 证据' },
  client_validated: { label: '客户端 DHCP、DNS 与 TUN 已验收', action: 'stop_gateway', actionLabel: STOP_LABEL },
  client_validation_skipped: { label: '客户端验收已跳过', action: 'stop_gateway', actionLabel: STOP_LABEL },
  gateway_stopped_waiting_router_dhcp: { label: '已停止，等待恢复路由器 DHCP', action: 'confirm_router_restored', actionLabel: '路由器 DHCP 已恢复，执行 OFFER 探测', warns: true },
  router_dhcp_restored: { label: '路由器 DHCP 已恢复', action: 'restore_mac_dhcp', actionLabel: '将 Mac 恢复为自动 DHCP', warns: true },
  complete: { label: 'Mac 与客户端已恢复自动获取', action: 'prepare', actionLabel: PREPARE_LABEL, planBlockers: true },
  complete_static: { label: '流程已结束，Mac 保持静态 IPv4', action: 'prepare', actionLabel: PREPARE_LABEL, planBlockers: true },
}

export function recoveryLabel(stage: string) {
  return STAGES[stage]?.label ?? stage
}

/**
 * A running takeover still has a recovery plan, but it is the intended steady
 * state rather than an unfinished restoration. An unknown stage warns: a stage
 * this build does not recognise is not one it can call safe.
 */
export function needsNetworkRecoveryWarning(stage: string) {
  const rule = STAGES[stage]
  return rule ? rule.warns === true : true
}

/**
 * Start-time plan blockers only constrain the stages before the gateway runs.
 * They must never disable the post-stop recovery actions.
 */
export function planBlockersApply(stage: string) {
  return STAGES[stage]?.planBlockers === true
}

export function recoveryNextAction(stage: string): RecoveryAction | null {
  return STAGES[stage]?.action ?? null
}

export function recoveryActionLabel(stage: string) {
  return STAGES[stage]?.actionLabel ?? recoveryLabel(stage)
}

/**
 * The ordered stages shown in the timeline. Which client checkpoint and which
 * completion appear depends on the choices the operator already made.
 */
export function recoveryTimeline(clientValidationSkipped: boolean, stage: string) {
  return [
    'prepared',
    'mac_static',
    'router_dhcp_disabled_confirmed',
    'gateway_active',
    clientValidationSkipped ? 'client_validation_skipped' : 'client_validated',
    'gateway_stopped_waiting_router_dhcp',
    'router_dhcp_restored',
    stage === 'complete_static' ? 'complete_static' : 'complete',
  ]
}
