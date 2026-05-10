'use client'

import { useEffect, useRef, useState } from 'react'
import Link from 'next/link'
import { usePathname } from 'next/navigation'
import {
  BarChart3,
  Server,
  Ticket,
  AlertTriangle,
  Globe,
  Package,
  Settings,
  Menu,
  X,
  Bell,
  Search,
  LogOut,
  Building2,
  Sparkles,
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
  ScrollText as PolicyIcon,
  DollarSign,
  FileSpreadsheet,
  Terminal,
  ChevronDown,
} from 'lucide-react'
import { useBranding } from '@/components/BrandingProvider'
import { useCurrentUser } from '@/components/CurrentUserProvider'
import api from '@/lib/api'
import CommandPalette from '@/components/CommandPalette'

type NavItem = {
  label: string
  href: string
  icon: typeof BarChart3
  superAdminOnly?: boolean
  adminOnly?: boolean
}

type NavGroup = {
  label: string
  items: NavItem[]
}

// Nav grouped by operator workflow. Six clusters keeps the sidebar
// scannable at the 25+ item count we're at after Stages 8-17 — flat
// lists past ~10 entries cease to be navigation, they become noise.
const navGroups: NavGroup[] = [
  {
    label: 'Operate',
    items: [
      { label: 'Dashboard', href: '/', icon: BarChart3 },
      { label: 'Devices', href: '/agents', icon: Server },
      { label: 'Tickets', href: '/tickets', icon: Ticket },
      { label: 'Alerts', href: '/alerts', icon: AlertTriangle },
    ],
  },
  {
    label: 'Manage',
    items: [
      { label: 'Patches', href: '/patches', icon: Package },
      { label: 'Maintenance', href: '/admin/maintenance', icon: CalendarClock, adminOnly: true },
      { label: 'Software', href: '/admin/software', icon: Boxes, adminOnly: true },
      { label: 'Groups', href: '/admin/groups', icon: Users, adminOnly: true },
      { label: 'Customers', href: '/admin/customers', icon: UserCircle, adminOnly: true },
    ],
  },
  {
    label: 'Network',
    items: [
      { label: 'Map', href: '/network', icon: Globe },
      { label: 'Neighbors', href: '/admin/neighbors', icon: Wifi, adminOnly: true },
      { label: 'Cert monitors', href: '/admin/cert-monitors', icon: ShieldEllipsis, adminOnly: true },
      { label: 'SNMP', href: '/admin/snmp', icon: Activity, adminOnly: true },
    ],
  },
  {
    label: 'Automation',
    items: [
      { label: 'Alert rules', href: '/admin/alert-rules', icon: BellRing, adminOnly: true },
      { label: 'Webhooks', href: '/admin/webhooks', icon: Webhook, adminOnly: true },
      { label: 'Reports', href: '/admin/reports', icon: FileSpreadsheet, adminOnly: true },
      { label: 'AI', href: '/admin/ai', icon: Sparkles },
    ],
  },
  {
    label: 'Audit',
    items: [
      { label: 'Audit log', href: '/admin/audit', icon: ScrollText, adminOnly: true },
      { label: 'Compliance', href: '/admin/compliance', icon: ShieldCheck, adminOnly: true },
      { label: 'AI cost', href: '/admin/ai/cost', icon: DollarSign, adminOnly: true },
    ],
  },
  {
    label: 'System',
    items: [
      { label: 'SSO', href: '/admin/sso', icon: KeyRound, adminOnly: true },
      { label: 'Policies', href: '/admin/policies', icon: PolicyIcon, adminOnly: true },
      { label: 'Settings', href: '/settings', icon: Settings },
      { label: 'Tenants', href: '/admin/tenants', icon: Building2, superAdminOnly: true },
      { label: 'Logs', href: '/admin/logs', icon: Terminal, superAdminOnly: true },
    ],
  },
]

