'use client'

// Phase 1 of Tailscale integration (issue #18). The wizard step that
// gives the operator three options:
//   - Connect Tailscale account (recommended)
//   - Bring your own (advanced)
//   - Skip (not recommended)
//
// Settings → Network page (commit 5) reuses the TailscaleConfigPanel
// component for post-setup connection. Keep the panel decoupled from
// the wizard so the same UX serves both surfaces.

import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { CheckCircle, AlertTriangle, ExternalLink, X } from 'lucide-react'
import { toast } from 'sonner'
import api from '@/lib/api'

type Mode = 'tailscale' | 'byo' | 'skip'

type CheckState = 'pending' | 'ok' | 'failed'

type ValidateResponse = {
  checks: { authentication: CheckState; auth_key_scope: CheckState; device_list_scope: CheckState }
  errors: Partial<Record<'authentication' | 'auth_key_scope' | 'device_list_scope' | 'tailnets', string>>
  tailnets: { name: string; display_name?: string }[]
}

export default function NetworkStep() {
  const [mode, setMode] = useState<Mode>('tailscale')
  return (
    <div className="space-y-5">
      <div>
        <h3 className="text-base font-semibold text-white">Remote access network</h3>
        <p className="text-sm text-white/50 mt-1">
          Configure how this VaporwareRMM instance reaches managed endpoints. Tailscale
          gives your devices a private mesh network so Sunshine remote desktop and agent
          traffic never traverses the public internet.
        </p>
      </div>

      <ModeOption
        active={mode === 'tailscale'}
        onSelect={() => setMode('tailscale')}
        label="Connect Tailscale account"
        tagline="recommended"
        body="Mint short-lived auth keys for each agent install. Endpoints never see your long-lived credential."
        accent="cyan"
      />
      <ModeOption
        active={mode === 'byo'}
        onSelect={() => setMode('byo')}
        label="Bring your own Tailscale setup"
        tagline="advanced"
        body="The install script will accept TAILSCALE_AUTH_KEY from environment variables. VaporwareRMM won't manage the connection."
        accent="white"
      />
      <ModeOption
        active={mode === 'skip'}
        onSelect={() => setMode('skip')}
        label="Skip Tailscale"
        tagline="NOT RECOMMENDED"
        body="Agent and Sunshine traffic will traverse the public internet. Sunshine remote desktop will not work without exposing additional ports."
        accent="amber"
      />

      {mode === 'tailscale' && <TailscaleConfigPanel />}
      {mode === 'byo' && <ByoPanel />}
      {mode === 'skip' && <SkipPanel />}
    </div>
  )
}

function ModeOption({
  active,
  onSelect,
  label,
  tagline,
  body,
  accent,
}: {
  active: boolean
  onSelect: () => void
  label: string
  tagline: string
  body: string
  accent: 'cyan' | 'white' | 'amber'
}) {
  const ring =
    accent === 'cyan'
      ? 'border-cyan-500/40'
      : accent === 'amber'
        ? 'border-amber-500/40'
        : 'border-white/[0.16]'
  const tagColor =
    accent === 'cyan'
      ? 'text-cyan-300'
      : accent === 'amber'
        ? 'text-amber-300'
        : 'text-white/50'
  return (
    <button
      type="button"
      onClick={onSelect}
      className={`w-full text-left rounded-xl border px-4 py-3 transition-colors ${
        active ? `bg-white/[0.04] ${ring}` : 'bg-transparent border-white/[0.06] hover:bg-white/[0.02]'
      }`}
    >
      <div className="flex items-baseline justify-between gap-3">
        <span className="text-sm font-medium text-white">{label}</span>
        <span className={`text-[10px] uppercase tracking-wider font-semibold ${tagColor}`}>{tagline}</span>
      </div>
      <p className="text-xs text-white/45 mt-1 leading-snug">{body}</p>
    </button>
  )
}

