'use client'

import { useEffect, useMemo, useRef, useState } from 'react'
import { useRouter } from 'next/navigation'
import {
  Search,
  ArrowRight,
  Server,
  Ticket,
  AlertTriangle,
  Package,
  Settings,
  ScrollText,
  Webhook,
  BellRing,
  ShieldCheck,
  Boxes,
  Users,
  CalendarClock,
  UserCircle,
  ShieldEllipsis,
  Activity,
  Wifi,
  KeyRound,
  DollarSign,
  FileSpreadsheet,
  Building2,
  Sparkles,
  BarChart3,
  Globe,
  Terminal,
  ScrollText as PolicyIcon,
} from 'lucide-react'
import { devices as devicesApi, ticketsApi, type Device, type Ticket as TicketT } from '@/lib/api'

interface CommandPaletteProps {
  open: boolean
  onClose: () => void
}

interface PaletteAction {
  id: string
  label: string
  hint?: string
  href: string
  icon: typeof Search
  group: 'Navigate' | 'Devices' | 'Tickets' | 'Quick'
}

const navActions: PaletteAction[] = [
  { id: 'nav-dashboard', label: 'Dashboard', href: '/', icon: BarChart3, group: 'Navigate' },
  { id: 'nav-devices', label: 'Devices', href: '/agents', icon: Server, group: 'Navigate' },
  { id: 'nav-tickets', label: 'Tickets', href: '/tickets', icon: Ticket, group: 'Navigate' },
  { id: 'nav-alerts', label: 'Alerts', href: '/alerts', icon: AlertTriangle, group: 'Navigate' },
  { id: 'nav-patches', label: 'Patches', href: '/patches', icon: Package, group: 'Navigate' },
  { id: 'nav-network', label: 'Network map', href: '/network', icon: Globe, group: 'Navigate' },
  { id: 'nav-maintenance', label: 'Maintenance windows', href: '/admin/maintenance', icon: CalendarClock, group: 'Navigate' },
  { id: 'nav-software', label: 'Software inventory', href: '/admin/software', icon: Boxes, group: 'Navigate' },
  { id: 'nav-groups', label: 'Device groups', href: '/admin/groups', icon: Users, group: 'Navigate' },
  { id: 'nav-customers', label: 'Customer portal users', href: '/admin/customers', icon: UserCircle, group: 'Navigate' },
  { id: 'nav-neighbors', label: 'Network neighbors', href: '/admin/neighbors', icon: Wifi, group: 'Navigate' },
  { id: 'nav-cert', label: 'Cert monitors', href: '/admin/cert-monitors', icon: ShieldEllipsis, group: 'Navigate' },
  { id: 'nav-snmp', label: 'SNMP targets', href: '/admin/snmp', icon: Activity, group: 'Navigate' },
  { id: 'nav-rules', label: 'Alert rules', href: '/admin/alert-rules', icon: BellRing, group: 'Navigate' },
  { id: 'nav-webhooks', label: 'Webhooks', href: '/admin/webhooks', icon: Webhook, group: 'Navigate' },
  { id: 'nav-reports', label: 'Scheduled reports', href: '/admin/reports', icon: FileSpreadsheet, group: 'Navigate' },
  { id: 'nav-ai', label: 'AI control', href: '/admin/ai', icon: Sparkles, group: 'Navigate' },
  { id: 'nav-aicost', label: 'AI cost', href: '/admin/ai/cost', icon: DollarSign, group: 'Navigate' },
  { id: 'nav-audit', label: 'Audit log', href: '/admin/audit', icon: ScrollText, group: 'Navigate' },
  { id: 'nav-compliance', label: 'Compliance', href: '/admin/compliance', icon: ShieldCheck, group: 'Navigate' },
  { id: 'nav-sso', label: 'SSO config', href: '/admin/sso', icon: KeyRound, group: 'Navigate' },
  { id: 'nav-policies', label: 'Tenant policies', href: '/admin/policies', icon: PolicyIcon, group: 'Navigate' },
  { id: 'nav-tenants', label: 'Tenants', href: '/admin/tenants', icon: Building2, group: 'Navigate' },
  { id: 'nav-logs', label: 'Server logs', href: '/admin/logs', icon: Terminal, group: 'Navigate' },
  { id: 'nav-settings', label: 'Settings', href: '/settings', icon: Settings, group: 'Navigate' },
]

// Lightweight fuzzy match: every char of the query has to appear in
// order somewhere in the haystack. Score by how tight the match is so
// "agt" ranks "agents" above "manage tickets". Fast enough at the
// hundreds-of-items scale we'd ever have here.
function fuzzy(query: string, hay: string): number | null {
  if (!query) return 0
  const q = query.toLowerCase()
  const h = hay.toLowerCase()
  let qi = 0
  let lastIdx = -1
  let score = 0
  for (let hi = 0; hi < h.length && qi < q.length; hi++) {
    if (h[hi] === q[qi]) {
      score += hi - lastIdx
      lastIdx = hi
      qi++
    }
  }
  if (qi !== q.length) return null
  // Lower is better. Also reward matches starting at a word boundary.
  if (h.startsWith(q)) score -= 100
  return score
}

