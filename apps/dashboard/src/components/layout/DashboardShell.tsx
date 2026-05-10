'use client'

import { useEffect, useState } from 'react'
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
  ScrollText as ScrollTextPolicies,
  DollarSign,
  FileSpreadsheet,
  ScrollText as ScrollTextLogs,
} from 'lucide-react'
import { ThemeToggle } from '@/components/ThemeToggle'
import { useBranding } from '@/components/BrandingProvider'
import { useCurrentUser } from '@/components/CurrentUserProvider'
import api from '@/lib/api'

type NavItem = {
  label: string
  href: string
  icon: typeof BarChart3
  superAdminOnly?: boolean
  adminOnly?: boolean
}

const navItems: NavItem[] = [
  { label: 'Dashboard', href: '/', icon: BarChart3 },
  { label: 'Agents', href: '/agents', icon: Server },
  { label: 'Tickets', href: '/tickets', icon: Ticket },
  { label: 'Alerts', href: '/alerts', icon: AlertTriangle },
  { label: 'Network Map', href: '/network', icon: Globe },
  { label: 'Patches', href: '/patches', icon: Package },
  { label: 'Maintenance', href: '/admin/maintenance', icon: CalendarClock, adminOnly: true },
  { label: 'Software', href: '/admin/software', icon: Boxes, adminOnly: true },
  { label: 'Groups', href: '/admin/groups', icon: Users, adminOnly: true },
  { label: 'Portal users', href: '/admin/customers', icon: UserCircle, adminOnly: true },
  { label: 'Neighbors', href: '/admin/neighbors', icon: Wifi, adminOnly: true },
  { label: 'Cert monitors', href: '/admin/cert-monitors', icon: ShieldEllipsis, adminOnly: true },
  { label: 'SNMP', href: '/admin/snmp', icon: Activity, adminOnly: true },
  { label: 'SSO', href: '/admin/sso', icon: KeyRound, adminOnly: true },
  { label: 'Policies', href: '/admin/policies', icon: ScrollTextPolicies, adminOnly: true },
  { label: 'AI cost', href: '/admin/ai/cost', icon: DollarSign, adminOnly: true },
  { label: 'Reports', href: '/admin/reports', icon: FileSpreadsheet, adminOnly: true },
  { label: 'Logs', href: '/admin/logs', icon: ScrollTextLogs, superAdminOnly: true },
  { label: 'Alert Rules', href: '/admin/alert-rules', icon: BellRing, adminOnly: true },
  { label: 'Webhooks', href: '/admin/webhooks', icon: Webhook, adminOnly: true },
  { label: 'Compliance', href: '/admin/compliance', icon: ShieldCheck, adminOnly: true },
  { label: 'Audit Log', href: '/admin/audit', icon: ScrollText, adminOnly: true },
  { label: 'AI', href: '/admin/ai', icon: Sparkles },
  { label: 'Settings', href: '/settings', icon: Settings },
  { label: 'Tenants', href: '/admin/tenants', icon: Building2, superAdminOnly: true },
]

