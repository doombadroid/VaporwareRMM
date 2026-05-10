'use client'

import { useEffect, useState } from 'react'
import { useParams, useRouter } from 'next/navigation'
import Link from 'next/link'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { PageHeader, Section, EmptyState } from '@/components/ui/page'
import { Pill, severityTone, statusTone, Code } from '@/components/ui/status'
import { ConfirmDialog } from '@/components/ui/sheet'
import { useCurrentUser } from '@/components/CurrentUserProvider'
import {
  ticketsApi,
  tenantUsersApi,
  timeEntriesApi,
  type Ticket,
  type TicketComment,
  type TenantUser,
  type TimeEntry,
} from '@/lib/api'
import { ArrowLeft, Send, Lock, Unlock, Clock, Trash2 } from 'lucide-react'

const STATUS_OPTIONS: Ticket['status'][] = ['open', 'in_progress', 'pending', 'resolved', 'closed']
const PRIORITY_OPTIONS: Ticket['priority'][] = ['low', 'medium', 'high', 'critical']
const MAX_COMMENT_BYTES = 32 * 1024

const inputCls = 'bg-white/[0.04] border border-white/[0.08] rounded-md px-2.5 py-1 text-[12.5px] text-white placeholder:text-white/30 focus:outline-none focus:border-white/[0.2]'

