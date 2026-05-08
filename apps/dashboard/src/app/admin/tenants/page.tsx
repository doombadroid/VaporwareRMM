'use client'

import { useEffect, useMemo, useState } from 'react'
import { useRouter } from 'next/navigation'
import { toast } from 'sonner'
import {
  Building2,
  Plus,
  RotateCw,
  Trash2,
  PauseCircle,
  PlayCircle,
  Copy,
  Check,
  X,
  Eye,
} from 'lucide-react'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { useCurrentUser } from '@/components/CurrentUserProvider'
import { tenantsApi, type Tenant, type CreateTenantRequest } from '@/lib/api'

function slugify(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .replace(/-{2,}/g, '-')
    .slice(0, 64)
}

function formatCount(count: number, max: number): string {
  if (max === 0) return String(count)
  return `${count} / ${max}`
}

function timeAgo(unix: number): string {
  const diff = Math.floor((Date.now() / 1000) - unix)
  if (diff < 60) return 'just now'
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`
  return `${Math.floor(diff / 86400)}d ago`
}

export default function TenantsAdminPage() {
  const router = useRouter()
  const { user, isLoading: userLoading } = useCurrentUser()
  const [tenants, setTenants] = useState<Tenant[]>([])
  const [loading, setLoading] = useState(true)
  const [createOpen, setCreateOpen] = useState(false)
  const [revealedSecret, setRevealedSecret] = useState<{
    tenantId: string
    tenantName: string
    secret: string
    commands?: { linux: string; macos: string; windows: string }
  } | null>(null)
  const [confirmDelete, setConfirmDelete] = useState<Tenant | null>(null)

  useEffect(() => {
    if (!userLoading && user && user.role !== 'super_admin') {
      router.replace('/')
    }
  }, [user, userLoading, router])

  const loadTenants = async () => {
    setLoading(true)
    try {
      const list = await tenantsApi.list()
      setTenants(list)
    } catch {
      toast.error('Failed to load tenants')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    if (user?.role === 'super_admin') loadTenants()
  }, [user?.role])

  const stats = useMemo(() => {
    const total = tenants.length
    const active = tenants.filter((t) => t.status === 'active').length
    const totalDevices = tenants.reduce((sum, t) => sum + t.device_count, 0)
    return { total, active, suspended: total - active, totalDevices }
  }, [tenants])

  if (userLoading || !user) {
    return (
      <AuthGuard>
        <DashboardShell>
          <div className="flex items-center justify-center h-[60vh] text-white/40 text-sm">
            Loading…
          </div>
        </DashboardShell>
      </AuthGuard>
    )
  }

  if (user.role !== 'super_admin') return null

  return (
    <AuthGuard>
      <DashboardShell>
        <div className="px-6 py-8 max-w-6xl mx-auto space-y-8">
          <header className="flex items-end justify-between gap-6">
            <div>
              <p className="text-[11px] uppercase tracking-[0.16em] text-amber-300/70 mb-2">
                Cross-tenant view
              </p>
              <h1 className="text-2xl font-semibold text-white">Tenants</h1>
              <p className="text-sm text-white/50 mt-1">
                {stats.total} {stats.total === 1 ? 'organization' : 'organizations'}
                {stats.suspended > 0 && (
                  <>
                    {' '}
                    <span className="text-amber-300/80">
                      ({stats.suspended} suspended)
                    </span>
                  </>
                )}
                {' · '}
                {stats.totalDevices} {stats.totalDevices === 1 ? 'device' : 'devices'} across all tenants
              </p>
            </div>
            <button
              onClick={() => setCreateOpen((v) => !v)}
              className="inline-flex items-center gap-2 rounded-lg bg-blue-600 hover:bg-blue-500 px-4 py-2 text-sm font-medium text-white transition-colors"
            >
              {createOpen ? (
                <>
                  <X className="w-4 h-4" /> Cancel
                </>
              ) : (
                <>
                  <Plus className="w-4 h-4" /> New tenant
                </>
              )}
            </button>
          </header>

          {revealedSecret && (
            <RegistrationSecretReveal
              tenantName={revealedSecret.tenantName}
              secret={revealedSecret.secret}
              commands={revealedSecret.commands}
              onDismiss={() => setRevealedSecret(null)}
            />
          )}

          {createOpen && (
            <CreateTenantForm
              onCancel={() => setCreateOpen(false)}
              onCreated={async (created) => {
                setCreateOpen(false)
                await loadTenants()
                toast.success(`Created tenant "${created.name}"`)
                // Auto-rotate to give them their first registration secret
                try {
                  const res = await tenantsApi.rotateRegistrationSecret(created.id)
                  setRevealedSecret({
                    tenantId: created.id,
                    tenantName: created.name,
                    secret: res.registration_secret,
                    commands: res.install_commands,
                  })
                } catch {
                  toast.error('Tenant created but registration secret generation failed')
                }
              }}
            />
          )}

          <TenantsTable
            tenants={tenants}
            loading={loading}
            onRefresh={loadTenants}
            onRotateSecret={async (t) => {
              try {
                const res = await tenantsApi.rotateRegistrationSecret(t.id)
                setRevealedSecret({
                  tenantId: t.id,
                  tenantName: t.name,
                  secret: res.registration_secret,
                  commands: res.install_commands,
                })
              } catch {
                toast.error('Failed to rotate registration secret')
              }
            }}
            onToggleStatus={async (t) => {
              const next = t.status === 'active' ? 'suspended' : 'active'
              try {
                await tenantsApi.update(t.id, { status: next })
                toast.success(`Tenant ${next === 'suspended' ? 'suspended' : 'reactivated'}`)
                loadTenants()
              } catch {
                toast.error('Failed to update tenant')
              }
            }}
            onImpersonate={async (t) => {
              if (!confirm(`Impersonate ${t.name}? You'll act as a tenant_admin in their tenant until you click "End impersonation".`)) return
              try {
                await tenantsApi.impersonate(t.id)
                window.location.href = '/'
              } catch {
                toast.error('Failed to start impersonation')
              }
            }}
            onDelete={(t) => setConfirmDelete(t)}
          />

          {confirmDelete && (
            <DeleteTenantDialog
              tenant={confirmDelete}
              onCancel={() => setConfirmDelete(null)}
              onConfirmed={async () => {
                try {
                  await tenantsApi.remove(confirmDelete.id)
                  toast.success(`Deleted tenant "${confirmDelete.name}"`)
                  setConfirmDelete(null)
                  loadTenants()
                } catch (e: any) {
                  toast.error(e?.response?.data?.error || 'Failed to delete tenant')
                }
              }}
            />
          )}
        </div>
      </DashboardShell>
    </AuthGuard>
  )
}

