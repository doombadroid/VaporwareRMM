'use client'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { PageHeader, EmptyState } from '@/components/ui/page'
import { Sheet, ConfirmDialog } from '@/components/ui/sheet'
import { Code } from '@/components/ui/status'
import { Button } from '@/components/ui/button'
import { Plus } from 'lucide-react'
import { reportsApi, type ReportSchedule } from '@/lib/api'

const REPORT_TYPES = ['fleet_status', 'sla_monthly', 'patch_compliance', 'ticket_volume', 'billing_hours']
const DOW = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']
const TZ = ['UTC', 'America/New_York', 'America/Chicago', 'America/Los_Angeles', 'Europe/London', 'Europe/Paris', 'Asia/Tokyo', 'Australia/Sydney']

const inputCls = 'bg-white/[0.04] border border-white/[0.08] rounded-md px-3 py-1.5 text-[13px] text-white placeholder:text-white/30 focus:outline-none focus:border-white/[0.2]'
const labelCls = 'block text-[11px] uppercase tracking-[0.12em] text-white/40 mb-1.5'

export default function ReportsPage() {
  const [schedules, setSchedules] = useState<ReportSchedule[]>([])
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
  const [creating, setCreating] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState<ReportSchedule | null>(null)
  const [form, setForm] = useState({
    name: '',
    report_type: 'fleet_status',
    dow: 1,
    hour: 6,
    minute: 0,
    timezone: 'UTC',
    email_recipients: '',
  })

  const load = async () => {
    setLoading(true)
    try {
      setSchedules(await reportsApi.list())
    } catch {
      toast.error('Failed to load')
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    void load()
  }, [])

  const create = async () => {
    if (!form.name || !form.email_recipients) {
      toast.error('Fill name + recipients')
      return
    }
    setCreating(true)
    try {
      await reportsApi.create({
        name: form.name,
        report_type: form.report_type,
        weekly_cron: `${form.minute} ${form.hour} ${form.dow}`,
        timezone: form.timezone,
        email_recipients: form.email_recipients.split(',').map((s) => s.trim()).filter(Boolean),
      })
      toast.success('Schedule created')
      setShowCreate(false)
      setForm({ name: '', report_type: 'fleet_status', dow: 1, hour: 6, minute: 0, timezone: 'UTC', email_recipients: '' })
      await load()
    } catch (e: unknown) {
      const msg = (e as { response?: { data?: { error?: string } } })?.response?.data?.error || 'Save failed'
      toast.error(msg)
    } finally {
      setCreating(false)
    }
  }

  const remove = async (s: ReportSchedule) => {
    try {
      await reportsApi.remove(s.id)
      setSchedules((p) => p.filter((x) => x.id !== s.id))
      setConfirmDelete(null)
    } catch {
      toast.error('Delete failed')
    }
  }

  const runNow = async (s: ReportSchedule) => {
    try {
      const blob = (await reportsApi.run(s.id)) as Blob
      const url = window.URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `${s.report_type}-${new Date().toISOString().slice(0, 10)}.csv`
      a.click()
      window.URL.revokeObjectURL(url)
    } catch {
      toast.error('Run failed')
    }
  }

  return (
    <AuthGuard>
      <DashboardShell>
        <PageHeader
          eyebrow="Audit"
          title="Scheduled reports"
          description="Recurring CSV exports emailed to stakeholders."
          actions={
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus className="w-3.5 h-3.5 mr-1.5" />
              New schedule
            </Button>
          }
        />

        {loading ? (
          <p className="text-[13px] text-white/45">Loading…</p>
        ) : schedules.length === 0 ? (
          <EmptyState title="No scheduled reports." hint="Create a schedule to email a recurring CSV." />
        ) : (
          <ul className="border border-white/[0.06] rounded-lg overflow-hidden divide-y divide-white/[0.04] bg-white/[0.01]">
            {schedules.map((s) => (
              <li key={s.id} className="flex items-start gap-3 px-4 py-3 hover:bg-white/[0.02]">
                <div className="flex-1 min-w-0">
                  <p className="text-[13.5px] font-medium text-white truncate">{s.name}</p>
                  <p className="text-[11.5px] text-white/55 mt-1">
                    <Code>{s.report_type}</Code>
                    <span className="mx-1.5">·</span>
                    cron <Code>{s.weekly_cron}</Code> {s.timezone}
                  </p>
                  <p className="text-[11px] text-white/40 mt-1 truncate">→ {s.email_recipients}</p>
                  {s.last_error && <p className="text-[11px] text-rose-300/85 mt-1">{s.last_error}</p>}
                </div>
                <div className="flex items-center gap-1 shrink-0">
                  <Button size="sm" variant="ghost" onClick={() => runNow(s)}>
                    Run now
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => setConfirmDelete(s)}>
                    Delete
                  </Button>
                </div>
              </li>
            ))}
          </ul>
        )}

        <Sheet
          open={showCreate}
          onClose={() => setShowCreate(false)}
          title="New report schedule"
          footer={
            <>
              <Button variant="ghost" size="sm" onClick={() => setShowCreate(false)}>
                Cancel
              </Button>
              <Button size="sm" onClick={create} disabled={creating}>
                {creating ? 'Saving…' : 'Create'}
              </Button>
            </>
          }
        >
          <div className="space-y-4">
            <div>
              <label className={labelCls}>Name</label>
              <input
                placeholder="e.g. Weekly fleet status"
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                className={`w-full ${inputCls}`}
              />
            </div>
            <div>
              <label className={labelCls}>Report type</label>
              <select value={form.report_type} onChange={(e) => setForm({ ...form, report_type: e.target.value })} className={`w-full ${inputCls}`}>
                {REPORT_TYPES.map((t) => (
                  <option key={t} value={t}>{t}</option>
                ))}
              </select>
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className={labelCls}>Day of week</label>
                <select value={form.dow} onChange={(e) => setForm({ ...form, dow: parseInt(e.target.value) })} className={`w-full ${inputCls}`}>
                  {DOW.map((d, i) => (
                    <option key={i} value={i}>{d}</option>
                  ))}
                </select>
              </div>
              <div>
                <label className={labelCls}>Timezone</label>
                <select value={form.timezone} onChange={(e) => setForm({ ...form, timezone: e.target.value })} className={`w-full ${inputCls}`}>
                  {TZ.map((t) => (
                    <option key={t} value={t}>{t}</option>
                  ))}
                </select>
              </div>
              <div>
                <label className={labelCls}>Hour (0-23)</label>
                <input type="number" min={0} max={23} value={form.hour} onChange={(e) => setForm({ ...form, hour: parseInt(e.target.value) || 0 })} className={`w-full ${inputCls}`} />
              </div>
              <div>
                <label className={labelCls}>Minute (0-59)</label>
                <input type="number" min={0} max={59} value={form.minute} onChange={(e) => setForm({ ...form, minute: parseInt(e.target.value) || 0 })} className={`w-full ${inputCls}`} />
              </div>
            </div>
            <div>
              <label className={labelCls}>Email recipients (comma-separated)</label>
              <input
                value={form.email_recipients}
                onChange={(e) => setForm({ ...form, email_recipients: e.target.value })}
                className={`w-full ${inputCls}`}
              />
            </div>
          </div>
        </Sheet>

        <ConfirmDialog
          open={!!confirmDelete}
          onClose={() => setConfirmDelete(null)}
          onConfirm={() => confirmDelete && void remove(confirmDelete)}
          title="Delete schedule?"
          description={`Stops emailing ${confirmDelete?.name || ''}.`}
          confirmLabel="Delete"
        />
      </DashboardShell>
    </AuthGuard>
  )
}
