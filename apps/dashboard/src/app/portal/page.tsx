'use client'

import { useEffect, useState } from 'react'
import Link from 'next/link'
import { useRouter } from 'next/navigation'
import { toast } from 'sonner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { portalApiClient, portalAuth, type PortalSelf, type PortalTicket } from '@/lib/portal-api'

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
      toast.error('Submit failed (rate-limited?)')
    } finally {
      setCreating(false)
    }
  }

  const logout = async () => {
    await portalAuth.logout().catch(() => {})
    router.push('/portal/login')
  }

  if (loading) {
    return (
      <div className="min-h-screen bg-gradient-to-br from-slate-950 via-slate-900 to-slate-800 flex items-center justify-center text-slate-400">
        Loading…
      </div>
    )
  }

  return (
    <div className="min-h-screen bg-gradient-to-br from-slate-950 via-slate-900 to-slate-800 text-white">
      <header className="border-b border-slate-800/50 bg-slate-950/80 backdrop-blur-xl sticky top-0 z-50">
        <div className="container mx-auto px-6 py-3 flex items-center justify-between">
          <div>
            <p className="text-sm font-bold">Customer portal</p>
            {me && <p className="text-xs text-slate-400">{me.name} · {me.email}</p>}
          </div>
          <Button size="sm" variant="ghost" onClick={logout}>Sign out</Button>
        </div>
      </header>
      <main className="container mx-auto px-6 py-8 max-w-3xl space-y-6">
        <div className="flex items-center justify-between">
          <h1 className="text-2xl font-bold">My tickets</h1>
          <Button onClick={() => setShowCreate((s) => !s)}>
            {showCreate ? 'Cancel' : 'New ticket'}
          </Button>
        </div>

        {showCreate && (
          <Card className="bg-slate-900/60 border-slate-800/50">
            <CardContent className="space-y-3 py-4">
              <input
                type="text"
                placeholder="Subject"
                value={form.title}
                onChange={(e) => setForm({ ...form, title: e.target.value })}
                maxLength={256}
                className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm"
              />
              <textarea
                placeholder="Describe the issue"
                rows={5}
                value={form.description}
                onChange={(e) => setForm({ ...form, description: e.target.value })}
                maxLength={16 * 1024}
                className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm"
              />
              <div className="flex justify-end">
                <Button onClick={create} disabled={creating}>
                  {creating ? 'Submitting…' : 'Submit'}
                </Button>
              </div>
            </CardContent>
          </Card>
        )}

        {tickets.length === 0 ? (
          <Card className="bg-slate-900/60 border-slate-800/50">
            <CardContent className="py-12 text-center text-slate-400">
              <p>No tickets yet.</p>
              <p className="text-sm mt-2">Open one with the button above.</p>
            </CardContent>
          </Card>
        ) : (
          <div className="grid gap-3">
            {tickets.map((t) => (
              <Card key={t.id} className="bg-slate-900/60 border-slate-800/50">
                <CardHeader className="pb-2">
                  <Link href={`/portal/tickets/${t.id}`} className="hover:text-blue-400">
                    <CardTitle className="text-base">{t.title}</CardTitle>
                  </Link>
                  <p className="text-xs text-slate-500 mt-1">
                    {t.status} · {t.priority} · opened {new Date(t.created_at * 1000).toLocaleString()}
                  </p>
                </CardHeader>
              </Card>
            ))}
          </div>
        )}
      </main>
    </div>
  )
}
