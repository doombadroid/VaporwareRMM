'use client'

import { useEffect, useMemo, useState } from 'react'
import Link from 'next/link'
import { toast } from 'sonner'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { PageHeader, EmptyState } from '@/components/ui/page'
import { Pill, severityTone, StatusDot, statusTone } from '@/components/ui/status'
import { FilterBar, FilterChip } from '@/components/ui/data-table'
import { Button } from '@/components/ui/button'
import { Plus } from 'lucide-react'
import CreateTicketModal from '@/components/dashboard/CreateTicketModal'
import { ticketsApi, type Ticket } from '@/lib/api'

type View = 'open' | 'all' | 'mine'
const STATUS_OPTIONS: Ticket['status'][] = ['open', 'in_progress', 'pending', 'resolved', 'closed']

export default function TicketsPage() {
  const [tickets, setTickets] = useState<Ticket[]>([])
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
  const [view, setView] = useState<View>('open')

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
  useEffect(() => {
    void load()
  }, [])

  const counts = useMemo(() => {
    const open = tickets.filter((t) => t.status !== 'resolved' && t.status !== 'closed').length
    return { open, all: tickets.length }
  }, [tickets])

  const visible = view === 'open' ? tickets.filter((t) => t.status !== 'resolved' && t.status !== 'closed') : tickets

  const updateStatus = async (id: string, status: Ticket['status']) => {
    try {
      await ticketsApi.update(id, { status })
      setTickets((prev) => prev.map((t) => (t.id === id ? { ...t, status } : t)))
    } catch {
      toast.error('Failed to update')
    }
  }

  return (
    <AuthGuard>
      <DashboardShell>
        <PageHeader
          eyebrow="Operate"
          title="Tickets"
          description="Service requests from agents, monitors, and customers."
          actions={
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus className="w-3.5 h-3.5 mr-1.5" />
              New ticket
            </Button>
          }
          separator={false}
        />

        <FilterBar>
          <FilterChip label="Open" active={view === 'open'} onClick={() => setView('open')} count={counts.open} />
          <FilterChip label="All" active={view === 'all'} onClick={() => setView('all')} count={counts.all} />
        </FilterBar>

        {loading ? (
          <p className="text-[13px] text-white/45">Loading…</p>
        ) : visible.length === 0 ? (
          <EmptyState
            title={view === 'open' ? 'No open tickets.' : 'No tickets yet.'}
            hint="Tickets appear here when agents fire help requests or you create one."
            action={
              <Button size="sm" onClick={() => setShowCreate(true)}>
                <Plus className="w-3.5 h-3.5 mr-1.5" />
                New ticket
              </Button>
            }
          />
        ) : (
          <ul className="border border-white/[0.06] rounded-lg overflow-hidden divide-y divide-white/[0.04] bg-white/[0.01]">
            {visible.map((t) => (
              <li
                key={t.id}
                className="flex items-start gap-3 px-4 py-3 hover:bg-white/[0.02] transition-colors"
              >
                <div className="flex flex-col gap-1.5 shrink-0">
                  <Pill tone={severityTone(t.priority)}>{t.priority}</Pill>
                </div>
                <div className="min-w-0 flex-1">
                  <Link
                    href={`/tickets/${t.id}`}
                    className="text-[13.5px] text-white/90 hover:text-cyan-400 transition-colors font-medium block truncate"
                  >
                    {t.title}
                  </Link>
                  <p className="text-[11px] text-white/35 mt-1 inline-flex items-center gap-1.5">
                    <StatusDot tone={statusTone(t.status)} />
                    {t.status}
                    <span className="text-white/25">·</span>
                    {new Date(t.created_at * 1000).toLocaleString()}
                  </p>
                </div>
                <select
                  value={t.status}
                  onChange={(e) => void updateStatus(t.id, e.target.value as Ticket['status'])}
                  onClick={(e) => e.stopPropagation()}
                  className="bg-white/[0.04] border border-white/[0.08] rounded-md px-2 py-1 text-[11.5px] text-white/85 focus:outline-none focus:border-white/[0.2] shrink-0"
                >
                  {STATUS_OPTIONS.map((s) => (
                    <option key={s} value={s}>
                      {s}
                    </option>
                  ))}
                </select>
              </li>
            ))}
          </ul>
        )}

        <CreateTicketModal
          open={showCreate}
          onClose={() => setShowCreate(false)}
          onCreated={() => {
            setShowCreate(false)
            void load()
          }}
        />
      </DashboardShell>
    </AuthGuard>
  )
}
