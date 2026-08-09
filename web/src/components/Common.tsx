import { useContext, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { recoveryLabel } from '../recovery'
import { ShellSlots } from './ShellSlots'

export function RecoveryBanner({ recovery, onOpen }: { recovery: string; onOpen: () => void }) {
  return <div className="recovery-banner" role="alert"><span aria-hidden="true">⚠</span><div><strong>网络恢复尚未完成</strong><p>{recoveryLabel(recovery)}。网络已开始变更；请在网络设置中完成状态机，并在路由器 DHCP 恢复已验证前不要把 Mac 切回自动 DHCP。</p></div><button onClick={onOpen}>继续恢复</button></div>
}

/**
 * The eyebrow, title and primary action are rendered into the shell's command
 * bar so the gateway state and the page's one action stay visible while the
 * page scrolls; the description stays in the page as its lead paragraph. Props
 * are unchanged, so every page keeps calling this the same way.
 *
 * The slots are null on the first render — the shell sets them by callback ref
 * — so the portal simply renders one commit later.
 */
export function PageHeader({ eyebrow, title, description, action }: { eyebrow: string; title: string; description: string; action?: ReactNode }) {
  const slots = useContext(ShellSlots)
  if (!slots) {
    return <header className="page-header"><div><small>{eyebrow}</small><h1>{title}</h1><p>{description}</p></div>{action}</header>
  }
  return <>
    {slots.title && createPortal(<><small>{eyebrow}</small><h1>{title}</h1></>, slots.title)}
    {action && slots.action && createPortal(action, slots.action)}
    {description && <p className="page-lead">{description}</p>}
  </>
}

export function SectionTitle({ title, subtitle }: { title: string; subtitle: string }) {
  return <div className="section-title"><h2>{title}</h2><p>{subtitle}</p></div>
}

export function Metric({ label, value, note }: { label: string; value: ReactNode; note: string }) {
  return <article className="metric"><small>{label}</small><strong>{value}</strong><span>{note}</span></article>
}

export function Service({ name, state, detail }: { name: string; state?: string; detail: string }) {
  return <article className="service"><StatusDot status={state ?? 'stopped'} /><div><strong>{name}</strong><small>{detail}</small></div><span>{state ?? '—'}</span></article>
}

export function Mode({ title, description, badge, active, expanded, controls, disabled, onSelect }: { title: string; description: string; badge?: string; active?: boolean; expanded?: boolean; controls?: string; disabled?: boolean; onSelect?: () => void }) {
  return <button type="button" className={`mode ${active ? 'active' : ''}`} aria-pressed={active} aria-expanded={expanded} aria-controls={controls} disabled={disabled} onClick={onSelect}><span>{badge && <span className="pill ok">{badge}</span>}<h3>{title}</h3><p>{description}</p></span><span className="mode-state" aria-hidden="true"><span className="radio">{active ? '●' : '○'}</span><span className="mode-chevron">⌄</span></span></button>
}

export function Empty({ text }: { text: string }) { return <div className="empty">{text}</div> }

export function StatusDot({ status }: { status: string }) {
  const state = status.includes('running') || status === 'ready'
    ? 'running'
    : status.includes('degraded') || status === 'failed' || status === 'unknown'
      ? 'degraded'
      : 'stopped'
  return <span className={`status-dot ${state}`} aria-label={state} />
}