export default function CommandPalette({ open, onClose }: CommandPaletteProps) {
  const router = useRouter()
  const [query, setQuery] = useState('')
  const [highlighted, setHighlighted] = useState(0)
  const inputRef = useRef<HTMLInputElement | null>(null)
  const [devices, setDevices] = useState<Device[]>([])
  const [tickets, setTickets] = useState<TicketT[]>([])

  // Lazy-load devices + tickets when palette first opens. We don't keep
  // them subscribed live — the palette is a navigation tool, not a
  // real-time list.
  useEffect(() => {
    if (!open) return
    setQuery('')
    setHighlighted(0)
    // Defer focus to after mount so the Escape-to-close handler in the
    // outer keydown effect doesn't race with the input ref.
    requestAnimationFrame(() => inputRef.current?.focus())
    devicesApi.getAll().then(setDevices).catch(() => {})
    ticketsApi.list().then(setTickets).catch(() => {})
  }, [open])

  // Keyboard navigation while palette is open.
  useEffect(() => {
    if (!open) return
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        onClose()
      } else if (e.key === 'ArrowDown') {
        e.preventDefault()
        setHighlighted((s) => s + 1)
      } else if (e.key === 'ArrowUp') {
        e.preventDefault()
        setHighlighted((s) => Math.max(0, s - 1))
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [open, onClose])

  const results = useMemo<PaletteAction[]>(() => {
    const out: { score: number; item: PaletteAction }[] = []
    const push = (item: PaletteAction) => {
      const text = `${item.label} ${item.hint || ''}`
      const s = fuzzy(query, text)
      if (s !== null) out.push({ score: s, item })
    }
    navActions.forEach(push)
    devices.slice(0, 100).forEach((d) =>
      push({
        id: 'dev-' + d.id,
        label: d.hostname || d.id.slice(0, 8),
        hint: `${d.os_name || ''} · ${d.status}`,
        href: `/devices/${d.id}`,
        icon: Server,
        group: 'Devices',
      }),
    )
    tickets.slice(0, 100).forEach((t) =>
      push({
        id: 'tk-' + t.id,
        label: t.title,
        hint: `${t.status} · ${t.priority}`,
        href: `/tickets/${t.id}`,
        icon: Ticket,
        group: 'Tickets',
      }),
    )
    out.sort((a, b) => a.score - b.score)
    return out.slice(0, 30).map((x) => x.item)
  }, [query, devices, tickets])

  const grouped = useMemo(() => {
    const groups = new Map<PaletteAction['group'], PaletteAction[]>()
    for (const r of results) {
      const arr = groups.get(r.group) ?? []
      arr.push(r)
      groups.set(r.group, arr)
    }
    return Array.from(groups.entries())
  }, [results])

  // Clamp highlight if filter shrinks list.
  const safeHighlight = Math.min(highlighted, Math.max(0, results.length - 1))

  const go = (item: PaletteAction) => {
    onClose()
    router.push(item.href)
  }

  if (!open) return null

  return (
    <div
      className="fixed inset-0 z-[60] flex items-start justify-center pt-[14vh] px-4 bg-black/60 backdrop-blur-sm"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose()
      }}
    >
      <div className="w-full max-w-xl bg-[#0b0b12] border border-white/[0.08] rounded-xl shadow-2xl overflow-hidden">
        <div className="flex items-center gap-3 px-4 h-12 border-b border-white/[0.06]">
          <Search className="w-4 h-4 text-white/40" />
          <input
            ref={inputRef}
            value={query}
            onChange={(e) => {
              setQuery(e.target.value)
              setHighlighted(0)
            }}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && results[safeHighlight]) {
                e.preventDefault()
                go(results[safeHighlight])
              }
            }}
            placeholder="Jump to a page, device, or ticket"
            className="flex-1 bg-transparent text-sm text-white placeholder:text-white/30 outline-none"
          />
          <kbd className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-white/[0.06] text-white/40">
            esc
          </kbd>
        </div>

        <div className="max-h-[60vh] overflow-y-auto py-1.5">
          {results.length === 0 ? (
            <p className="px-4 py-8 text-center text-xs text-white/30">No matches.</p>
          ) : (
            grouped.map(([groupLabel, items]) => (
              <div key={groupLabel} className="mb-1.5 last:mb-0">
                <p className="px-4 pt-1.5 pb-1 text-[10px] uppercase tracking-[0.16em] text-white/25">
                  {groupLabel}
                </p>
                {items.map((item) => {
                  const idx = results.indexOf(item)
                  const active = idx === safeHighlight
                  return (
                    <button
                      key={item.id}
                      onMouseEnter={() => setHighlighted(idx)}
                      onClick={() => go(item)}
                      className={`w-full flex items-center gap-3 px-4 py-2 text-left text-[13px] transition-colors ${
                        active ? 'bg-white/[0.06] text-white' : 'text-white/70 hover:bg-white/[0.03]'
                      }`}
                    >
                      <item.icon className="w-3.5 h-3.5 text-white/45 shrink-0" />
                      <span className="flex-1 min-w-0 truncate">{item.label}</span>
                      {item.hint && (
                        <span className="text-[11px] text-white/30 truncate max-w-[40%]">{item.hint}</span>
                      )}
                      <ArrowRight className="w-3 h-3 text-white/30 shrink-0" />
                    </button>
                  )
                })}
              </div>
            ))
          )}
        </div>

        <div className="border-t border-white/[0.06] px-4 py-2 flex items-center gap-3 text-[10px] text-white/30">
          <span>
            <kbd className="font-mono px-1 py-0.5 rounded bg-white/[0.06] text-white/45">↑↓</kbd>{' '}
            navigate
          </span>
          <span>
            <kbd className="font-mono px-1 py-0.5 rounded bg-white/[0.06] text-white/45">↵</kbd>{' '}
            open
          </span>
          <span className="ml-auto">⌘K to toggle</span>
        </div>
      </div>
    </div>
  )
}
