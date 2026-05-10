'use client'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { policiesApi, type TenantPolicy } from '@/lib/api'

export default function PoliciesPage() {
  const [policy, setPolicy] = useState<TenantPolicy>({
    audit_retention_days: 365,
    metrics_retention_days: 90,
    ticket_comment_retention_days: 0,
    time_entry_retention_days: 0,
    failed_login_threshold: 10,
    lockout_minutes: 15,
  })
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)

  const load = async () => {
    setLoading(true)
    try { setPolicy(await policiesApi.get()) }
    catch { toast.error('Failed to load (admin only?)') }
    finally { setLoading(false) }
  }
  useEffect(() => { void load() }, [])

  const save = async () => {
    setSaving(true)
    try {
      const updated = await policiesApi.save(policy)
      setPolicy(updated)
      toast.success('Saved')
    } catch (e: unknown) {
      const msg = (e as { response?: { data?: { error?: string } } })?.response?.data?.error || 'Save failed'
      toast.error(msg)
    } finally { setSaving(false) }
  }

  const num = (k: keyof TenantPolicy, label: string, hint: string, min = 0) => (
    <div>
      <label className="block text-xs text-slate-400 mb-1">{label}</label>
      <input
        type="number"
        min={min}
        value={policy[k]}
        onChange={(e) => setPolicy({ ...policy, [k]: parseInt(e.target.value) || 0 })}
        className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm"
      />
      <p className="text-xs text-slate-500 mt-1">{hint}</p>
    </div>
  )

  return (
    <AuthGuard>
      <DashboardShell>
        <div className="max-w-3xl space-y-6">
          <h1 className="text-2xl font-bold">Tenant policies</h1>
          {loading ? (
            <Card className="bg-slate-900/60 border-slate-800/50"><CardContent className="py-12 text-center text-slate-400">Loading…</CardContent></Card>
          ) : (
            <>
              <Card className="bg-slate-900/60 border-slate-800/50">
                <CardHeader className="pb-3"><CardTitle className="text-base">Retention</CardTitle></CardHeader>
                <CardContent className="space-y-3">
                  {num('audit_retention_days', 'Audit logs (days)', 'minimum 30 — compliance floor', 30)}
                  {num('metrics_retention_days', 'Metrics history (days)', 'minimum 7 days', 7)}
                  {num('ticket_comment_retention_days', 'Ticket comments (days)', '0 = forever', 0)}
                  {num('time_entry_retention_days', 'Billing time entries (days)', '0 = forever (recommended for compliance)', 0)}
                </CardContent>
              </Card>
              <Card className="bg-slate-900/60 border-slate-800/50">
                <CardHeader className="pb-3"><CardTitle className="text-base">Lockout</CardTitle></CardHeader>
                <CardContent className="space-y-3">
                  {num('failed_login_threshold', 'Failed login threshold', 'lockout triggers after N consecutive failures (3-100)', 3)}
                  {num('lockout_minutes', 'Lockout duration (minutes)', 'how long the account stays locked', 1)}
                </CardContent>
              </Card>
              <div className="flex justify-end">
                <Button onClick={save} disabled={saving}>{saving ? 'Saving…' : 'Save'}</Button>
              </div>
            </>
          )}
        </div>
      </DashboardShell>
    </AuthGuard>
  )
}
