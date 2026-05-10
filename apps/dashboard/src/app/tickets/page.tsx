'use client'

import { useEffect, useState } from 'react'
import Link from 'next/link'
import { toast } from 'sonner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import AuthGuard from '@/components/AuthGuard'
import { ThemeToggle } from '@/components/ThemeToggle'
import CreateTicketModal from '@/components/dashboard/CreateTicketModal'
import { ticketsApi, type Ticket } from '@/lib/api'

const priorityClass: Record<string, string> = {
  critical: 'bg-red-500/15 border-red-500/40 text-red-300',
  high: 'bg-orange-500/15 border-orange-500/40 text-orange-300',
  medium: 'bg-amber-500/15 border-amber-500/40 text-amber-300',
  low: 'bg-slate-500/15 border-slate-500/40 text-slate-300',
}

const statusClass: Record<string, string> = {
  open: 'bg-blue-500/15 border-blue-500/40 text-blue-300',
  in_progress: 'bg-purple-500/15 border-purple-500/40 text-purple-300',
  pending: 'bg-amber-500/15 border-amber-500/40 text-amber-300',
  resolved: 'bg-emerald-500/15 border-emerald-500/40 text-emerald-300',
  closed: 'bg-slate-500/15 border-slate-500/40 text-slate-300',
}

const STATUS_OPTIONS: Ticket['status'][] = ['open', 'in_progress', 'pending', 'resolved', 'closed']

export default function TicketsPage() {
  const [tickets, setTickets] = useState<Ticket[]>([])
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
  const [statusFilter, setStatusFilter] = useState<'open' | 'all'>('open')

  const load = async () => {
    setLoading(true)
    try {
      setTickets(await ticketsApi.list())
    } catch {
      toast.error('Failed to load tickets')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { void load() }, [])

  const visible = statusFilter === 'open'
    ? tickets.filter((t) => t.status !== 'resolved' && t.status !== 'closed')
    : tickets

  const updateStatus = async (id: string, status: Ticket['status']) => {
    try {
      await ticketsApi.update(id, { status })
      setTickets((prev) => prev.map((t) => (t.id === id ? { ...t, status } : t)))
    } catch {
      toast.error('Failed to update ticket')
    }
  }

  return (
    <AuthGuard>
      <div className="min-h-screen bg-gradient-to-br from-slate-950 via-slate-900 to-slate-800 text-white">
        <header className="border-b border-slate-800/50 bg-slate-950/80 backdrop-blur-xl sticky top-0 z-50">
          <div className="container mx-auto px-6 py-3">
            <div className="flex items-center justify-between">
              <Link href="/" className="text-xl font-bold bg-gradient-to-r from-blue-400 to-purple-400 bg-clip-text text-transparent">
                vaporRMM
              </Link>
              <div className="flex items-center gap-3">
                <ThemeToggle />
                <Link href="/">
                  <Button variant="ghost" size="sm" className="text-slate-400 hover:text-white">← Dashboard</Button>
                </Link>
              </div>
            </div>
          </div>
        </header>
        <main className="container mx-auto px-6 py-8">
          <div className="flex items-center justify-between mb-6">
            <h1 className="text-2xl font-bold">Tickets</h1>
            <div className="flex items-center gap-3">
              <select
                value={statusFilter}
                onChange={(e) => setStatusFilter(e.target.value as 'open' | 'all')}
                className="bg-slate-800 border border-slate-700 rounded-md px-3 py-1.5 text-sm"
              >
                <option value="open">Open</option>
                <option value="all">All</option>
              </select>
              <Button onClick={() => setShowCreate(true)}>New ticket</Button>
            </div>
          </div>

          {loading ? (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardContent className="py-12 text-center text-slate-400">Loading…</CardContent>
            </Card>
          ) : visible.length === 0 ? (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardContent className="py-12 text-center text-slate-400">
                <p>No {statusFilter === 'open' ? 'open ' : ''}tickets.</p>
                <p className="text-sm mt-2">Tickets appear here when agents fire help requests or you create one manually.</p>
              </CardContent>
            </Card>
          ) : (
            <div className="grid gap-3">
              {visible.map((t) => (
                <Card key={t.id} className="bg-slate-900/60 border-slate-800/50">
                  <CardHeader className="pb-2 flex flex-row items-start justify-between gap-3">
                    <div className="flex flex-col">
                      <CardTitle className="text-base">{t.title}</CardTitle>
                      <div className="flex items-center gap-2 mt-1 text-xs">
                        <span className={`px-2 py-0.5 rounded border ${priorityClass[t.priority] ?? priorityClass.medium}`}>
                          {t.priority}
                        </span>
                        <span className={`px-2 py-0.5 rounded border ${statusClass[t.status] ?? statusClass.open}`}>
                          {t.status}
                        </span>
                        <span className="text-slate-500">
                          {new Date(t.created_at * 1000).toLocaleString()}
                        </span>
                      </div>
                    </div>
                    <select
                      value={t.status}
                      onChange={(e) => void updateStatus(t.id, e.target.value as Ticket['status'])}
                      className="bg-slate-800 border border-slate-700 rounded-md px-2 py-1 text-xs"
                    >
                      {STATUS_OPTIONS.map((s) => (
                        <option key={s} value={s}>{s}</option>
                      ))}
                    </select>
                  </CardHeader>
                  {t.description && (
                    <CardContent>
                      <p className="text-sm text-slate-300 whitespace-pre-wrap">{t.description}</p>
                    </CardContent>
                  )}
                </Card>
              ))}
            </div>
          )}
        </main>
      </div>

      <CreateTicketModal
        open={showCreate}
        onClose={() => setShowCreate(false)}
        onCreated={() => { setShowCreate(false); void load() }}
      />
    </AuthGuard>
  )
}
