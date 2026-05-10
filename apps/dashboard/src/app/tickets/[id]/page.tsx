'use client'

import { useEffect, useState } from 'react'
import { useParams, useRouter } from 'next/navigation'
import Link from 'next/link'
import { toast } from 'sonner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
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

const MAX_COMMENT_BYTES = 32 * 1024

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

  const loadAll = async () => {
    setLoading(true)
    try {
      const [t, c] = await Promise.all([
        ticketsApi.get(ticketId),
        ticketsApi.listComments(ticketId),
      ])
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
      toast.error('Update failed (admin only?)')
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
    if (!confirm('Delete this time entry?')) return
    try {
      await timeEntriesApi.remove(id)
      setEntries((prev) => prev.filter((e) => e.id !== id))
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
          <p className="text-center text-slate-400 py-12">Loading…</p>
        </DashboardShell>
      </AuthGuard>
    )
  }

  if (!ticket) {
    return (
      <AuthGuard>
        <DashboardShell>
          <div className="text-center py-12">
            <p className="text-slate-400 mb-4">Ticket not found</p>
            <Button onClick={() => router.push('/tickets')}>
              <ArrowLeft className="w-4 h-4 mr-2" />
              Back to Tickets
            </Button>
          </div>
        </DashboardShell>
      </AuthGuard>
    )
  }

  const userById = (id: string) => users.find((u) => u.id === id)?.name || users.find((u) => u.id === id)?.email || id.slice(0, 8)

  return (
    <AuthGuard>
      <DashboardShell>
        <div className="space-y-6 max-w-4xl">
          <div className="flex items-center justify-between gap-3">
            <div className="min-w-0">
              <Link href="/tickets" className="text-xs text-slate-400 hover:text-white">← Tickets</Link>
              <h1 className="text-2xl font-bold mt-1 truncate">{ticket.title}</h1>
              <div className="flex items-center gap-2 flex-wrap mt-2">
                <span className={`px-2 py-0.5 rounded border text-xs ${priorityClass[ticket.priority] ?? priorityClass.medium}`}>
                  {ticket.priority}
                </span>
                <span className={`px-2 py-0.5 rounded border text-xs ${statusClass[ticket.status] ?? statusClass.open}`}>
                  {ticket.status}
                </span>
                <span className="text-xs text-slate-500">
                  opened {new Date(ticket.created_at * 1000).toLocaleString()}
                </span>
              </div>
            </div>
          </div>

          <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
            <Card className="lg:col-span-2 bg-slate-900/60 border-slate-800/50">
              <CardHeader className="pb-3">
                <CardTitle className="text-sm font-medium text-slate-300">Description</CardTitle>
              </CardHeader>
              <CardContent>
                <p className="text-sm text-slate-300 whitespace-pre-wrap">
                  {ticket.description || <span className="text-slate-500 italic">no description</span>}
                </p>
              </CardContent>
            </Card>

            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardHeader className="pb-3">
                <CardTitle className="text-sm font-medium text-slate-300">Properties</CardTitle>
              </CardHeader>
              <CardContent className="space-y-3 text-sm">
                <div>
                  <p className="text-xs text-slate-400 mb-1">Status</p>
                  <select
                    value={ticket.status}
                    onChange={(e) => updateField({ status: e.target.value as Ticket['status'] })}
                    disabled={!isAdmin || savingField === 'status'}
                    className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-2 py-1 text-xs disabled:opacity-50"
                  >
                    {STATUS_OPTIONS.map((s) => <option key={s} value={s}>{s}</option>)}
                  </select>
                </div>
                <div>
                  <p className="text-xs text-slate-400 mb-1">Priority</p>
                  <select
                    value={ticket.priority}
                    onChange={(e) => updateField({ priority: e.target.value as Ticket['priority'] })}
                    disabled={!isAdmin || savingField === 'priority'}
                    className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-2 py-1 text-xs disabled:opacity-50"
                  >
                    {PRIORITY_OPTIONS.map((s) => <option key={s} value={s}>{s}</option>)}
                  </select>
                </div>
                {isAdmin && (
                  <div>
                    <p className="text-xs text-slate-400 mb-1">Assigned to</p>
                    <select
                      value={ticket.assigned_to || ''}
                      onChange={(e) => updateField({ assigned_to: e.target.value })}
                      disabled={savingField === 'assigned_to'}
                      className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-2 py-1 text-xs disabled:opacity-50"
                    >
                      <option value="">unassigned</option>
                      {users.map((u) => <option key={u.id} value={u.id}>{u.name || u.email}</option>)}
                    </select>
                  </div>
                )}
                {ticket.device_id && (
                  <div>
                    <p className="text-xs text-slate-400 mb-1">Device</p>
                    <Link href={`/devices/${ticket.device_id}`} className="text-xs text-blue-400 hover:underline font-mono">
                      {ticket.device_name || ticket.device_id.slice(0, 8)}
                    </Link>
                  </div>
                )}
                <div>
                  <p className="text-xs text-slate-400 mb-1">Category</p>
                  <p className="text-xs text-slate-300">{ticket.category}</p>
                </div>
              </CardContent>
            </Card>
          </div>

          {isAdmin && (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardHeader className="pb-3 flex flex-row items-center justify-between">
                <CardTitle className="text-sm font-medium text-slate-300 flex items-center gap-2">
                  <Clock className="w-4 h-4" />
                  Time tracking
                </CardTitle>
                <span className="text-xs text-slate-500">
                  {Math.floor(totalMinutes / 60)}h {totalMinutes % 60}m logged
                  {billableMinutes !== totalMinutes && <> · {Math.floor(billableMinutes / 60)}h {billableMinutes % 60}m billable</>}
                </span>
              </CardHeader>
              <CardContent className="space-y-3">
                <div className="flex items-center gap-2 flex-wrap">
                  <input
                    type="number"
                    min={1}
                    max={1440}
                    value={timeForm.minutes}
                    onChange={(e) => setTimeForm({ ...timeForm, minutes: parseInt(e.target.value) || 0 })}
                    className="w-20 bg-slate-800/50 border border-slate-700/50 rounded-md px-2 py-1.5 text-sm"
                  />
                  <span className="text-xs text-slate-400">min</span>
                  <input
                    type="text"
                    placeholder="note (optional)"
                    value={timeForm.note}
                    onChange={(e) => setTimeForm({ ...timeForm, note: e.target.value })}
                    maxLength={1024}
                    className="flex-1 min-w-[120px] bg-slate-800/50 border border-slate-700/50 rounded-md px-2 py-1.5 text-sm"
                  />
                  <label className="flex items-center gap-1 text-xs text-slate-400 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={timeForm.billable}
                      onChange={(e) => setTimeForm({ ...timeForm, billable: e.target.checked })}
                      className="rounded border-slate-600 bg-slate-800"
                    />
                    billable
                  </label>
                  <Button size="sm" onClick={logTime} disabled={loggingTime}>
                    {loggingTime ? 'Saving…' : 'Log'}
                  </Button>
                </div>
                {entries.length > 0 && (
                  <div className="divide-y divide-slate-800/50">
                    {entries.map((e) => (
                      <div key={e.id} className="py-2 flex items-center justify-between text-xs">
                        <div className="min-w-0 flex-1">
                          <p className="text-slate-200">
                            {Math.floor(e.minutes / 60) > 0 && `${Math.floor(e.minutes / 60)}h `}{e.minutes % 60}m
                            {!e.billable && <span className="text-slate-500"> · non-billable</span>}
                            {e.note && <span className="text-slate-400"> — {e.note}</span>}
                          </p>
                          <p className="text-slate-500">{userById(e.user_id)} · {new Date(e.started_at * 1000).toLocaleString()}</p>
                        </div>
                        <button onClick={() => removeEntry(e.id)} className="text-rose-400 hover:text-rose-300 p-1">
                          <Trash2 className="w-3 h-3" />
                        </button>
                      </div>
                    ))}
                  </div>
                )}
              </CardContent>
            </Card>
          )}

          <Card className="bg-slate-900/60 border-slate-800/50">
            <CardHeader className="pb-3">
              <CardTitle className="text-sm font-medium text-slate-300">
                Activity ({comments.length} comment{comments.length === 1 ? '' : 's'})
              </CardTitle>
            </CardHeader>
            <CardContent className="space-y-4">
              {comments.length === 0 ? (
                <p className="text-sm text-slate-500 text-center py-6">No comments yet.</p>
              ) : (
                <div className="space-y-3">
                  {comments.map((c) => (
                    <div key={c.id} className={`p-3 rounded-lg border ${c.internal ? 'border-amber-500/20 bg-amber-500/5' : 'border-slate-800/50 bg-slate-800/30'}`}>
                      <div className="flex items-center gap-2 text-xs text-slate-400">
                        <span className="font-medium text-slate-300">{userById(c.user_id)}</span>
                        {c.internal && (
                          <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded border border-amber-500/30 bg-amber-500/10 text-amber-300">
                            <Lock className="w-3 h-3" />
                            internal
                          </span>
                        )}
                        <span className="text-slate-500">{new Date(c.created_at * 1000).toLocaleString()}</span>
                      </div>
                      <p className="text-sm text-slate-200 mt-2 whitespace-pre-wrap">{c.body}</p>
                    </div>
                  ))}
                </div>
              )}

              <div className="border-t border-slate-800/50 pt-4 space-y-2">
                <textarea
                  value={draft}
                  onChange={(e) => setDraft(e.target.value)}
                  placeholder="Add a comment…"
                  rows={3}
                  maxLength={MAX_COMMENT_BYTES}
                  className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm text-white placeholder:text-slate-500 focus:outline-none focus:ring-2 focus:ring-blue-500/50"
                />
                <div className="flex items-center justify-between gap-2 flex-wrap">
                  {isAdmin && (
                    <label className="flex items-center gap-2 text-xs text-slate-400 cursor-pointer">
                      <input
                        type="checkbox"
                        checked={draftInternal}
                        onChange={(e) => setDraftInternal(e.target.checked)}
                        className="rounded border-slate-600 bg-slate-800"
                      />
                      {draftInternal ? <Lock className="w-3 h-3" /> : <Unlock className="w-3 h-3" />}
                      Internal note (staff only)
                    </label>
                  )}
                  <span className="text-xs text-slate-500 ml-auto">{draft.length} / {MAX_COMMENT_BYTES}</span>
                  <Button onClick={post} disabled={posting || !draft.trim()}>
                    <Send className="w-4 h-4 mr-1" />
                    {posting ? 'Posting…' : 'Post'}
                  </Button>
                </div>
              </div>
            </CardContent>
          </Card>
        </div>
      </DashboardShell>
    </AuthGuard>
  )
}
