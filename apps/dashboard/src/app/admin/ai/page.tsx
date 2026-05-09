'use client'

import { useEffect, useMemo, useState } from 'react'
import { toast } from 'sonner'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { useCurrentUser } from '@/components/CurrentUserProvider'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import {
  aiApi,
  AICapability,
  AIKillSwitch,
  AIProvider,
  AIRoutingRule,
  AIRun,
  AITenantSettings,
} from '@/lib/api'
import {
  AlertTriangle,
  Loader2,
  Plus,
  ShieldOff,
  Sparkles,
  Trash2,
  XCircle,
} from 'lucide-react'

const TASK_TYPES = ['classify', 'reason', 'summarize', 'embed', 'generate'] as const
const RUNG_OPTIONS = ['shadow', 'suggest', 'act_low', 'act_policy', 'autonomous'] as const

function microsToUSD(n: number): string {
  return (n / 1_000_000).toFixed(4)
}
function usdToMicros(s: string): number {
  const v = parseFloat(s)
  if (Number.isNaN(v) || v < 0) return 0
  return Math.round(v * 1_000_000)
}

export default function AIAdminPage() {
  return (
    <AuthGuard>
      <DashboardShell>
        <AIAdminInner />
      </DashboardShell>
    </AuthGuard>
  )
}

function AIAdminInner() {
  const { user } = useCurrentUser()
  const isSuperAdmin = user?.role === 'super_admin'
  const [unsupported, setUnsupported] = useState<string | null>(null)
  const [tenant, setTenant] = useState<AITenantSettings | null>(null)
  const [providers, setProviders] = useState<AIProvider[]>([])
  const [providerKinds, setProviderKinds] = useState<string[]>([])
  const [routing, setRouting] = useState<AIRoutingRule[]>([])
  const [capabilities, setCapabilities] = useState<AICapability[]>([])
  const [runs, setRuns] = useState<AIRun[]>([])
  const [killSwitches, setKillSwitches] = useState<AIKillSwitch[]>([])
  const [loading, setLoading] = useState(true)

  async function reloadAll() {
    setLoading(true)
    try {
      const [t, ps, rs, cs, runRes, ks] = await Promise.all([
        aiApi.getTenant(),
        aiApi.listProviders(),
        aiApi.listRouting(),
        aiApi.listCapabilities(),
        aiApi.listRuns({ limit: 50 }),
        aiApi.listKill(),
      ])
      setTenant(t)
      setProviders(ps.providers)
      setProviderKinds(ps.kinds)
      setRouting(rs)
      setCapabilities(cs)
      setRuns(runRes.runs)
      setKillSwitches(ks)
      setUnsupported(null)
    } catch (err: any) {
      const msg: string | undefined = err?.response?.data?.error
      if (err?.response?.status === 503 && msg) {
        setUnsupported(msg)
      } else {
        toast.error(msg ?? 'Failed to load AI configuration')
      }
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    reloadAll()
  }, [])

  if (unsupported) {
    return (
      <div className="max-w-3xl mx-auto py-12 px-4">
        <Card className="border-amber-500/50 bg-amber-500/5">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-amber-700">
              <AlertTriangle className="h-5 w-5" /> AI features unavailable
            </CardTitle>
          </CardHeader>
          <CardContent className="text-sm text-muted-foreground">
            <p>{unsupported}</p>
            <p className="mt-3">
              The AI agent layer needs PostgreSQL for the embeddings store, atomic cost
              counters, and audit-log volume. Switch your <code>DATABASE_URL</code> to a
              Postgres connection string and restart the server. Existing tenant data
              migrates with the standard backup/restore flow.
            </p>
          </CardContent>
        </Card>
      </div>
    )
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center py-24 text-muted-foreground">
        <Loader2 className="h-6 w-6 animate-spin mr-2" /> Loading AI configuration…
      </div>
    )
  }

  return (
    <div className="max-w-6xl mx-auto py-8 px-4 space-y-8">
      <header className="flex items-baseline justify-between">
        <div>
          <h1 className="text-2xl font-semibold flex items-center gap-2">
            <Sparkles className="h-6 w-6" /> AI agent
          </h1>
          <p className="text-sm text-muted-foreground mt-1">
            Provider configuration, capability ladder, kill switches, and the audit
            ledger for every model call.
          </p>
        </div>
      </header>

      <TenantSection tenant={tenant} isSuperAdmin={isSuperAdmin} onReload={reloadAll} />

      <KillSwitchSection
        killSwitches={killSwitches}
        isSuperAdmin={isSuperAdmin}
        onReload={reloadAll}
      />

      <ProvidersSection
        providers={providers}
        kinds={providerKinds}
        onReload={reloadAll}
      />

      <RoutingSection providers={providers} routing={routing} onReload={reloadAll} />

      <CapabilitiesSection
        capabilities={capabilities}
        isSuperAdmin={isSuperAdmin}
        onReload={reloadAll}
      />

      <RunsSection runs={runs} />
    </div>
  )
}