export default function DashboardShell({
  children,
  alertCount = 0,
}: {
  children: React.ReactNode
  alertCount?: number
}) {
  const [mobileOpen, setMobileOpen] = useState(false)
  const [time, setTime] = useState<Date | null>(null)
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

  useEffect(() => {
    setTime(new Date())
    const timer = setInterval(() => setTime(new Date()), 1000)
    return () => clearInterval(timer)
  }, [])

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

  return (
    <div className={`min-h-screen bg-[#030308] text-white flex relative ${isImpersonating || user?.tenant_in_grace ? 'pt-9' : ''}`}>
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
      {/* Desktop Sidebar */}
      <aside className="hidden md:flex w-60 flex-col border-r border-white/[0.06] bg-[#030308] fixed h-screen z-40">
        {/* Logo */}
        <div className="h-16 flex items-center px-5 border-b border-white/[0.06]">
          <Link href="/" className="flex items-center gap-2.5">
            <div
              className="w-8 h-8 rounded-lg flex items-center justify-center"
              style={{
                background: `linear-gradient(135deg, var(--brand-primary), var(--brand-secondary))`,
              }}
            >
              <span className="text-white font-bold text-sm">
                {branding.company_name.charAt(0).toUpperCase()}
              </span>
            </div>
            <div>
              <span
                className="text-base font-bold bg-clip-text text-transparent"
                style={{
                  backgroundImage: `linear-gradient(to right, var(--brand-primary), var(--brand-secondary))`,
                }}
              >
                {branding.company_name}
              </span>
              <p className="text-[10px] text-white/30 -mt-0.5">{branding.app_name}</p>
            </div>
          </Link>
        </div>

        {/* Navigation */}
        <nav className="flex-1 px-3 py-4 space-y-1 overflow-y-auto">
          {navItems.filter((item) => {
            if (item.superAdminOnly && !isSuperAdmin) return false
            if (item.adminOnly && user?.role !== 'admin' && !isSuperAdmin) return false
            return true
          }).map((item) => {
            const active =
              pathname === item.href || pathname.startsWith(item.href + '/')
            return (
              <Link
                key={item.href}
                href={item.href}
                className={`flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm font-medium transition-all ${
                  active
                    ? 'text-cyan-400 bg-cyan-500/5 border-l-2 border-cyan-400 shadow-[0_0_12px_rgba(0,240,255,0.08)]'
                    : 'text-white/50 hover:text-white hover:bg-white/[0.04]'
                }`}
              >
                <item.icon className="w-4 h-4" />
                {item.label}
              </Link>
            )
          })}
        </nav>

        {/* Tenant + user footer */}
        <div className="px-4 py-3 border-t border-white/[0.06] space-y-3">
          <div>
            <p className="text-[10px] uppercase tracking-[0.14em] text-white/30 mb-0.5">
              Tenant
            </p>
            <p
              className={`text-xs font-medium truncate ${
                isSuperAdmin ? 'text-amber-300/90' : 'text-white/85'
              }`}
            >
              {isSuperAdmin ? 'All tenants' : user?.tenant_name || '—'}
            </p>
          </div>
          <div className="flex items-center gap-3">
            <div className="w-8 h-8 rounded-full bg-gradient-to-br from-violet-500 to-cyan-500 flex items-center justify-center text-xs font-bold">
              {user?.name?.charAt(0).toUpperCase() || '?'}
            </div>
            <div className="flex-1 min-w-0">
              <p className="text-xs font-medium text-white truncate">
                {user?.name || '—'}
              </p>
              <p className="text-[10px] text-white/30 truncate capitalize">
                {user?.role?.replace('_', ' ') || '—'}
              </p>
            </div>
            <button
              onClick={handleSignOut}
              className="text-white/30 hover:text-rose-400 transition-colors"
              title="Sign out"
            >
              <LogOut className="w-4 h-4" />
            </button>
          </div>
        </div>
      </aside>

      {/* Mobile overlay */}
      {mobileOpen && (
        <div
          className="fixed inset-0 bg-black/60 z-40 md:hidden"
          onClick={() => setMobileOpen(false)}
        />
      )}

      {/* Mobile Sidebar Drawer */}
      <aside
        className={`fixed inset-y-0 left-0 w-60 bg-[#0a0a10] border-r border-white/[0.06] z-50 transform transition-transform duration-300 md:hidden ${
          mobileOpen ? 'translate-x-0' : '-translate-x-full'
        }`}
      >
        <div className="h-16 flex items-center justify-between px-5 border-b border-white/[0.06]">
          <span className="text-base font-bold text-white">{branding.app_name}</span>
          <button
            onClick={() => setMobileOpen(false)}
            className="text-white/40 hover:text-white"
          >
            <X className="w-5 h-5" />
          </button>
        </div>
        <nav className="p-3 space-y-1">
          {navItems.filter((item) => {
            if (item.superAdminOnly && !isSuperAdmin) return false
            if (item.adminOnly && user?.role !== 'admin' && !isSuperAdmin) return false
            return true
          }).map((item) => {
            const active =
              pathname === item.href || pathname.startsWith(item.href + '/')
            return (
              <Link
                key={item.href}
                href={item.href}
                onClick={() => setMobileOpen(false)}
                className={`flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm font-medium transition-all ${
                  active
                    ? 'text-cyan-400 bg-cyan-500/5 border-l-2 border-cyan-400'
                    : 'text-white/50 hover:text-white hover:bg-white/[0.04]'
                }`}
              >
                <item.icon className="w-4 h-4" />
                {item.label}
              </Link>
            )
          })}
        </nav>
      </aside>

      {/* Main Content Area */}
      <div className="flex-1 md:ml-60 flex flex-col min-h-screen">
        {/* Top Bar */}
        <header className="h-16 border-b border-white/[0.06] bg-[#030308]/80 backdrop-blur-xl sticky top-0 z-30 flex items-center justify-between px-6">
          <div className="flex items-center gap-4">
            <button
              className="md:hidden text-white/50 hover:text-white"
              onClick={() => setMobileOpen(true)}
            >
              <Menu className="w-5 h-5" />
            </button>
            <div className="relative hidden sm:block">
              <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-4 h-4 text-white/30" />
              <input
                type="text"
                placeholder="Search..."
                className="bg-white/[0.04] border border-white/[0.06] rounded-lg pl-9 pr-3 py-1.5 text-sm text-white placeholder:text-white/20 focus:outline-none focus:border-cyan-500/40 w-64 transition-colors"
              />
            </div>
          </div>

          <div className="flex items-center gap-4">
            {time && (
              <div className="hidden lg:block text-right">
                <div className="text-sm font-mono text-white/80">
                  {time.toLocaleTimeString('en-US', {
                    hour: '2-digit',
                    minute: '2-digit',
                    second: '2-digit',
                  })}
                </div>
                <div className="text-[10px] text-white/30">
                  {time.toLocaleDateString('en-US', {
                    weekday: 'short',
                    month: 'short',
                    day: 'numeric',
                  })}
                </div>
              </div>
            )}

            <ThemeToggle />

            <button className="relative text-white/40 hover:text-white transition-colors">
              <Bell className="w-5 h-5" />
              {alertCount > 0 && (
                <span className="absolute -top-1 -right-1 w-4 h-4 bg-rose-500 rounded-full text-[10px] flex items-center justify-center animate-pulse">
                  {alertCount}
                </span>
              )}
            </button>

            <div className="w-8 h-8 rounded-full bg-gradient-to-br from-violet-500 to-cyan-500 flex items-center justify-center text-xs font-bold">
              A
            </div>
          </div>
        </header>

        <main className="flex-1 p-6">{children}</main>
      </div>
    </div>
  )
}