export default function DashboardShell({
  children,
  alertCount = 0,
}: {
  children: React.ReactNode
  alertCount?: number
}) {
  const [mobileOpen, setMobileOpen] = useState(false)
  const [paletteOpen, setPaletteOpen] = useState(false)
  const [profileOpen, setProfileOpen] = useState(false)
  const profileRef = useRef<HTMLDivElement | null>(null)
  const pathname = usePathname()
  const { branding } = useBranding()
  const { user, refresh } = useCurrentUser()
  const isSuperAdmin = user?.role === 'super_admin'
  const isImpersonating = !!user?.impersonating

  const handleEndImpersonation = async () => {
    try {
      const { tenantsApi } = await import('@/lib/api')
      await tenantsApi.endImpersonation()
      await refresh()
      window.location.href = '/'
    } catch {
      // ignore — UI shows state, user can retry
    }
  }

  const handleSignOut = async () => {
    try {
      await api.post('/auth/logout')
    } catch {
      // ignore
    }
    localStorage.removeItem('auth_expiry')
    localStorage.removeItem('user_id')
    localStorage.removeItem('user_email')
    window.location.href = '/login'
  }

  // Global ⌘K / Ctrl-K opens the command palette. Single source of nav
  // truth from anywhere in the app — operators don't need to think about
  // which page they're on to jump elsewhere.
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        setPaletteOpen((s) => !s)
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [])

  useEffect(() => {
    if (!profileOpen) return
    const handler = (e: MouseEvent) => {
      if (profileRef.current && !profileRef.current.contains(e.target as Node)) {
        setProfileOpen(false)
      }
    }
    window.addEventListener('mousedown', handler)
    return () => window.removeEventListener('mousedown', handler)
  }, [profileOpen])

  const visibleGroups = navGroups
    .map((g) => ({
      ...g,
      items: g.items.filter((item) => {
        if (item.superAdminOnly && !isSuperAdmin) return false
        if (item.adminOnly && user?.role !== 'admin' && !isSuperAdmin) return false
        return true
      }),
    }))
    .filter((g) => g.items.length > 0)

  const isActive = (href: string) =>
    pathname === href ||
    (href !== '/' && pathname.startsWith(href + '/')) ||
    (href === '/' && pathname === '/')

  return (
    <div
      className={`min-h-screen bg-[#030308] text-white flex relative ${
        isImpersonating || user?.tenant_in_grace ? 'pt-9' : ''
      }`}
    >
      {/* Super-admin amber hairline. The ONE place amber appears in chrome —
          announces "you're seeing across all tenants". */}
      {isSuperAdmin && !isImpersonating && (
        <div
          className="fixed top-0 left-0 right-0 h-px bg-amber-500/50 z-50 pointer-events-none"
          aria-hidden="true"
        />
      )}
      {isImpersonating && (
        <div className="fixed top-0 left-0 right-0 z-50 bg-amber-500/95 text-amber-950 text-xs font-medium px-4 py-2 flex items-center justify-between shadow-lg">
          <span className="flex items-center gap-2">
            <span className="inline-block w-1.5 h-1.5 rounded-full bg-amber-950 animate-pulse" />
            Viewing as <span className="font-semibold">{user?.tenant_name}</span> · super-admin powers temporarily reduced to tenant_admin
          </span>
          <button
            onClick={handleEndImpersonation}
            className="bg-amber-950 text-amber-100 hover:bg-amber-800 px-3 py-1 rounded font-medium text-xs transition-colors"
          >
            End impersonation
          </button>
        </div>
      )}
      {user?.tenant_in_grace && !isImpersonating && (
        <div className="fixed top-0 left-0 right-0 z-50 bg-rose-500/95 text-rose-50 text-xs font-medium px-4 py-2 flex items-center justify-center shadow-lg">
          <span className="flex items-center gap-2">
            <span className="inline-block w-1.5 h-1.5 rounded-full bg-rose-50 animate-pulse" />
            <span>
              Account suspended. Access ends{' '}
              <strong>
                {user.grace_deadline
                  ? new Date(user.grace_deadline * 1000).toLocaleString()
                  : 'soon'}
              </strong>
              . Contact your administrator.
            </span>
          </span>
        </div>
      )}

      {/* Desktop sidebar */}
      <aside className="hidden md:flex w-60 flex-col border-r border-white/[0.06] bg-[#030308] fixed h-screen z-40">
        <div className="h-14 flex items-center px-4 border-b border-white/[0.06]">
          <Link href="/" className="flex items-center gap-2.5 min-w-0">
            <div className="w-7 h-7 rounded-md bg-gradient-to-br from-cyan-500 to-violet-600 flex items-center justify-center shrink-0">
              <span className="text-white font-bold text-xs">
                {branding.company_name.charAt(0).toUpperCase()}
              </span>
            </div>
            <div className="min-w-0">
              <p className="text-[13px] font-semibold text-white truncate leading-tight">
                {branding.app_name}
              </p>
              <p className="text-[10px] text-white/35 truncate leading-tight tracking-wide">
                {branding.company_name}
              </p>
            </div>
          </Link>
        </div>

        {/* Tenant chip — second-most-prominent fixed UI after the brand,
            because PRODUCT.md says "every screen answers: what tenant am
            I in." */}
        <div className="px-3 pt-3 pb-2 border-b border-white/[0.06]">
          <div className="px-2.5 py-1.5 rounded-md bg-white/[0.03]">
            <p className="text-[9px] uppercase tracking-[0.18em] text-white/30 leading-none mb-1">
              Tenant
            </p>
            <p
              className={`text-xs font-medium truncate leading-tight ${
                isSuperAdmin ? 'text-amber-300/90' : 'text-white/90'
              }`}
            >
              {isSuperAdmin ? 'All tenants' : user?.tenant_name || '—'}
            </p>
          </div>
        </div>

        <nav className="flex-1 px-2 py-3 overflow-y-auto">
          {visibleGroups.map((group) => (
            <div key={group.label} className="mb-4 last:mb-0">
              <p className="px-3 mb-1 text-[10px] uppercase tracking-[0.16em] text-white/25 font-medium">
                {group.label}
              </p>
              <div className="space-y-px">
                {group.items.map((item) => {
                  const active = isActive(item.href)
                  return (
                    <Link
                      key={item.href}
                      href={item.href}
                      className={`flex items-center gap-3 px-3 py-1.5 rounded-md text-[13px] transition-colors ${
                        active
                          ? 'bg-white/[0.06] text-white font-medium'
                          : 'text-white/55 hover:text-white hover:bg-white/[0.03]'
                      }`}
                    >
                      <item.icon
                        className={`w-3.5 h-3.5 shrink-0 ${active ? 'text-cyan-400' : ''}`}
                      />
                      <span className="truncate">{item.label}</span>
                    </Link>
                  )
                })}
              </div>
            </div>
          ))}
        </nav>

        <div className="px-3 py-3 border-t border-white/[0.06]">
          <div className="flex items-center gap-2.5 px-2">
            <div className="w-7 h-7 rounded-full bg-gradient-to-br from-violet-500 to-cyan-500 flex items-center justify-center text-[11px] font-bold shrink-0">
              {user?.name?.charAt(0).toUpperCase() || '?'}
            </div>
            <div className="flex-1 min-w-0">
              <p className="text-xs font-medium text-white truncate leading-tight">
                {user?.name || '—'}
              </p>
              <p className="text-[10px] text-white/35 truncate capitalize leading-tight">
                {user?.role?.replace('_', ' ') || '—'}
              </p>
            </div>
          </div>
        </div>
      </aside>

      {mobileOpen && (
        <div
          className="fixed inset-0 bg-black/60 z-40 md:hidden"
          onClick={() => setMobileOpen(false)}
        />
      )}

      <aside
        className={`fixed inset-y-0 left-0 w-64 bg-[#0a0a10] border-r border-white/[0.06] z-50 transform transition-transform duration-200 md:hidden flex flex-col ${
          mobileOpen ? 'translate-x-0' : '-translate-x-full'
        }`}
      >
        <div className="h-14 flex items-center justify-between px-4 border-b border-white/[0.06]">
          <span className="text-sm font-semibold text-white">{branding.app_name}</span>
          <button onClick={() => setMobileOpen(false)} className="text-white/40 hover:text-white">
            <X className="w-5 h-5" />
          </button>
        </div>
        <nav className="flex-1 px-2 py-3 overflow-y-auto">
          {visibleGroups.map((group) => (
            <div key={group.label} className="mb-4">
              <p className="px-3 mb-1 text-[10px] uppercase tracking-[0.16em] text-white/25 font-medium">
                {group.label}
              </p>
              <div className="space-y-px">
                {group.items.map((item) => {
                  const active = isActive(item.href)
                  return (
                    <Link
                      key={item.href}
                      href={item.href}
                      onClick={() => setMobileOpen(false)}
                      className={`flex items-center gap-3 px-3 py-1.5 rounded-md text-[13px] transition-colors ${
                        active
                          ? 'bg-white/[0.06] text-white font-medium'
                          : 'text-white/55 hover:text-white'
                      }`}
                    >
                      <item.icon className={`w-3.5 h-3.5 ${active ? 'text-cyan-400' : ''}`} />
                      {item.label}
                    </Link>
                  )
                })}
              </div>
            </div>
          ))}
        </nav>
      </aside>

      <div className="flex-1 md:ml-60 flex flex-col min-h-screen">
        <header className="h-14 border-b border-white/[0.06] bg-[#030308]/80 backdrop-blur-xl sticky top-0 z-30 flex items-center justify-between px-4 md:px-6">
          <div className="flex items-center gap-3 min-w-0">
            <button
              className="md:hidden text-white/50 hover:text-white"
              onClick={() => setMobileOpen(true)}
              aria-label="Open menu"
            >
              <Menu className="w-5 h-5" />
            </button>
            <button
              type="button"
              onClick={() => setPaletteOpen(true)}
              className="group flex items-center gap-2 bg-white/[0.03] border border-white/[0.06] hover:border-white/[0.12] rounded-md pl-2.5 pr-1.5 py-1.5 text-xs text-white/40 hover:text-white/60 transition-colors min-w-[180px] sm:min-w-[280px]"
            >
              <Search className="w-3.5 h-3.5" />
              <span className="flex-1 text-left">Search devices, tickets…</span>
              <kbd className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-white/[0.06] text-white/45 group-hover:text-white/70">
                ⌘K
              </kbd>
            </button>
          </div>

          <div className="flex items-center gap-2">
            <Link
              href="/alerts"
              className="relative text-white/40 hover:text-white p-1.5 transition-colors"
              aria-label={`${alertCount} alerts`}
            >
              <Bell className="w-4 h-4" />
              {alertCount > 0 && (
                <span className="absolute top-0 right-0 min-w-[14px] h-[14px] bg-rose-500 rounded-full text-[9px] font-medium flex items-center justify-center px-1">
                  {alertCount > 9 ? '9+' : alertCount}
                </span>
              )}
            </Link>

            <div className="relative" ref={profileRef}>
              <button
                onClick={() => setProfileOpen((s) => !s)}
                className="flex items-center gap-2 px-2 py-1 rounded-md hover:bg-white/[0.04] transition-colors"
              >
                <div className="w-6 h-6 rounded-full bg-gradient-to-br from-violet-500 to-cyan-500 flex items-center justify-center text-[10px] font-bold">
                  {user?.name?.charAt(0).toUpperCase() || '?'}
                </div>
                <ChevronDown className="w-3 h-3 text-white/40" />
              </button>
              {profileOpen && (
                <div className="absolute right-0 top-full mt-1 w-56 bg-[#0a0a10] border border-white/[0.08] rounded-lg shadow-2xl py-1 text-sm">
                  <div className="px-3 py-2 border-b border-white/[0.06]">
                    <p className="text-white text-xs font-medium truncate">{user?.name || '—'}</p>
                    <p className="text-white/40 text-[11px] truncate">{user?.email || ''}</p>
                  </div>
                  <Link
                    href="/settings"
                    onClick={() => setProfileOpen(false)}
                    className="flex items-center gap-2 px-3 py-2 text-white/70 hover:text-white hover:bg-white/[0.04] text-xs"
                  >
                    <Settings className="w-3.5 h-3.5" /> Settings
                  </Link>
                  <button
                    onClick={handleSignOut}
                    className="w-full flex items-center gap-2 px-3 py-2 text-rose-300 hover:bg-rose-500/10 text-xs"
                  >
                    <LogOut className="w-3.5 h-3.5" /> Sign out
                  </button>
                </div>
              )}
            </div>
          </div>
        </header>

        <main className="flex-1 px-4 md:px-6 py-6">{children}</main>
      </div>

      <CommandPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} />
    </div>
  )
}
