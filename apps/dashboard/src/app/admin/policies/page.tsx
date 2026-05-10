'use client'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { PageHeader, Section } from '@/components/ui/page'
import { Button } from '@/components/ui/button'
import { policiesApi, type TenantPolicy } from '@/lib/api'

const inputCls = 'bg-white/[0.04] border border-white/[0.08] rounded-md px-3 py-1.5 text-[13px] text-white placeholder:text-white/30 focus:outline-none focus:border-white/[0.2]'
const labelCls = 'block text-[11px] uppercase tracking-[0.12em] text-white/40 mb-1.5'

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
    try {
      setPolicy(await policiesApi.get())
    } catch {
      toast.error('Failed to load')
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    void load()
  }, [])

  const save = async () => {
    setSaving(true)
    try {
      const updated = await policiesApi.save(policy)
      setPolicy(updated)
      toast.success('Saved')
    } catch (e: unknown) {
      const msg = (e as { response?: { data?: { error?: string } } })?.response?.data?.error || 'Save failed'
      toast.error(msg)
    } finally {
      setSaving(false)
    }
  }

  const num = (k: keyof TenantPolicy, label: string, hint: string, min = 0) => (
    <div>
      <label className={labelCls}>{label}</label>
      <input
        type="number"
        min={min}
        value={policy[k]}
        onChange={(e) => setPolicy({ ...policy, [k]: parseInt(e.target.value) || 0 })}
        className={`w-full ${inputCls}`}
      />
      <p className="text-[11px] text-white/40 mt-1">{hint}</p>
    </div>
  )

  return (
    <AuthGuard>
      <DashboardShell>
        <PageHeader
          eyebrow="System"
          title="Tenant policies"
          description="Retention, lockouts, and per-tenant defaults."
          actions={
            <Button size="sm" onClick={save} disabled={saving || loading}>
              {saving ? 'Saving…' : 'Save'}
            </Button>
          }
        />

        {loading ? (
          <p className="text-[13px] text-white/45">Loading…</p>
        ) : (
          <div className="max-w-2xl space-y-6">
            <Section title="Retention" className="mb-0">
              <div className="border border-white/[0.06] rounded-lg bg-white/[0.01] p-4 space-y-4">
                {num('audit_retention_days', 'Audit logs (days)', 'minimum 30 — compliance floor', 30)}
                {num('metrics_retention_days', 'Metrics history (days)', 'minimum 7 days', 7)}
                {num('ticket_comment_retention_days', 'Ticket comments (days)', '0 = forever', 0)}
                {num('time_entry_retention_days', 'Billing time entries (days)', '0 = forever (recommended)', 0)}
              </div>
            </Section>
            <Section title="Lockout" className="mb-0">
              <div className="border border-white/[0.06] rounded-lg bg-white/[0.01] p-4 space-y-4">
                {num('failed_login_threshold', 'Failed login threshold', 'lockout triggers after N consecutive failures (3-100)', 3)}
                {num('lockout_minutes', 'Lockout duration (minutes)', 'how long the account stays locked', 1)}
              </div>
            </Section>
          </div>
        )}
      </DashboardShell>
    </AuthGuard>
  )
}