export function TailscaleConfigPanel({ onConnected }: { onConnected?: () => void } = {}) {
  const [clientID, setClientID] = useState('')
  const [clientSecret, setClientSecret] = useState('')
  const [validating, setValidating] = useState(false)
  const [result, setResult] = useState<ValidateResponse | null>(null)
  const [selectedTailnet, setSelectedTailnet] = useState('')
  const [connecting, setConnecting] = useState(false)

  const allPassed =
    result?.checks.authentication === 'ok' &&
    result?.checks.auth_key_scope === 'ok' &&
    result?.checks.device_list_scope === 'ok'

  const validate = async () => {
    setValidating(true)
    try {
      const { data } = await api.post<ValidateResponse>('/tailscale/validate', {
        client_id: clientID,
        client_secret: clientSecret,
      })
      setResult(data)
      if (data.tailnets?.length === 1) setSelectedTailnet(data.tailnets[0].name)
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : 'request failed'
      toast.error(`Validation request failed: ${msg}`)
    } finally {
      setValidating(false)
    }
  }

  const connect = async () => {
    if (!allPassed || !selectedTailnet) return
    setConnecting(true)
    try {
      await api.post('/tailscale/connect', {
        client_id: clientID,
        client_secret: clientSecret,
        tailnet: selectedTailnet,
      })
      toast.success(`Tailscale connected to ${selectedTailnet}`)
      onConnected?.()
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : 'request failed'
      toast.error(`Connect failed: ${msg}`)
    } finally {
      setConnecting(false)
    }
  }

  return (
    <div className="rounded-xl border border-cyan-500/20 bg-cyan-500/[0.04] p-4 space-y-4">
      <div>
        <h4 className="text-sm font-semibold text-white">Create an OAuth client in Tailscale</h4>
        <ol className="mt-2 space-y-1 text-xs text-white/60 list-decimal list-inside">
          <li>
            Open the OAuth clients page:{' '}
            <a
              href="https://login.tailscale.com/admin/settings/oauth"
              target="_blank"
              rel="noopener noreferrer"
              className="text-cyan-300 hover:text-cyan-200 underline inline-flex items-center gap-1"
            >
              tailscale.com/admin/settings/oauth <ExternalLink className="w-3 h-3" />
            </a>
          </li>
          <li>Click <span className="text-white/80">Generate OAuth client...</span></li>
          <li>Name it <code className="text-white/80">VaporwareRMM</code></li>
          <li>
            Grant scopes: <code className="text-white/80">auth_keys</code> (write) and{' '}
            <code className="text-white/80">devices</code> (read)
          </li>
          <li>Click <span className="text-white/80">Generate client</span>. Copy the client ID and client secret below.</li>
        </ol>
      </div>

      <div className="space-y-3">
        <div>
          <label className="block text-[11px] uppercase tracking-wider text-white/40 mb-1">OAuth client ID</label>
          <input
            type="text"
            value={clientID}
            onChange={(e) => setClientID(e.target.value)}
            className="w-full bg-white/[0.04] border border-white/[0.08] rounded-lg px-3 py-2 text-xs font-mono text-white focus:outline-none focus:border-cyan-500/40"
            placeholder="k123abc"
            autoComplete="off"
          />
        </div>
        <div>
          <label className="block text-[11px] uppercase tracking-wider text-white/40 mb-1">OAuth client secret</label>
          <input
            type="password"
            value={clientSecret}
            onChange={(e) => setClientSecret(e.target.value)}
            className="w-full bg-white/[0.04] border border-white/[0.08] rounded-lg px-3 py-2 text-xs font-mono text-white focus:outline-none focus:border-cyan-500/40"
            placeholder="tskey-client-..."
            autoComplete="off"
          />
        </div>
        <Button
          size="sm"
          variant="outline"
          onClick={validate}
          disabled={!clientID || !clientSecret || validating}
          className="text-xs"
        >
          {validating ? 'Validating...' : 'Validate'}
        </Button>
      </div>

      {result && (
        <div className="rounded-lg border border-white/[0.06] bg-black/30 p-3 space-y-2">
          <CheckRow label="Authentication" state={result.checks.authentication} error={result.errors?.authentication} />
          <CheckRow label="Auth-key minting" state={result.checks.auth_key_scope} error={result.errors?.auth_key_scope} />
          <CheckRow label="Device list" state={result.checks.device_list_scope} error={result.errors?.device_list_scope} />
        </div>
      )}

      {allPassed && (
        <div className="space-y-2">
          {result!.tailnets.length > 1 ? (
            <div>
              <label className="block text-[11px] uppercase tracking-wider text-white/40 mb-1">Tailnet</label>
              <select
                value={selectedTailnet}
                onChange={(e) => setSelectedTailnet(e.target.value)}
                className="w-full bg-white/[0.04] border border-white/[0.08] rounded-lg px-3 py-2 text-xs text-white"
              >
                <option value="">Select tailnet...</option>
                {result!.tailnets.map((t) => (
                  <option key={t.name} value={t.name}>{t.display_name ? `${t.display_name} (${t.name})` : t.name}</option>
                ))}
              </select>
            </div>
          ) : (
            <p className="text-xs text-white/50">
              Tailnet: <span className="font-mono text-white/80">{selectedTailnet || result!.tailnets[0]?.name}</span>
            </p>
          )}
          <Button
            size="sm"
            onClick={connect}
            disabled={!selectedTailnet || connecting}
            className="text-xs bg-cyan-600 hover:bg-cyan-500 text-white"
          >
            {connecting ? 'Connecting...' : 'Connect'}
          </Button>
        </div>
      )}
    </div>
  )
}