function CreateTenantForm({
  onCancel,
  onCreated,
}: {
  onCancel: () => void
  onCreated: (t: Tenant) => void
}) {
  const [name, setName] = useState('')
  const [slug, setSlug] = useState('')
  const [slugTouched, setSlugTouched] = useState(false)
  const [plan, setPlan] = useState('free')
  const [maxDevices, setMaxDevices] = useState('')
  const [maxUsers, setMaxUsers] = useState('')
  const [submitting, setSubmitting] = useState(false)

  const effectiveSlug = slugTouched ? slug : slugify(name)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!name.trim()) return
    setSubmitting(true)
    const payload: CreateTenantRequest = {
      name: name.trim(),
      slug: effectiveSlug || undefined,
      plan,
      max_devices: maxDevices ? parseInt(maxDevices, 10) : 0,
      max_users: maxUsers ? parseInt(maxUsers, 10) : 0,
    }
    try {
      const created = await tenantsApi.create(payload)
      onCreated(created)
    } catch (e: any) {
      toast.error(e?.response?.data?.error || 'Failed to create tenant')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <form
      onSubmit={submit}
      className="rounded-xl border border-white/10 bg-white/[0.02] p-6 animate-fadeInUp"
    >
      <div className="grid grid-cols-1 md:grid-cols-2 gap-5">
        <Field label="Name">
          <input
            autoFocus
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Acme Corp"
            className="w-full bg-[#0c0c12] border border-white/10 rounded-lg px-3 py-2 text-sm text-white placeholder:text-white/25 focus:outline-none focus:border-blue-500/50"
            required
          />
        </Field>
        <Field
          label="Slug"
          hint={effectiveSlug ? `URL fragment: ${effectiveSlug}` : 'auto from name'}
        >
          <input
            value={effectiveSlug}
            onChange={(e) => {
              setSlug(e.target.value)
              setSlugTouched(true)
            }}
            placeholder="acme-corp"
            pattern="[a-z0-9][a-z0-9-]{1,62}[a-z0-9]"
            className="w-full bg-[#0c0c12] border border-white/10 rounded-lg px-3 py-2 text-sm text-white font-mono placeholder:text-white/25 focus:outline-none focus:border-blue-500/50"
          />
        </Field>
        <Field label="Plan">
          <select
            value={plan}
            onChange={(e) => setPlan(e.target.value)}
            className="w-full bg-[#0c0c12] border border-white/10 rounded-lg px-3 py-2 text-sm text-white focus:outline-none focus:border-blue-500/50"
          >
            <option value="free">Free</option>
            <option value="pro">Pro</option>
            <option value="enterprise">Enterprise</option>
          </select>
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Max devices" hint="0 = unlimited">
            <input
              type="number"
              min="0"
              value={maxDevices}
              onChange={(e) => setMaxDevices(e.target.value)}
              placeholder="0"
              className="w-full bg-[#0c0c12] border border-white/10 rounded-lg px-3 py-2 text-sm text-white placeholder:text-white/25 focus:outline-none focus:border-blue-500/50"
            />
          </Field>
          <Field label="Max users" hint="0 = unlimited">
            <input
              type="number"
              min="0"
              value={maxUsers}
              onChange={(e) => setMaxUsers(e.target.value)}
              placeholder="0"
              className="w-full bg-[#0c0c12] border border-white/10 rounded-lg px-3 py-2 text-sm text-white placeholder:text-white/25 focus:outline-none focus:border-blue-500/50"
            />
          </Field>
        </div>
      </div>

      <div className="flex items-center justify-end gap-2 mt-6 pt-4 border-t border-white/[0.06]">
        <button
          type="button"
          onClick={onCancel}
          className="px-4 py-2 text-sm text-white/60 hover:text-white transition-colors"
        >
          Cancel
        </button>
        <button
          type="submit"
          disabled={submitting || !name.trim()}
          className="inline-flex items-center gap-2 rounded-lg bg-blue-600 hover:bg-blue-500 disabled:opacity-50 disabled:cursor-not-allowed px-4 py-2 text-sm font-medium text-white transition-colors"
        >
          {submitting ? 'Creating…' : 'Create tenant'}
        </button>
      </div>
    </form>
  )
}