// ── Tenant master switch ────────────────────────────────────────────────

function TenantSection({
  tenant,
  isSuperAdmin,
  onReload,
}: {
  tenant: AITenantSettings | null
  isSuperAdmin: boolean
  onReload: () => void
}) {
  const [chatCap, setChatCap] = useState('')
  const [embedCap, setEmbedCap] = useState('')
  const [billing, setBilling] = useState<'absorb' | 'passthrough'>('absorb')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (!tenant) return
    setChatCap(microsToUSD(tenant.ai_max_chat_cost_per_day_micros))
    setEmbedCap(microsToUSD(tenant.ai_max_embedding_cost_per_day_micros))
    setBilling(tenant.ai_billing_mode)
  }, [tenant])

  if (!tenant) return null

  async function toggleEnabled(next: boolean) {
    setBusy(true)
    try {
      await aiApi.patchTenant({ ai_enabled: next })
      toast.success(next ? 'AI enabled for this tenant' : 'AI disabled')
      onReload()
    } catch (err: any) {
      toast.error(err?.response?.data?.error ?? 'Update failed')
    } finally {
      setBusy(false)
    }
  }

  async function saveCaps() {
    setBusy(true)
    try {
      await aiApi.patchTenant({
        ai_billing_mode: billing,
        ai_max_chat_cost_per_day_micros: usdToMicros(chatCap),
        ai_max_embedding_cost_per_day_micros: usdToMicros(embedCap),
      })
      toast.success('Saved')
      onReload()
    } catch (err: any) {
      toast.error(err?.response?.data?.error ?? 'Save failed')
    } finally {
      setBusy(false)
    }
  }

  async function ackDPA() {
    await aiApi.patchTenant({ acknowledge_dpa: true })
    toast.success('DPA acknowledgement recorded')
    onReload()
  }

  const dpaOK = !!tenant.ai_dpa_acknowledged_at

  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between space-y-0">
        <div>
          <CardTitle>Tenant settings</CardTitle>
          <CardDescription>
            Master switch, billing mode, and per-day cost caps.
          </CardDescription>
        </div>
        <div className="flex items-center gap-3">
          <span className="text-sm text-muted-foreground">
            {tenant.ai_enabled ? 'Enabled' : 'Disabled'}
          </span>
          <Button
            variant={tenant.ai_enabled ? 'outline' : 'default'}
            disabled={busy || (!isSuperAdmin && !tenant.ai_enabled)}
            onClick={() => toggleEnabled(!tenant.ai_enabled)}
            title={!isSuperAdmin && !tenant.ai_enabled ? 'Only super_admin may enable AI' : ''}
          >
            {tenant.ai_enabled ? 'Disable' : 'Enable'}
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-6">
        {!dpaOK && (
          <div className="rounded-md border border-amber-500/40 bg-amber-500/5 p-4 text-sm">
            <p className="font-medium text-amber-700">Data-processing acknowledgement required</p>
            <p className="text-muted-foreground mt-1">
              AI features send tenant data (device metadata, ticket bodies, alert
              text) to the providers you configure. You are responsible for
              executing a Data Processing Agreement with each external provider
              before enabling AI on regulated workloads.
            </p>
            <Button onClick={ackDPA} className="mt-3" size="sm">
              I&apos;ve reviewed my DPAs — acknowledge
            </Button>
          </div>
        )}

        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <Field label="Billing mode">
            <select
              className="border rounded px-2 py-1.5 bg-background w-full"
              value={billing}
              onChange={(e) => setBilling(e.target.value as 'absorb' | 'passthrough')}
            >
              <option value="absorb">Absorb (MSP eats AI cost)</option>
              <option value="passthrough">Passthrough (bill the customer)</option>
            </select>
          </Field>
          <Field label="Daily chat cap (USD, 0 = unlimited)">
            <input
              type="text"
              inputMode="decimal"
              className="border rounded px-2 py-1.5 bg-background w-full"
              value={chatCap}
              onChange={(e) => setChatCap(e.target.value)}
            />
          </Field>
          <Field label="Daily embedding cap (USD, 0 = unlimited)">
            <input
              type="text"
              inputMode="decimal"
              className="border rounded px-2 py-1.5 bg-background w-full"
              value={embedCap}
              onChange={(e) => setEmbedCap(e.target.value)}
            />
          </Field>
        </div>

        <div className="flex justify-end">
          <Button onClick={saveCaps} disabled={busy}>Save</Button>
        </div>
      </CardContent>
    </Card>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block">
      <span className="text-xs uppercase tracking-wide text-muted-foreground">
        {label}
      </span>
      <div className="mt-1">{children}</div>
    </label>
  )
}

