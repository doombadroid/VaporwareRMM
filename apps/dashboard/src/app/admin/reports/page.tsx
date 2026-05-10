'use client'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { reportsApi, type ReportSchedule } from '@/lib/api'

const REPORT_TYPES = ['fleet_status', 'sla_monthly', 'patch_compliance', 'ticket_volume', 'billing_hours']
const DOW = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']
const TZ = ['UTC', 'America/New_York', 'America/Chicago', 'America/Los_Angeles', 'Europe/London', 'Europe/Paris', 'Asia/Tokyo', 'Australia/Sydney']

export default function ReportsPage() {
  const [schedules, setSchedules] = useState<ReportSchedule[]>([])
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
  const [creating, setCreating] = useState(false)
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
    try { setSchedules(await reportsApi.list()) }
    catch { toast.error('Failed to load (admin only?)') }
    finally { setLoading(false) }
  }
  useEffect(() => { void load() }, [])

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
    } finally { setCreating(false) }
  }

  const remove = async (s: ReportSchedule) => {
    if (!confirm(`Delete ${s.name}?`)) return
    try { await reportsApi.remove(s.id); setSchedules((p) => p.filter((x) => x.id !== s.id)) }
    catch { toast.error('Delete failed') }
  }

  const runNow = async (s: ReportSchedule) => {
    try {
      const blob = await reportsApi.run(s.id) as Blob
      const url = window.URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `${s.report_type}-${new Date().toISOString().slice(0, 10)}.csv`
      a.click()
      window.URL.revokeObjectURL(url)
    } catch { toast.error('Run failed') }
  }

  return (
    <AuthGuard>
      <DashboardShell>
        <div className="max-w-4xl space-y-6">
          <div className="flex items-center justify-between">
            <h1 className="text-2xl font-bold">Scheduled reports</h1>
            <Button onClick={() => setShowCreate((s) => !s)}>{showCreate ? 'Cancel' : 'New schedule'}</Button>
          </div>
          {showCreate && (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardContent className="space-y-3 py-4">
                <input type="text" placeholder="Name (e.g. Weekly fleet status)" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm" />
                <div className="grid grid-cols-2 gap-3">
                  <select value={form.report_type} onChange={(e) => setForm({ ...form, report_type: e.target.value })} className="bg-slate-800/50 border border-slate-700/50 rounded-md px-2 py-2 text-sm">
                    {REPORT_TYPES.map((t) => <option key={t} value={t}>{t}</option>)}
                  </select>
                  <select value={form.dow} onChange={(e) => setForm({ ...form, dow: parseInt(e.target.value) })} className="bg-slate-800/50 border border-slate-700/50 rounded-md px-2 py-2 text-sm">
                    {DOW.map((d, i) => <option key={i} value={i}>{d}</option>)}
                  </select>
                  <input type="number" min={0} max={23} value={form.hour} onChange={(e) => setForm({ ...form, hour: parseInt(e.target.value) || 0 })} className="bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm" placeholder="hour" />
                  <input type="number" min={0} max={59} value={form.minute} onChange={(e) => setForm({ ...form, minute: parseInt(e.target.value) || 0 })} className="bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm" placeholder="minute" />
                  <select value={form.timezone} onChange={(e) => setForm({ ...form, timezone: e.target.value })} className="bg-slate-800/50 border border-slate-700/50 rounded-md px-2 py-2 text-sm">
                    {TZ.map((t) => <option key={t} value={t}>{t}</option>)}
                  </select>
                  <input type="text" placeholder="emails comma-separated" value={form.email_recipients} onChange={(e) => setForm({ ...form, email_recipients: e.target.value })} className="bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm" />
                </div>
                <div className="flex justify-end">
                  <Button onClick={create} disabled={creating}>{creating ? 'Saving…' : 'Create'}</Button>
                </div>
              </CardContent>
            </Card>
          )}
          {loading ? (
            <Card className="bg-slate-900/60 border-slate-800/50"><CardContent className="py-12 text-center text-slate-400">Loading…</CardContent></Card>
          ) : schedules.length === 0 ? (
            <Card className="bg-slate-900/60 border-slate-800/50"><CardContent className="py-12 text-center text-slate-400">No scheduled reports.</CardContent></Card>
          ) : (
            <div className="grid gap-3">
              {schedules.map((s) => (
                <Card key={s.id} className="bg-slate-900/60 border-slate-800/50">
                  <CardContent className="py-4 flex items-start justify-between gap-3">
                    <div className="flex-1 min-w-0">
                      <p className="font-medium text-white truncate">{s.name}</p>
                      <p className="text-xs text-slate-400 mt-1 font-mono">{s.report_type} · cron &ldquo;{s.weekly_cron}&rdquo; {s.timezone}</p>
                      <p className="text-xs text-slate-500 mt-1 truncate">→ {s.email_recipients}</p>
                      {s.last_error && <p className="text-xs text-rose-400 mt-1">{s.last_error}</p>}
                    </div>
                    <div className="flex flex-col gap-1 shrink-0">
                      <Button size="sm" variant="ghost" onClick={() => runNow(s)}>Run now</Button>
                      <Button size="sm" variant="outline" className="border-red-500/30 text-red-400 hover:bg-red-500/10" onClick={() => remove(s)}>Delete</Button>
                    </div>
                  </CardContent>
                </Card>
              ))}
            </div>
          )}
        </div>
      </DashboardShell>
    </AuthGuard>
  )
}
