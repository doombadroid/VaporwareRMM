'use client'

import type { ReactNode } from 'react'

// Status semantics map to a small fixed palette so a glance teaches
// the reader the meaning without reading the label. DESIGN.md:
// emerald=online, slate-500=offline, amber=warn, red=critical, plus
// blue for informational neutral.
type Tone = 'online' | 'offline' | 'warn' | 'danger' | 'info' | 'muted' | 'success'

const dotClass: Record<Tone, string> = {
  online: 'bg-emerald-400',
  success: 'bg-emerald-400',
  offline: 'bg-slate-500',
  warn: 'bg-amber-400',
  danger: 'bg-rose-400',
  info: 'bg-blue-400',
  muted: 'bg-white/30',
}

const pillClass: Record<Tone, string> = {
  online: 'text-emerald-300 bg-emerald-500/10 border-emerald-500/20',
  success: 'text-emerald-300 bg-emerald-500/10 border-emerald-500/20',
  offline: 'text-slate-400 bg-slate-500/10 border-slate-500/20',
  warn: 'text-amber-300 bg-amber-500/10 border-amber-500/25',
  danger: 'text-rose-300 bg-rose-500/10 border-rose-500/25',
  info: 'text-blue-300 bg-blue-500/10 border-blue-500/20',
  muted: 'text-white/55 bg-white/[0.04] border-white/[0.08]',
}

export function StatusDot({ tone, pulse = false }: { tone: Tone; pulse?: boolean }) {
  return (
    <span
      className={`inline-block w-1.5 h-1.5 rounded-full shrink-0 ${dotClass[tone]} ${pulse ? 'animate-pulse' : ''}`}
      aria-hidden="true"
    />
  )
}

export function Pill({
  tone = 'muted',
  children,
  className,
}: {
  tone?: Tone
  children: ReactNode
  className?: string
}) {
  return (
    <span
      className={`inline-flex items-center gap-1.5 px-1.5 py-0.5 rounded border text-[10.5px] font-medium uppercase tracking-[0.06em] ${pillClass[tone]} ${className ?? ''}`}
    >
      {children}
    </span>
  )
}

// Severity → tone. Every severity-emitting handler in the codebase
// uses one of these values; map centrally so a status pill anywhere
// looks identical.
export function severityTone(severity?: string): Tone {
  switch ((severity || '').toLowerCase()) {
    case 'critical':
    case 'danger':
      return 'danger'
    case 'high':
    case 'warning':
    case 'warn':
      return 'warn'
    case 'medium':
    case 'info':
      return 'info'
    case 'low':
      return 'muted'
    default:
      return 'muted'
  }
}

export function statusTone(status?: string): Tone {
  switch ((status || '').toLowerCase()) {
    case 'online':
    case 'connected':
    case 'active':
    case 'resolved':
    case 'closed':
    case 'installed':
    case 'completed':
    case 'pass':
    case 'ok':
      return 'online'
    case 'offline':
    case 'disconnected':
    case 'disabled':
    case 'failed':
    case 'expired':
    case 'critical':
    case 'fail':
      return 'danger'
    case 'warn':
    case 'warning':
    case 'pending':
    case 'in_progress':
    case 'installing':
    case 'running':
    case 'suspended':
      return 'warn'
    case 'open':
    case 'info':
      return 'info'
    default:
      return 'muted'
  }
}

// Inline mono code block for IDs/IPs/secrets. select-all so triple
// click selects exactly the value.
export function Code({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <code
      className={`font-mono text-[11.5px] bg-white/[0.04] text-white/85 px-1.5 py-0.5 rounded select-all ${className ?? ''}`}
    >
      {children}
    </code>
  )
}