// ── Kill switches ──────────────────────────────────────────────────────

function KillSwitchSection({
  killSwitches,
  isSuperAdmin,
  onReload,
}: {
  killSwitches: AIKillSwitch[]
  isSuperAdmin: boolean
  onReload: () => void
}) {
  const globalKilled = killSwitches.find((k) => k.scope === 'global')?.enabled ?? false

  async function toggleGlobal() {
    if (!isSuperAdmin) return
    const reason = prompt('Reason for flipping the global kill switch:') ?? ''
    await aiApi.setKill('global', !globalKilled, reason)
    toast.success(globalKilled ? 'Global kill cleared' : 'GLOBAL KILL ACTIVE')
    onReload()
  }

  return (
    <Card className={globalKilled ? 'border-red-500/60 bg-red-500/5' : ''}>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <ShieldOff className="h-5 w-5" /> Kill switches
        </CardTitle>
        <CardDescription>
          Active kill switches short-circuit every gate before any provider call. Cached
          in memory and broadcast over Redis pub/sub.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {isSuperAdmin && (
          <div className="flex items-center justify-between border rounded p-3">
            <div>
              <p className="font-medium">Global</p>
              <p className="text-xs text-muted-foreground">
                {globalKilled ? 'KILLED — no AI calls succeed anywhere.' : 'Healthy.'}
              </p>
            </div>
            <Button
              variant={globalKilled ? 'outline' : 'destructive'}
              onClick={toggleGlobal}
            >
              {globalKilled ? 'Clear global kill' : 'KILL ALL AI'}
            </Button>
          </div>
        )}
        {killSwitches.length === 0 ? (
          <p className="text-sm text-muted-foreground">No active kill switches.</p>
        ) : (
          <table className="w-full text-sm">
            <thead className="text-left text-xs uppercase text-muted-foreground">
              <tr>
                <th className="py-2 pr-3">Scope</th>
                <th className="py-2 pr-3">State</th>
                <th className="py-2 pr-3">Reason</th>
                <th className="py-2 pr-3">Set at</th>
              </tr>
            </thead>
            <tbody>
              {killSwitches.map((k) => (
                <tr key={k.scope} className="border-t">
                  <td className="py-2 pr-3 font-mono">{k.scope}</td>
                  <td className="py-2 pr-3">
                    {k.enabled ? (
                      <span className="text-red-600">killed</span>
                    ) : (
                      <span className="text-muted-foreground">cleared</span>
                    )}
                  </td>
                  <td className="py-2 pr-3 text-muted-foreground">{k.reason || '—'}</td>
                  <td className="py-2 pr-3 text-muted-foreground">
                    {new Date(k.set_at * 1000).toLocaleString()}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </CardContent>
    </Card>
  )
}

// ── Providers ──────────────────────────────────────────────────────────

function ProvidersSection({
  providers,
  kinds,
  onReload,
}: {
  providers: AIProvider[]
  kinds: string[]
  onReload: () => void
}) {
  const [showAdd, setShowAdd] = useState(false)

  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between space-y-0">
        <div>
          <CardTitle>Providers</CardTitle>
          <CardDescription>
            One per upstream API. Self-hosted Ollama / vLLM endpoints are supported via the
            base-URL field.
          </CardDescription>
        </div>
        <Button onClick={() => setShowAdd((v) => !v)}>
          <Plus className="h-4 w-4 mr-1" /> Add provider
        </Button>
      </CardHeader>
      <CardContent>
        {showAdd && (
          <ProviderForm
            kinds={kinds}
            onCancel={() => setShowAdd(false)}
            onSaved={() => {
              setShowAdd(false)
              onReload()
            }}
          />
        )}
        {providers.length === 0 ? (
          <p className="text-sm text-muted-foreground py-4">
            No providers configured. Add one above to enable AI features.
          </p>
        ) : (
          <table className="w-full text-sm mt-3">
            <thead className="text-left text-xs uppercase text-muted-foreground">
              <tr>
                <th className="py-2 pr-3">Name</th>
                <th className="py-2 pr-3">Kind</th>
                <th className="py-2 pr-3">Base URL</th>
                <th className="py-2 pr-3">Region</th>
                <th className="py-2 pr-3">Trust</th>
                <th className="py-2 pr-3">State</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {providers.map((p) => (
                <tr key={p.id} className="border-t">
                  <td className="py-2 pr-3 font-medium">{p.name}</td>
                  <td className="py-2 pr-3 text-muted-foreground">{p.kind}</td>
                  <td className="py-2 pr-3 font-mono text-xs">{p.base_url || '—'}</td>
                  <td className="py-2 pr-3 text-muted-foreground">{p.region || '—'}</td>
                  <td className="py-2 pr-3 text-muted-foreground">{p.model_trust_level}</td>
                  <td className="py-2 pr-3">
                    {p.enabled ? 'enabled' : <span className="text-muted-foreground">disabled</span>}
                  </td>
                  <td className="py-2 pr-3 text-right">
                    <Button
                      variant="ghost"
                      size="icon"
                      onClick={async () => {
                        if (!confirm(`Delete provider "${p.name}"?`)) return
                        await aiApi.deleteProvider(p.id)
                        toast.success('Deleted')
                        onReload()
                      }}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </CardContent>
    </Card>
  )
}

function ProviderForm({
  kinds,
  onCancel,
  onSaved,
}: {
  kinds: string[]
  onCancel: () => void
  onSaved: () => void
}) {
  const [kind, setKind] = useState(kinds[0] ?? 'openai')
  const [name, setName] = useState('')
  const [baseURL, setBaseURL] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [region, setRegion] = useState('')
  const [trust, setTrust] = useState<'local' | 'external' | 'self_hosted'>('external')
  const [enabled, setEnabled] = useState(true)
  const [busy, setBusy] = useState(false)

  async function save() {
    if (!name) {
      toast.error('Name is required')
      return
    }
    setBusy(true)
    try {
      await aiApi.createProvider({
        kind,
        name,
        base_url: baseURL || undefined,
        api_key: apiKey || undefined,
        region: region || undefined,
        model_trust_level: trust,
        enabled,
      })
      toast.success('Provider added')
      onSaved()
    } catch (err: any) {
      toast.error(err?.response?.data?.error ?? 'Save failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="border rounded p-4 mb-4 space-y-3 bg-muted/30">
      <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
        <Field label="Kind">
          <select
            className="border rounded px-2 py-1.5 bg-background w-full"
            value={kind}
            onChange={(e) => setKind(e.target.value)}
          >
            {kinds.map((k) => (
              <option key={k} value={k}>{k}</option>
            ))}
          </select>
        </Field>
        <Field label="Display name">
          <input
            className="border rounded px-2 py-1.5 bg-background w-full"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="e.g. Production OpenAI"
          />
        </Field>
        <Field label="Base URL (optional, for compat / self-hosted)">
          <input
            className="border rounded px-2 py-1.5 bg-background w-full font-mono text-xs"
            value={baseURL}
            onChange={(e) => setBaseURL(e.target.value)}
            placeholder="https://api.openai.com/v1"
          />
        </Field>
        <Field label="API key (write-only)">
          <input
            type="password"
            className="border rounded px-2 py-1.5 bg-background w-full font-mono text-xs"
            value={apiKey}
            onChange={(e) => setApiKey(e.target.value)}
            placeholder="sk-..."
          />
        </Field>
        <Field label="Region (optional)">
          <input
            className="border rounded px-2 py-1.5 bg-background w-full"
            value={region}
            onChange={(e) => setRegion(e.target.value)}
            placeholder="us | eu | …"
          />
        </Field>
        <Field label="Trust level">
          <select
            className="border rounded px-2 py-1.5 bg-background w-full"
            value={trust}
            onChange={(e) => setTrust(e.target.value as any)}
          >
            <option value="external">external (SaaS)</option>
            <option value="local">local (in-cluster)</option>
            <option value="self_hosted">self_hosted (extra approval)</option>
          </select>
        </Field>
      </div>
      <label className="inline-flex items-center gap-2 text-sm">
        <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
        Enabled
      </label>
      <div className="flex gap-2 justify-end">
        <Button variant="ghost" onClick={onCancel}>Cancel</Button>
        <Button onClick={save} disabled={busy}>Save</Button>
      </div>
    </div>
  )
}

// ── Routing rules ──────────────────────────────────────────────────────

function RoutingSection({
  providers,
  routing,
  onReload,
}: {
  providers: AIProvider[]
  routing: AIRoutingRule[]
  onReload: () => void
}) {
  const [showAdd, setShowAdd] = useState(false)
  const providerName = useMemo(() => {
    const m = new Map<string, string>()
    providers.forEach((p) => m.set(p.id, p.name))
    return m
  }, [providers])

  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between space-y-0">
        <div>
          <CardTitle>Routing rules</CardTitle>
          <CardDescription>
            One rule per task type — cheap small/local models for classification, frontier
            models for reasoning.
          </CardDescription>
        </div>
        <Button onClick={() => setShowAdd((v) => !v)} disabled={providers.length === 0}>
          <Plus className="h-4 w-4 mr-1" /> Add rule
        </Button>
      </CardHeader>
      <CardContent>
        {showAdd && (
          <RoutingForm
            providers={providers}
            onCancel={() => setShowAdd(false)}
            onSaved={() => {
              setShowAdd(false)
              onReload()
            }}
          />
        )}
        {routing.length === 0 ? (
          <p className="text-sm text-muted-foreground py-4">
            No routing rules. Add one per task type once you have at least one provider.
          </p>
        ) : (
          <table className="w-full text-sm mt-3">
            <thead className="text-left text-xs uppercase text-muted-foreground">
              <tr>
                <th className="py-2 pr-3">Task</th>
                <th className="py-2 pr-3">Provider</th>
                <th className="py-2 pr-3">Model</th>
                <th className="py-2 pr-3">Max / call</th>
                <th className="py-2 pr-3">Rate per 1k (in / out)</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {routing.map((r) => (
                <tr key={r.id} className="border-t">
                  <td className="py-2 pr-3 font-medium">{r.task_type}</td>
                  <td className="py-2 pr-3">{providerName.get(r.preferred_provider_id) ?? r.preferred_provider_id}</td>
                  <td className="py-2 pr-3 font-mono text-xs">{r.model_name}</td>
                  <td className="py-2 pr-3">${microsToUSD(r.max_cost_per_call_micros)}</td>
                  <td className="py-2 pr-3 text-muted-foreground">
                    ${microsToUSD(r.cost_per_1k_input_micros)} / ${microsToUSD(r.cost_per_1k_output_micros)}
                  </td>
                  <td className="py-2 pr-3 text-right">
                    <Button
                      variant="ghost"
                      size="icon"
                      onClick={async () => {
                        if (!confirm(`Delete routing rule for ${r.task_type}?`)) return
                        await aiApi.deleteRouting(r.id)
                        toast.success('Deleted')
                        onReload()
                      }}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </CardContent>
    </Card>
  )
}

function RoutingForm({
  providers,
  onCancel,
  onSaved,
}: {
  providers: AIProvider[]
  onCancel: () => void
  onSaved: () => void
}) {
  const [taskType, setTaskType] = useState<typeof TASK_TYPES[number]>('reason')
  const [preferred, setPreferred] = useState(providers[0]?.id ?? '')
  const [fallback, setFallback] = useState('')
  const [model, setModel] = useState('')
  const [maxCost, setMaxCost] = useState('1.00')
  const [inRate, setInRate] = useState('0')
  const [outRate, setOutRate] = useState('0')
  const [busy, setBusy] = useState(false)

  async function save() {
    if (!preferred || !model) {
      toast.error('Provider and model name are required')
      return
    }
    setBusy(true)
    try {
      await aiApi.createRouting({
        task_type: taskType,
        preferred_provider_id: preferred,
        fallback_provider_id: fallback || undefined,
        model_name: model,
        max_cost_per_call_micros: usdToMicros(maxCost),
        cost_per_1k_input_micros: usdToMicros(inRate),
        cost_per_1k_output_micros: usdToMicros(outRate),
      })
      toast.success('Routing rule added')
      onSaved()
    } catch (err: any) {
      toast.error(err?.response?.data?.error ?? 'Save failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="border rounded p-4 mb-4 space-y-3 bg-muted/30">
      <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
        <Field label="Task type">
          <select
            className="border rounded px-2 py-1.5 bg-background w-full"
            value={taskType}
            onChange={(e) => setTaskType(e.target.value as typeof TASK_TYPES[number])}
          >
            {TASK_TYPES.map((t) => <option key={t} value={t}>{t}</option>)}
          </select>
        </Field>
        <Field label="Preferred provider">
          <select
            className="border rounded px-2 py-1.5 bg-background w-full"
            value={preferred}
            onChange={(e) => setPreferred(e.target.value)}
          >
            {providers.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
          </select>
        </Field>
        <Field label="Fallback provider (optional)">
          <select
            className="border rounded px-2 py-1.5 bg-background w-full"
            value={fallback}
            onChange={(e) => setFallback(e.target.value)}
          >
            <option value="">— none —</option>
            {providers.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
          </select>
        </Field>
        <Field label="Model name (pin a version where possible)">
          <input
            className="border rounded px-2 py-1.5 bg-background w-full font-mono text-xs"
            value={model}
            onChange={(e) => setModel(e.target.value)}
            placeholder="gpt-4o-2024-11-20"
          />
        </Field>
        <Field label="Max cost per call (USD)">
          <input
            className="border rounded px-2 py-1.5 bg-background w-full"
            value={maxCost}
            onChange={(e) => setMaxCost(e.target.value)}
          />
        </Field>
        <Field label="Cost per 1k tokens (in / out, USD)">
          <div className="flex gap-2">
            <input
              className="border rounded px-2 py-1.5 bg-background w-full"
              value={inRate}
              onChange={(e) => setInRate(e.target.value)}
            />
            <input
              className="border rounded px-2 py-1.5 bg-background w-full"
              value={outRate}
              onChange={(e) => setOutRate(e.target.value)}
            />
          </div>
        </Field>
      </div>
      <div className="flex gap-2 justify-end">
        <Button variant="ghost" onClick={onCancel}>Cancel</Button>
        <Button onClick={save} disabled={busy}>Save</Button>
      </div>
    </div>
  )
}

// ── Capabilities ───────────────────────────────────────────────────────

function CapabilitiesSection({
  capabilities,
  isSuperAdmin,
  onReload,
}: {
  capabilities: AICapability[]
  isSuperAdmin: boolean
  onReload: () => void
}) {
  if (capabilities.length === 0) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>Capabilities</CardTitle>
          <CardDescription>
            No capabilities are registered in this build. Stage 1 ships alert dedup,
            ticket clustering, anomaly detection, and risk scoring.
          </CardDescription>
        </CardHeader>
      </Card>
    )
  }

  async function patch(name: string, data: any) {
    try {
      await aiApi.patchCapability(name, data)
      toast.success('Updated')
      onReload()
    } catch (err: any) {
      toast.error(err?.response?.data?.error ?? 'Update failed')
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Capabilities</CardTitle>
        <CardDescription>
          Default off. Promotion past <code>suggest</code> is super_admin only. Demotion is
          automatic when metrics regress.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <table className="w-full text-sm">
          <thead className="text-left text-xs uppercase text-muted-foreground">
            <tr>
              <th className="py-2 pr-3">Capability</th>
              <th className="py-2 pr-3">Stage</th>
              <th className="py-2 pr-3">Enabled</th>
              <th className="py-2 pr-3">Rung</th>
              <th className="py-2 pr-3">Kill</th>
              <th className="py-2 pr-3">Deps</th>
            </tr>
          </thead>
          <tbody>
            {capabilities.map((c) => {
              const blocked = c.unmet_dependencies.length > 0
              return (
                <tr key={c.name} className="border-t align-top">
                  <td className="py-2 pr-3">
                    <p className="font-medium">{c.name}</p>
                    <p className="text-xs text-muted-foreground">{c.description}</p>
                  </td>
                  <td className="py-2 pr-3 text-muted-foreground">{c.stage}</td>
                  <td className="py-2 pr-3">
                    <input
                      type="checkbox"
                      checked={c.enabled}
                      disabled={blocked}
                      onChange={(e) => patch(c.name, { enabled: e.target.checked })}
                    />
                  </td>
                  <td className="py-2 pr-3">
                    <select
                      className="border rounded px-2 py-1 bg-background text-xs"
                      value={c.rung}
                      onChange={(e) => patch(c.name, { rung: e.target.value })}
                    >
                      {RUNG_OPTIONS.map((r) => (
                        <option
                          key={r}
                          value={r}
                          disabled={!isSuperAdmin && (r === 'act_low' || r === 'act_policy' || r === 'autonomous')}
                        >
                          {r}
                        </option>
                      ))}
                    </select>
                  </td>
                  <td className="py-2 pr-3">
                    <input
                      type="checkbox"
                      checked={c.kill_switch}
                      onChange={(e) => patch(c.name, { kill_switch: e.target.checked })}
                    />
                  </td>
                  <td className="py-2 pr-3 text-xs text-muted-foreground">
                    {blocked ? (
                      <span className="text-amber-700 inline-flex items-center gap-1">
                        <XCircle className="h-3 w-3" /> {c.unmet_dependencies.join(', ')}
                      </span>
                    ) : (
                      c.depends_on.length === 0 ? '—' : c.depends_on.join(', ')
                    )}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </CardContent>
    </Card>
  )
}

// ── Audit ledger ────────────────────────────────────────────────────────

function RunsSection({ runs }: { runs: AIRun[] }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Recent runs</CardTitle>
        <CardDescription>
          Last 50 model calls, write-once. Includes rung at call time, model version,
          cost, and outcome label.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {runs.length === 0 ? (
          <p className="text-sm text-muted-foreground py-4">No AI calls have run yet.</p>
        ) : (
          <table className="w-full text-sm">
            <thead className="text-left text-xs uppercase text-muted-foreground">
              <tr>
                <th className="py-2 pr-3">When</th>
                <th className="py-2 pr-3">Capability</th>
                <th className="py-2 pr-3">Type</th>
                <th className="py-2 pr-3">Model</th>
                <th className="py-2 pr-3">Tokens</th>
                <th className="py-2 pr-3">Cost</th>
                <th className="py-2 pr-3">Latency</th>
                <th className="py-2 pr-3">Rung</th>
                <th className="py-2 pr-3">Outcome</th>
              </tr>
            </thead>
            <tbody>
              {runs.map((r) => (
                <tr key={r.id} className="border-t">
                  <td className="py-2 pr-3 text-muted-foreground">
                    {new Date(r.created_at * 1000).toLocaleString()}
                  </td>
                  <td className="py-2 pr-3 font-medium">{r.capability_id || '—'}</td>
                  <td className="py-2 pr-3 text-muted-foreground">{r.run_type}</td>
                  <td className="py-2 pr-3 font-mono text-xs">
                    {r.model_name}
                    {r.model_version && r.model_version !== r.model_name && (
                      <span className="text-muted-foreground"> · {r.model_version}</span>
                    )}
                  </td>
                  <td className="py-2 pr-3 text-muted-foreground">
                    {r.prompt_tokens} / {r.output_tokens}
                  </td>
                  <td className="py-2 pr-3">${microsToUSD(r.cost_usd_micros)}</td>
                  <td className="py-2 pr-3 text-muted-foreground">{r.latency_ms} ms</td>
                  <td className="py-2 pr-3">{r.rung_at_call}</td>
                  <td className="py-2 pr-3 text-muted-foreground">{r.outcome || '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </CardContent>
    </Card>
  )
}
