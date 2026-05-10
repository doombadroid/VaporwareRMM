'use client'

import { useEffect, useState } from 'react'
import Link from 'next/link'
import { useRouter } from 'next/navigation'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Pill, severityTone, statusTone } from '@/components/ui/status'
import { Sheet } from '@/components/ui/sheet'
import { EmptyState } from '@/components/ui/page'
import { Plus, LogOut } from 'lucide-react'
import { portalApiClient, portalAuth, type PortalSelf, type PortalTicket } from '@/lib/portal-api'

const inputCls = 'w-full bg-white/[0.04] border border-white/[0.08] rounded-md px-3 py-1.5 text-[13px] text-white placeholder:text-white/30 focus:outline-none focus:border-white/[0.2]'

export default function PortalDashboard() {
  const router = useRouter()
  const [me, setMe] = useState<PortalSelf | null>(null)
  const [tickets, setTickets] = useState<PortalTicket[]>([])
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
  const [creating, setCreating] = useState(false)
  const [form, setForm] = useState({ title: '', description: '' })

  useEffect(() => {
    Promise.all([portalApiClient.me(), portalApiClient.listTickets()])
      .then(([self, ts]) => {
        setMe(self)
        setTickets(ts)
      })
      .catch(() => {
        router.push('/portal/login')
      })
      .finally(() => setLoading(false))
  }, [router])

  const create = async () => {
    if (!form.title.trim()) {
      toast.error('Title required')
      return
    }
    setCreating(true)
    try {
      await portalApiClient.createTicket(form)
      const fresh = await portalApiClient.listTickets()
      setTickets(fresh)
      setForm({ title: '', description: '' })
      setShowCreate(false)
      toast.success('Ticket submitted')
    } catch {
      toast.error('Submit failed')
    } finally {
      setCreating(false)
    }
  }

  const logout = async () => {
    await portalAuth.logout().catch(() => {})
    router.push('/portal/login')
  }

  if (loading) {
    return <div className="min-h-screen bg-[#030308] flex items-center justify-center text-white/40 text-[13px]">Loading…</div>
  }

  return (
    <div className="min-h-screen bg-[#030308] text-white">
      <header className="border-b border-white/[0.06] bg-[#030308]/85 backdrop-blur sticky top-0 z-30">
        <div className="container mx-auto px-6 py-3 flex items-center justify-between">
          <div>
            <p className="text-[10.5px] uppercase tracking-[0.16em] text-white/30 font-medium">Customer portal</p>
            {me && (
              <p className="text-[12.5px] text-white/85 mt-0.5">
                {me.name} <span className="text-white/35">· {me.email}</span>
              </p>
            )}
          </div>
          <Button size="sm" variant="ghost" onClick={logout}>
            <LogOut className="w-3.5 h-3.5 mr-1.5" />
            Sign out
          </Button>
        </div>
      </header>

      <main className="container mx-auto px-6 py-8 max-w-2xl">
        <div className="flex items-end justify-between gap-4 mb-6">
          <div>
            <h1 className="text-xl font-semibold tracking-tight">My tickets</h1>
            <p className="text-[12.5px] text-white/45 mt-1.5">{tickets.length} on record.</p>
          </div>
          <Button size="sm" onClick={() => setShowCreate(true)}>
            <Plus className="w-3.5 h-3.5 mr-1.5" />
            New ticket
          </Button>
        </div>

        {tickets.length === 0 ? (
          <EmptyState
            title="No tickets yet."
            hint="Open one with the button above."
            action={
              <Button size="sm" onClick={() => setShowCreate(true)}>
                <Plus className="w-3.5 h-3.5 mr-1.5" />
                New ticket
              </Button>
            }
          />
        ) : (
          <ul className="border border-white/[0.06] rounded-lg overflow-hidden divide-y divide-white/[0.04] bg-white/[0.01]">
            {tickets.map((t) => (
              <li key={t.id}>
                <Link
                  href={`/portal/tickets/${t.id}`}
                  className="flex items-start gap-3 px-4 py-3 hover:bg-white/[0.02] transition-colors"
                >
                  <Pill tone={severityTone(t.priority)}>{t.priority}</Pill>
                  <div className="min-w-0 flex-1">
                    <p className="text-[13.5px] text-white/90 font-medium truncate">{t.title}</p>
                    <p className="text-[11px] text-white/40 mt-1">
                      <span className="inline-flex items-center gap-1">
                        <span
                          className={`inline-block w-1 h-1 rounded-full ${
                            statusTone(t.status) === 'online'
                              ? 'bg-emerald-400'
                              : statusTone(t.status) === 'warn'
                                ? 'bg-amber-400'
                                : 'bg-blue-400'
                          }`}
                        />
                        {t.status}
                      </span>
                      <span className="mx-1.5">·</span>
                      opened {new Date(t.created_at * 1000).toLocaleString()}
                    </p>
                  </div>
                </Link>
              </li>
            ))}
          </ul>
        )}

        <Sheet
          open={showCreate}
          onClose={() => setShowCreate(false)}
          title="New ticket"
          description="Tell us what happened — we'll route it to the right tech."
          footer={
            <>
              <Button variant="ghost" size="sm" onClick={() => setShowCreate(false)}>
                Cancel
              </Button>
              <Button size="sm" onClick={create} disabled={creating}>
                {creating ? 'Submitting…' : 'Submit'}
              </Button>
            </>
          }
        >
          <div className="space-y-4">
            <div>
              <label className="block text-[11px] uppercase tracking-[0.12em] text-white/40 mb-1.5">Subject</label>
              <input
                type="text"
                placeholder="Short description"
                value={form.title}
                onChange={(e) => setForm({ ...form, title: e.target.value })}
                maxLength={256}
                className={inputCls}
              />
            </div>
            <div>
              <label className="block text-[11px] uppercase tracking-[0.12em] text-white/40 mb-1.5">Details</label>
              <textarea
                placeholder="What were you doing? What happened?"
                rows={6}
                value={form.description}
                onChange={(e) => setForm({ ...form, description: e.target.value })}
                maxLength={16 * 1024}
                className={inputCls}
              />
            </div>
          </div>
        </Sheet>
      </main>
    </div>
  )
}