function Field({
  label,
  hint,
  children,
}: {
  label: string
  hint?: string
  children: React.ReactNode
}) {
  return (
    <label className="block">
      <div className="flex items-baseline justify-between mb-1.5">
        <span className="text-xs font-medium text-white/70">{label}</span>
        {hint && <span className="text-[10px] text-white/30 font-mono">{hint}</span>}
      </div>
      {children}
    </label>
  )
}

function TenantsTable({
  tenants,
  loading,
  onRefresh,
  onRotateSecret,
  onToggleStatus,
  onImpersonate,
  onDelete,
}: {
  tenants: Tenant[]
  loading: boolean
  onRefresh: () => void
  onRotateSecret: (t: Tenant) => void
  onToggleStatus: (t: Tenant) => void
  onImpersonate: (t: Tenant) => void
  onDelete: (t: Tenant) => void
}) {
  if (loading) {
    return (
      <div className="border border-white/[0.06] rounded-xl p-12 text-center text-white/30 text-sm">
        Loading tenants…
      </div>
    )
  }
  if (tenants.length === 0) {
    return (
      <div className="border border-white/[0.06] rounded-xl p-12 text-center">
        <Building2 className="w-8 h-8 text-white/20 mx-auto mb-3" />
        <p className="text-sm text-white/50">No tenants yet.</p>
        <p className="text-xs text-white/30 mt-1">Create one above to get started.</p>
      </div>
    )
  }

  return (
    <div className="border border-white/[0.06] rounded-xl overflow-hidden">
      <table className="w-full text-sm">
        <thead>
          <tr className="text-[11px] uppercase tracking-[0.12em] text-white/40 border-b border-white/[0.06]">
            <th className="text-left font-medium px-5 py-3">Tenant</th>
            <th className="text-left font-medium px-5 py-3">Plan</th>
            <th className="text-right font-medium px-5 py-3">Devices</th>
            <th className="text-right font-medium px-5 py-3">Users</th>
            <th className="text-right font-medium px-5 py-3">Created</th>
            <th className="text-right font-medium px-5 py-3"></th>
          </tr>
        </thead>
        <tbody>
          {tenants.map((t) => {
            const isDefault = t.id === 'default'
            const isSuspended = t.status === 'suspended'
            return (
              <tr
                key={t.id}
                className={`border-b border-white/[0.04] last:border-b-0 transition-colors hover:bg-white/[0.02] ${
                  isSuspended ? 'opacity-60' : ''
                }`}
              >
                <td className="px-5 py-4">
                  <div className="flex items-center gap-3">
                    <div className="w-2 h-2 rounded-full bg-emerald-400" style={isSuspended ? { background: '#f59e0b' } : undefined} />
                    <div className="min-w-0">
                      <div className="flex items-center gap-2">
                        <span className="text-white font-medium truncate">{t.name}</span>
                        {isSuspended && (
                          <span className="text-[10px] uppercase tracking-wider text-amber-300/90 bg-amber-500/10 border border-amber-500/30 px-1.5 py-0.5 rounded">
                            Suspended
                          </span>
                        )}
                        {isDefault && (
                          <span className="text-[10px] uppercase tracking-wider text-white/40 bg-white/5 border border-white/10 px-1.5 py-0.5 rounded">
                            Default
                          </span>
                        )}
                        {!t.has_registration_key && !isDefault && (
                          <span className="text-[10px] uppercase tracking-wider text-orange-300/80 bg-orange-500/5 border border-orange-500/20 px-1.5 py-0.5 rounded" title="No registration secret yet">
                            No key
                          </span>
                        )}
                      </div>
                      <span className="text-xs text-white/35 font-mono">
                        {t.slug || t.id}
                      </span>
                    </div>
                  </div>
                </td>
                <td className="px-5 py-4 text-white/70 capitalize">{t.plan}</td>
                <td className="px-5 py-4 text-right text-white/70 font-mono tabular-nums">
                  {formatCount(t.device_count, t.max_devices)}
                </td>
                <td className="px-5 py-4 text-right text-white/70 font-mono tabular-nums">
                  {formatCount(t.user_count, t.max_users)}
                </td>
                <td className="px-5 py-4 text-right text-xs text-white/40">
                  {timeAgo(t.created_at)}
                </td>
                <td className="px-5 py-4 text-right">
                  <div className="inline-flex items-center gap-0.5">
                    <IconButton
                      title="Rotate registration secret"
                      onClick={() => onRotateSecret(t)}
                    >
                      <RotateCw className="w-3.5 h-3.5" />
                    </IconButton>
                    {!isDefault && !isSuspended && (
                      <IconButton
                        title="View as this tenant (impersonate)"
                        onClick={() => onImpersonate(t)}
                      >
                        <Eye className="w-3.5 h-3.5" />
                      </IconButton>
                    )}
                    {!isDefault && (
                      <>
                        <IconButton
                          title={isSuspended ? 'Reactivate' : 'Suspend'}
                          onClick={() => onToggleStatus(t)}
                        >
                          {isSuspended ? (
                            <PlayCircle className="w-3.5 h-3.5" />
                          ) : (
                            <PauseCircle className="w-3.5 h-3.5" />
                          )}
                        </IconButton>
                        <IconButton
                          title="Delete"
                          onClick={() => onDelete(t)}
                          danger
                        >
                          <Trash2 className="w-3.5 h-3.5" />
                        </IconButton>
                      </>
                    )}
                  </div>
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}

function IconButton({
  title,
  onClick,
  children,
  danger,
}: {
  title: string
  onClick: () => void
  children: React.ReactNode
  danger?: boolean
}) {
  return (
    <button
      title={title}
      onClick={onClick}
      className={`p-2 rounded-md transition-colors ${
        danger
          ? 'text-white/40 hover:text-rose-400 hover:bg-rose-500/10'
          : 'text-white/40 hover:text-white hover:bg-white/[0.05]'
      }`}
    >
      {children}
    </button>
  )
}

function RegistrationSecretReveal({
  tenantName,
  secret,
  commands,
  onDismiss,
}: {
  tenantName: string
  secret: string
  commands?: { linux: string; macos: string; windows: string }
  onDismiss: () => void
}) {
  const [copied, setCopied] = useState<string | null>(null)
  const [tab, setTab] = useState<'linux' | 'macos' | 'windows'>('linux')
  const copyText = async (key: string, text: string) => {
    await navigator.clipboard.writeText(text)
    setCopied(key)
    setTimeout(() => setCopied(null), 2000)
  }
  const tabCmd = commands?.[tab] ?? ''
  return (
    <div className="rounded-xl border border-amber-500/40 bg-amber-500/[0.04] animate-fadeInUp">
      <div className="px-6 py-5 flex items-start justify-between gap-6 border-b border-amber-500/20">
        <div>
          <p className="text-[11px] uppercase tracking-[0.16em] text-amber-300 mb-1">
            Install command · shown once
          </p>
          <h2 className="text-base font-medium text-white">
            Agent install for {tenantName}
          </h2>
          <p className="text-xs text-white/50 mt-2 max-w-xl leading-relaxed">
            Run the command below on the target machine to enrol an agent under this tenant.
            Re-rotating invalidates the previous secret.
          </p>
        </div>
        <button
          onClick={onDismiss}
          className="text-amber-300/60 hover:text-amber-200 transition-colors -mt-1"
          title="Dismiss"
        >
          <X className="w-5 h-5" />
        </button>
      </div>

      {commands ? (
        <>
          <div className="px-6 pt-4 flex items-center gap-1">
            {(['linux', 'macos', 'windows'] as const).map((p) => (
              <button
                key={p}
                onClick={() => setTab(p)}
                className={`px-3 py-1.5 text-xs font-medium rounded-md capitalize transition-colors ${
                  tab === p
                    ? 'bg-amber-500/20 text-amber-100 border border-amber-500/30'
                    : 'text-amber-300/60 hover:text-amber-200 border border-transparent'
                }`}
              >
                {p}
              </button>
            ))}
          </div>
          <div className="px-6 py-4 flex items-start gap-3">
            <code className="flex-1 font-mono text-xs text-amber-100 bg-black/40 border border-amber-500/20 rounded-lg px-4 py-3 select-all break-all whitespace-pre-wrap">
              {tabCmd}
            </code>
            <button
              onClick={() => copyText('cmd', tabCmd)}
              className="inline-flex items-center gap-2 rounded-lg bg-amber-500/20 hover:bg-amber-500/30 border border-amber-500/30 px-3 py-3 text-xs font-medium text-amber-100 transition-colors shrink-0"
            >
              {copied === 'cmd' ? <><Check className="w-3.5 h-3.5" /> Copied</> : <><Copy className="w-3.5 h-3.5" /> Copy</>}
            </button>
          </div>
        </>
      ) : null}

      <div className="px-6 py-4 border-t border-amber-500/20">
        <p className="text-[10px] uppercase tracking-[0.14em] text-amber-300/70 mb-2">
          Raw registration secret
        </p>
        <div className="flex items-center gap-3">
          <code className="flex-1 font-mono text-sm text-amber-100 bg-black/40 border border-amber-500/20 rounded-lg px-4 py-3 select-all break-all">
            {secret}
          </code>
          <button
            onClick={() => copyText('secret', secret)}
            className="inline-flex items-center gap-2 rounded-lg bg-amber-500/20 hover:bg-amber-500/30 border border-amber-500/30 px-3 py-3 text-xs font-medium text-amber-100 transition-colors shrink-0"
          >
            {copied === 'secret' ? <><Check className="w-3.5 h-3.5" /> Copied</> : <><Copy className="w-3.5 h-3.5" /> Copy</>}
          </button>
        </div>
      </div>

      <div className="px-6 py-3 border-t border-amber-500/20 flex items-center justify-end">
        <button
          onClick={onDismiss}
          className="text-xs text-amber-300/70 hover:text-amber-200 transition-colors"
        >
          I've saved it, dismiss
        </button>
      </div>
    </div>
  )
}

function DeleteTenantDialog({
  tenant,
  onCancel,
  onConfirmed,
}: {
  tenant: Tenant
  onCancel: () => void
  onConfirmed: () => void
}) {
  const [typed, setTyped] = useState('')
  const matches = typed.trim() === tenant.name
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm"
      onClick={onCancel}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        className="w-full max-w-md rounded-xl border border-rose-500/30 bg-[#0a0a0e] shadow-2xl"
      >
        <div className="px-6 py-5 border-b border-white/[0.06]">
          <p className="text-[11px] uppercase tracking-[0.14em] text-rose-300/80 mb-1">
            Destructive action
          </p>
          <h3 className="text-base font-medium text-white">Delete tenant</h3>
          <p className="text-xs text-white/50 mt-2 leading-relaxed">
            This deletes the tenant row. It will fail if any users or devices
            still reference this tenant. Audit logs are preserved.
          </p>
        </div>
        <div className="px-6 py-4 space-y-3">
          <p className="text-xs text-white/60">
            Type <span className="font-mono text-white">{tenant.name}</span> to confirm:
          </p>
          <input
            autoFocus
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            className="w-full bg-[#0c0c12] border border-white/10 rounded-lg px-3 py-2 text-sm text-white font-mono focus:outline-none focus:border-rose-500/50"
          />
        </div>
        <div className="px-6 py-3 border-t border-white/[0.06] flex items-center justify-end gap-2">
          <button
            onClick={onCancel}
            className="px-4 py-2 text-sm text-white/60 hover:text-white transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={onConfirmed}
            disabled={!matches}
            className="inline-flex items-center gap-2 rounded-lg bg-rose-600 hover:bg-rose-500 disabled:bg-rose-600/30 disabled:cursor-not-allowed px-4 py-2 text-sm font-medium text-white transition-colors"
          >
            <Trash2 className="w-4 h-4" />
            Delete tenant
          </button>
        </div>
      </div>
    </div>
  )
}