export default function TicketDetailPage() {
  const params = useParams()
  const router = useRouter()
  const ticketId = params.id as string
  const { user } = useCurrentUser()
  const isAdmin = user?.role === 'admin' || user?.role === 'super_admin'

  const [ticket, setTicket] = useState<Ticket | null>(null)
  const [comments, setComments] = useState<TicketComment[]>([])
  const [users, setUsers] = useState<TenantUser[]>([])
  const [loading, setLoading] = useState(true)
  const [posting, setPosting] = useState(false)
  const [savingField, setSavingField] = useState<string | null>(null)
  const [draft, setDraft] = useState('')
  const [draftInternal, setDraftInternal] = useState(false)
  const [entries, setEntries] = useState<TimeEntry[]>([])
  const [timeForm, setTimeForm] = useState({ minutes: 30, note: '', billable: true })
  const [loggingTime, setLoggingTime] = useState(false)
  const [confirmEntryDelete, setConfirmEntryDelete] = useState<string | null>(null)

  const loadAll = async () => {
    setLoading(true)
    try {
      const [t, c] = await Promise.all([ticketsApi.get(ticketId), ticketsApi.listComments(ticketId)])
      setTicket(t)
      setComments(c)
    } catch {
      toast.error('Failed to load ticket')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    if (!ticketId) return
    void loadAll()
  }, [ticketId])

  useEffect(() => {
    if (isAdmin) {
      tenantUsersApi.list().then(setUsers).catch(() => {})
      timeEntriesApi.list(ticketId).then(setEntries).catch(() => {})
    }
  }, [isAdmin, ticketId])

  const updateField = async (patch: Partial<Pick<Ticket, 'status' | 'priority' | 'assigned_to'>>) => {
    if (!ticket) return
    setSavingField(Object.keys(patch)[0])
    try {
      await ticketsApi.update(ticket.id, patch)
      setTicket({ ...ticket, ...patch })
      toast.success('Updated')
    } catch {
      toast.error('Update failed')
    } finally {
      setSavingField(null)
    }
  }

  const logTime = async () => {
    if (timeForm.minutes <= 0) {
      toast.error('minutes must be positive')
      return
    }
    setLoggingTime(true)
    try {
      await timeEntriesApi.create(ticketId, {
        minutes: timeForm.minutes,
        billable: timeForm.billable,
        note: timeForm.note,
      })
      const fresh = await timeEntriesApi.list(ticketId)
      setEntries(fresh)
      setTimeForm({ minutes: 30, note: '', billable: true })
      toast.success('Logged')
    } catch {
      toast.error('Failed to log time')
    } finally {
      setLoggingTime(false)
    }
  }

  const removeEntry = async (id: string) => {
    try {
      await timeEntriesApi.remove(id)
      setEntries((p) => p.filter((e) => e.id !== id))
      setConfirmEntryDelete(null)
    } catch {
      toast.error('Failed to delete')
    }
  }

  const totalMinutes = entries.reduce((acc, e) => acc + e.minutes, 0)
  const billableMinutes = entries.filter((e) => e.billable).reduce((acc, e) => acc + e.minutes, 0)

  const post = async () => {
    if (!draft.trim()) return
    if (draft.length > MAX_COMMENT_BYTES) {
      toast.error('Comment too long')
      return
    }
    setPosting(true)
    try {
      await ticketsApi.addComment(ticketId, draft, draftInternal && isAdmin)
      setDraft('')
      setDraftInternal(false)
      const c = await ticketsApi.listComments(ticketId)
      setComments(c)
    } catch {
      toast.error('Failed to add comment')
    } finally {
      setPosting(false)
    }
  }

  if (loading) {
    return (
      <AuthGuard>
        <DashboardShell>
          <p className="text-[13px] text-white/45">Loading ticket…</p>
        </DashboardShell>
      </AuthGuard>
    )
  }

  if (!ticket) {
    return (
      <AuthGuard>
        <DashboardShell>
          <EmptyState
            title="Ticket not found."
            action={
              <Button size="sm" onClick={() => router.push('/tickets')}>
                <ArrowLeft className="w-3.5 h-3.5 mr-1.5" />
                Back to tickets
              </Button>
            }
          />
        </DashboardShell>
      </AuthGuard>
    )
  }

  const userById = (id: string) =>
    users.find((u) => u.id === id)?.name || users.find((u) => u.id === id)?.email || id.slice(0, 8)

  return (
    <AuthGuard>
      <DashboardShell>
        <PageHeader
          breadcrumbs={[
            { href: '/tickets', label: 'Tickets' },
            { label: ticket.title },
          ]}
          title={ticket.title}
          description={`Opened ${new Date(ticket.created_at * 1000).toLocaleString()}`}
          actions={
            <div className="flex items-center gap-1.5">
              <Pill tone={severityTone(ticket.priority)}>{ticket.priority}</Pill>
              <Pill tone={statusTone(ticket.status)}>{ticket.status}</Pill>
            </div>
          }
        />

        <div className="grid grid-cols-1 lg:grid-cols-3 gap-5 mb-6">
          <Section title="Description" className="lg:col-span-2 mb-0">
            <div className="border border-white/[0.06] rounded-lg bg-white/[0.01] px-4 py-3">
              <p className="text-[13px] text-white/85 whitespace-pre-wrap leading-relaxed">
                {ticket.description || <span className="text-white/35 italic">no description</span>}
              </p>
            </div>
          </Section>

          <Section title="Properties" className="mb-0">
            <div className="border border-white/[0.06] rounded-lg bg-white/[0.01] divide-y divide-white/[0.04]">
              <div className="px-3 py-2.5">
                <p className="text-[10.5px] uppercase tracking-[0.12em] text-white/40 mb-1.5">Status</p>
                <select
                  value={ticket.status}
                  onChange={(e) => updateField({ status: e.target.value as Ticket['status'] })}
                  disabled={!isAdmin || savingField === 'status'}
                  className={`w-full ${inputCls} disabled:opacity-50`}
                >
                  {STATUS_OPTIONS.map((s) => (
                    <option key={s} value={s}>{s}</option>
                  ))}
                </select>
              </div>
              <div className="px-3 py-2.5">
                <p className="text-[10.5px] uppercase tracking-[0.12em] text-white/40 mb-1.5">Priority</p>
                <select
                  value={ticket.priority}
                  onChange={(e) => updateField({ priority: e.target.value as Ticket['priority'] })}
                  disabled={!isAdmin || savingField === 'priority'}
                  className={`w-full ${inputCls} disabled:opacity-50`}
                >
                  {PRIORITY_OPTIONS.map((s) => (
                    <option key={s} value={s}>{s}</option>
                  ))}
                </select>
              </div>
              {isAdmin && (
                <div className="px-3 py-2.5">
                  <p className="text-[10.5px] uppercase tracking-[0.12em] text-white/40 mb-1.5">Assigned to</p>
                  <select
                    value={ticket.assigned_to || ''}
                    onChange={(e) => updateField({ assigned_to: e.target.value })}
                    disabled={savingField === 'assigned_to'}
                    className={`w-full ${inputCls} disabled:opacity-50`}
                  >
                    <option value="">unassigned</option>
                    {users.map((u) => (
                      <option key={u.id} value={u.id}>{u.name || u.email}</option>
                    ))}
                  </select>
                </div>
              )}
              {ticket.device_id && (
                <div className="px-3 py-2.5">
                  <p className="text-[10.5px] uppercase tracking-[0.12em] text-white/40 mb-1.5">Device</p>
                  <Link
                    href={`/devices/${ticket.device_id}`}
                    className="text-[12.5px] text-cyan-400 hover:text-cyan-300 font-mono"
                  >
                    {ticket.device_name || ticket.device_id.slice(0, 8)}
                  </Link>
                </div>
              )}
              <div className="px-3 py-2.5">
                <p className="text-[10.5px] uppercase tracking-[0.12em] text-white/40 mb-1.5">Category</p>
                <Code>{ticket.category}</Code>
              </div>
            </div>
          </Section>
        </div>

        {isAdmin && (
          <Section
            title="Time tracking"
            description={
              `${Math.floor(totalMinutes / 60)}h ${totalMinutes % 60}m logged` +
              (billableMinutes !== totalMinutes
                ? ` · ${Math.floor(billableMinutes / 60)}h ${billableMinutes % 60}m billable`
                : '')
            }
          >
            <div className="border border-white/[0.06] rounded-lg bg-white/[0.01] px-4 py-3 space-y-3">
              <div className="flex items-center gap-2 flex-wrap">
                <Clock className="w-3.5 h-3.5 text-white/45" />
                <input
                  type="number"
                  min={1}
                  max={1440}
                  value={timeForm.minutes}
                  onChange={(e) => setTimeForm({ ...timeForm, minutes: parseInt(e.target.value) || 0 })}
                  className={`w-20 ${inputCls}`}
                />
                <span className="text-[11.5px] text-white/45">min</span>
                <input
                  type="text"
                  placeholder="note (optional)"
                  value={timeForm.note}
                  onChange={(e) => setTimeForm({ ...timeForm, note: e.target.value })}
                  maxLength={1024}
                  className={`flex-1 min-w-[120px] ${inputCls}`}
                />
                <label className="flex items-center gap-1.5 text-[11.5px] text-white/55 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={timeForm.billable}
                    onChange={(e) => setTimeForm({ ...timeForm, billable: e.target.checked })}
                    className="rounded bg-white/[0.04] border-white/[0.12]"
                  />
                  billable
                </label>
                <Button size="sm" onClick={logTime} disabled={loggingTime}>
                  {loggingTime ? 'Saving…' : 'Log'}
                </Button>
              </div>
              {entries.length > 0 && (
                <ul className="divide-y divide-white/[0.04] -mx-4">
                  {entries.map((e) => (
                    <li key={e.id} className="px-4 py-2 flex items-center gap-3 text-[12px]">
                      <div className="min-w-0 flex-1">
                        <p className="text-white/85">
                          {Math.floor(e.minutes / 60) > 0 && `${Math.floor(e.minutes / 60)}h `}
                          {e.minutes % 60}m
                          {!e.billable && <span className="text-white/35"> · non-billable</span>}
                          {e.note && <span className="text-white/55"> — {e.note}</span>}
                        </p>
                        <p className="text-[11px] text-white/35 mt-0.5">
                          {userById(e.user_id)} · {new Date(e.started_at * 1000).toLocaleString()}
                        </p>
                      </div>
                      <button
                        onClick={() => setConfirmEntryDelete(e.id)}
                        className="text-white/30 hover:text-rose-300 p-1"
                        aria-label="Delete entry"
                      >
                        <Trash2 className="w-3 h-3" />
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </Section>
        )}

        <Section
          title="Activity"
          description={`${comments.length} ${comments.length === 1 ? 'comment' : 'comments'}`}
          className="mb-0"
        >
          <div className="space-y-3">
            {comments.length === 0 ? (
              <EmptyState title="No comments yet." />
            ) : (
              <ul className="space-y-2">
                {comments.map((c) => (
                  <li
                    key={c.id}
                    className={`px-4 py-3 rounded-lg border ${
                      c.internal
                        ? 'border-amber-500/15 bg-amber-500/[0.03]'
                        : 'border-white/[0.06] bg-white/[0.01]'
                    }`}
                  >
                    <div className="flex items-center gap-2 text-[11.5px] text-white/45">
                      <span className="font-medium text-white/85">{userById(c.user_id)}</span>
                      {c.internal && (
                        <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] text-amber-300/85 bg-amber-500/10 border border-amber-500/20">
                          <Lock className="w-2.5 h-2.5" />
                          internal
                        </span>
                      )}
                      <span className="text-white/30">{new Date(c.created_at * 1000).toLocaleString()}</span>
                    </div>
                    <p className="text-[13px] text-white/85 mt-2 whitespace-pre-wrap leading-relaxed">{c.body}</p>
                  </li>
                ))}
              </ul>
            )}

            <div className="border-t border-white/[0.06] pt-4 space-y-2">
              <textarea
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
                placeholder="Add a comment…"
                rows={3}
                maxLength={MAX_COMMENT_BYTES}
                className="w-full bg-white/[0.04] border border-white/[0.08] rounded-md px-3 py-2 text-[13px] text-white placeholder:text-white/30 focus:outline-none focus:border-white/[0.2]"
              />
              <div className="flex items-center justify-between gap-2 flex-wrap">
                {isAdmin && (
                  <label className="flex items-center gap-1.5 text-[11.5px] text-white/55 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={draftInternal}
                      onChange={(e) => setDraftInternal(e.target.checked)}
                      className="rounded bg-white/[0.04] border-white/[0.12]"
                    />
                    {draftInternal ? <Lock className="w-3 h-3" /> : <Unlock className="w-3 h-3" />}
                    Internal note (staff only)
                  </label>
                )}
                <span className="text-[11px] text-white/30 ml-auto tabular-nums">
                  {draft.length} / {MAX_COMMENT_BYTES}
                </span>
                <Button size="sm" onClick={post} disabled={posting || !draft.trim()}>
                  <Send className="w-3.5 h-3.5 mr-1.5" />
                  {posting ? 'Posting…' : 'Post'}
                </Button>
              </div>
            </div>
          </div>
        </Section>

        <ConfirmDialog
          open={!!confirmEntryDelete}
          onClose={() => setConfirmEntryDelete(null)}
          onConfirm={() => confirmEntryDelete && void removeEntry(confirmEntryDelete)}
          title="Delete time entry?"
          description="The entry is removed from this ticket's billable record."
          confirmLabel="Delete"
        />
      </DashboardShell>
    </AuthGuard>
  )
}