function CheckRow({ label, state, error }: { label: string; state: CheckState; error?: string }) {
  const icon =
    state === 'ok' ? (
      <CheckCircle className="w-4 h-4 text-emerald-400" />
    ) : state === 'failed' ? (
      <X className="w-4 h-4 text-rose-400" />
    ) : (
      <div className="w-4 h-4 rounded-full border border-white/20" />
    )
  return (
    <div className="space-y-0.5">
      <div className="flex items-center gap-2 text-xs">
        {icon}
        <span className={state === 'failed' ? 'text-rose-300' : 'text-white/80'}>{label}</span>
      </div>
      {state === 'failed' && error && (
        <p className="ml-6 text-[11px] text-rose-300/80 leading-snug">{error}</p>
      )}
    </div>
  )
}

function ByoPanel() {
  return (
    <div className="rounded-xl border border-white/[0.06] bg-white/[0.02] p-4 text-xs text-white/55 leading-relaxed">
      The install script will continue to accept <code className="text-white/80">TAILSCALE_AUTH_KEY</code> from the operator&apos;s
      environment variables, same as today. VaporwareRMM doesn&apos;t manage the Tailscale connection in this mode.
      Generate auth keys at{' '}
      <a
        href="https://login.tailscale.com/admin/settings/keys"
        target="_blank"
        rel="noopener noreferrer"
        className="text-cyan-300 hover:text-cyan-200 underline"
      >
        tailscale.com/admin/settings/keys
      </a>
      .
    </div>
  )
}

function SkipPanel() {
  const [ack, setAck] = useState(false)
  return (
    <div className="rounded-xl border border-amber-500/30 bg-amber-500/[0.05] p-4 space-y-3">
      <div className="flex items-start gap-2">
        <AlertTriangle className="w-4 h-4 text-amber-400 shrink-0 mt-0.5" />
        <p className="text-xs text-amber-200/90 leading-relaxed">
          Without Tailscale, agent and Sunshine traffic ride the public internet. Sunshine remote desktop will not work
          unless you expose additional ports — and the listener has no built-in auth, so exposing it publicly is unsafe.
          This trades security for operational simplicity. Only choose this for deployments fully on a private network
          you already control.
        </p>
      </div>
      <label className="flex items-center gap-2 text-xs text-white/70">
        <input type="checkbox" checked={ack} onChange={(e) => setAck(e.target.checked)} />
        I understand the trade-off.
      </label>
      {!ack && <p className="text-[11px] text-white/30">Acknowledge before continuing.</p>}
    </div>
  )
}
