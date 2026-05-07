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
} from 'lucide-react'
import { ThemeToggle } from '@/components/ThemeToggle'
import { useBranding } from '@/components/BrandingProvider'
import api from '@/lib/api'

const navItems = [
  { label: 'Dashboard', href: '/', icon: BarChart3 },
  { label: 'Agents', href: '/agents', icon: Server },
  { label: 'Tickets', href: '/tickets', icon: Ticket },
  { label: 'Alerts', href: '/alerts', icon: AlertTriangle },
  { label: 'Network Map', href: '/network', icon: Globe },
  { label: 'Patches', href: '/patches', icon: Package },
  { label: 'Settings', href: '/settings', icon: Settings },
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
    localStorage.removeItem('auth_token')
    localStorage.removeItem('user_id')
    localStorage.removeItem('user_email')
    window.location.href = '/login'
  }

  return (
    <div className="min-h-screen bg-[#030308] text-white flex">
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
          {navItems.map((item) => {
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

        {/* Bottom branding */}
        <div className="p-4 border-t border-white/[0.06]">
          <div className="flex items-center gap-3">
            <div className="w-8 h-8 rounded-full bg-gradient-to-br from-violet-500 to-cyan-500 flex items-center justify-center text-xs font-bold">
              A
            </div>
            <div className="flex-1 min-w-0">
              <p className="text-xs font-medium text-white truncate">Admin</p>
              <p className="text-[10px] text-white/30 truncate">Administrator</p>
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
          {navItems.map((item) => {
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
