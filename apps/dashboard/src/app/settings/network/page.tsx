'use client'

// Settings → Network page. Phase 1 of Tailscale integration
// (issue #18). Super-admin-gated UI for managing the global
// Tailscale connection.
//
// Per design Q3 from issue #18, tenant admins see only a read-only
// transparency indicator (commit 6) — they cannot reach this page.
// The route checks user_role and renders a 403-style message for
// non-super-admins.

import { useCallback, useEffect, useState } from 'react'
import { Button } from '@/components/ui/button'
import { toast } from 'sonner'
import api from '@/lib/api'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { PageHeader, Section } from '@/components/ui/page'
import { StatusDot } from '@/components/ui/status'
import { ConfirmDialog } from '@/components/ui/sheet'
import { TailscaleConfigPanel } from '@/components/dashboard/NetworkStep'

type Connection = {
  connected: boolean
  tailnet?: string
  tailnet_display_name?: string
  connected_at?: number
  connected_by_user_id?: string
  last_validated_at?: number
  last_validation_error?: string
  rotated_at?: number
}

type Device = {
  name: string
  hostname: string
  addresses: string[]
  os: string
  tags: string[]
  lastSeen: string
}

function relative(ts?: number) {
  if (!ts) return '—'
  const d = new Date(ts * 1000)
  const delta = Math.floor((Date.now() - d.getTime()) / 1000)
  if (delta < 60) return `${delta}s ago`
  if (delta < 3600) return `${Math.floor(delta / 60)}m ago`
  if (delta < 86400) return `${Math.floor(delta / 3600)}h ago`
  return `${Math.floor(delta / 86400)}d ago`
}

export default function NetworkSettingsPage() {
  const [role, setRole] = useState<string>('')
  const [conn, setConn] = useState<Connection | null>(null)
  const [loading, setLoading] = useState(true)
  const [devices, setDevices] = useState<Device[]>([])
  const [devicesLoading, setDevicesLoading] = useState(false)
  const [showRotate, setShowRotate] = useState(false)
  const [showDisconnect, setShowDisconnect] = useState(false)

  useEffect(() => {
    // Resolve current user role for the super-admin gate. The /users/me
    // endpoint returns it directly; if the call fails, fall back to the
    // restrictive default ("you can't see this page").
    api.get<{ role: string }>('/users/me').then(r => setRole(r.data.role)).catch(() => setRole(''))
  }, [])

  const refresh = useCallback(async () => {
    setLoading(true)
    try {
      const { data } = await api.get<Connection>('/tailscale/connection')
      setConn(data)
    } catch {
      setConn({ connected: false })
    } finally {
      setLoading(false)
    }
  }, [])

  const refreshDevices = useCallback(async () => {
    if (!conn?.connected) return
    setDevicesLoading(true)
    try {
      const { data } = await api.get<{ devices: Device[] }>('/tailscale/devices')
      setDevices(data.devices || [])
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : 'failed to load devices'
      toast.error(`Devices: ${msg}`)
    } finally {
      setDevicesLoading(false)
    }
  }, [conn?.connected])

  useEffect(() => { refresh() }, [refresh])
  useEffect(() => { if (conn?.connected) refreshDevices() }, [conn?.connected, refreshDevices])

  const disconnect = async () => {
    try {
      await api.delete('/tailscale/connection')
      toast.success('Tailscale disconnected')
      setShowDisconnect(false)
      refresh()
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : 'failed'
      toast.error(`Disconnect failed: ${msg}`)
    }
  }

  if (role && role !== 'super_admin') {
    return (
      <AuthGuard>
        <DashboardShell>
          <PageHeader title="Network" />
          <Section title="Tailscale">
            <p className="text-sm text-white/55">
              Network configuration is managed by your administrator.
            </p>
          </Section>
        </DashboardShell>
      </AuthGuard>
    )
  }

  return (
    <AuthGuard>
      <DashboardShell>
        <PageHeader
          title="Network"
          description="Tailscale integration — single tailnet across all tenants in v1."
        />

        <Section title="Tailscale connection">
          {loading ? (
            <p className="text-sm text-white/50">Loading…</p>
          ) : conn?.connected ? (
            <div className="space-y-4">
              <div className="flex items-center gap-2">
                <StatusDot tone="online" pulse />
                <span className="text-sm font-medium text-emerald-300">Connected</span>
              </div>
              <dl className="grid grid-cols-1 sm:grid-cols-2 gap-3 text-xs">
                <Field label="Tailnet" value={conn.tailnet || conn.tailnet_display_name || '—'} mono />
                {conn.tailnet_display_name && conn.tailnet_display_name !== conn.tailnet && (
                  <Field label="Display name" value={conn.tailnet_display_name} />
                )}
                <Field label="Connected" value={relative(conn.connected_at)} />
                <Field label="Last validated" value={relative(conn.last_validated_at)} />
                {conn.rotated_at && <Field label="Rotated" value={relative(conn.rotated_at)} />}
                {conn.last_validation_error && (
                  <Field label="Last error" value={conn.last_validation_error} valueClassName="text-rose-300" />
                )}
              </dl>
              <div className="flex gap-2">
                <Button size="sm" variant="outline" onClick={() => setShowRotate(true)}>Rotate credential</Button>
                <Button size="sm" variant="outline" onClick={() => setShowDisconnect(true)}>Disconnect</Button>
              </div>
            </div>
          ) : (
            <div className="space-y-4">
              <div className="flex items-center gap-2">
                <StatusDot tone="offline" />
                <span className="text-sm text-white/55">Not connected</span>
              </div>
              <TailscaleConfigPanel onConnected={refresh} />
            </div>
          )}
        </Section>

        {conn?.connected && (
          <Section
            title="Devices on tailnet"
            description="Live from Tailscale's API. May include devices not managed by VaporwareRMM."
          >
            <div className="flex items-center justify-between mb-3">
              <p className="text-xs text-white/40">
                Total: {devices.length}
              </p>
              <Button size="sm" variant="outline" onClick={refreshDevices} disabled={devicesLoading}>
                {devicesLoading ? 'Refreshing…' : 'Refresh'}
              </Button>
            </div>
            <div className="overflow-x-auto">
              <table className="w-full text-xs">
                <thead>
                  <tr className="text-left text-[10px] uppercase tracking-wider text-white/30">
                    <th className="pb-2">Hostname</th>
                    <th className="pb-2">Tailscale IP</th>
                    <th className="pb-2">OS</th>
                    <th className="pb-2">Tags</th>
                    <th className="pb-2">Last seen</th>
                  </tr>
                </thead>
                <tbody>
                  {devices.map((d, i) => (
                    <tr key={d.name + i} className="border-t border-white/[0.04]">
                      <td className="py-2 font-mono text-white/80">{d.hostname || d.name}</td>
                      <td className="py-2 font-mono text-white/55">{d.addresses?.[0] || '—'}</td>
                      <td className="py-2 text-white/55">{d.os || '—'}</td>
                      <td className="py-2 text-white/55">{d.tags?.join(', ') || '—'}</td>
                      <td className="py-2 text-white/40">{d.lastSeen || '—'}</td>
                    </tr>
                  ))}
                  {devices.length === 0 && !devicesLoading && (
                    <tr><td colSpan={5} className="py-3 text-center text-white/30">No devices reported by Tailscale yet.</td></tr>
                  )}
                </tbody>
              </table>
            </div>
          </Section>
        )}

        <Section title="Future">
          <ul className="text-xs text-white/45 space-y-1 list-disc list-inside">
            <li>
              Per-tenant tailnet isolation is planned for v2 — see{' '}
              <a href="https://github.com/doombadroid/VaporwareRMM/issues/19" target="_blank" rel="noopener noreferrer" className="text-cyan-300 underline">#19</a>
              .
            </li>
            <li>
              Tenant-managed credentials (BYO) are planned for v3 — see{' '}
              <a href="https://github.com/doombadroid/VaporwareRMM/issues/20" target="_blank" rel="noopener noreferrer" className="text-cyan-300 underline">#20</a>
              .
            </li>
            <li>
              v1 cross-tenant isolation is enforced by Tailscale ACLs you author in Tailscale&apos;s
              admin console. See <code className="text-white/70">docs/tailscale-acl-recipe.md</code> for a paste-ready starting config.
            </li>
          </ul>
        </Section>

        {showRotate && conn?.connected && (
          <RotateModal
            tailnet={conn.tailnet || ''}
            onClose={() => setShowRotate(false)}
            onRotated={() => { setShowRotate(false); refresh() }}
          />
        )}
        <ConfirmDialog
          open={showDisconnect}
          onClose={() => setShowDisconnect(false)}
          onConfirm={disconnect}
          title="Disconnect Tailscale"
          description="Existing agent devices will remain on the tailnet (they have their own Tailscale identities). Future agent installs will fall back to bring-your-own mode. Continue?"
          confirmLabel="Disconnect"
        />
      </DashboardShell>
    </AuthGuard>
  )
}

function Field({ label, value, mono, valueClassName }: { label: string; value: string; mono?: boolean; valueClassName?: string }) {
  return (
    <div className="bg-white/[0.02] rounded-lg border border-white/[0.04] px-3 py-2">
      <dt className="text-[10px] uppercase tracking-wider text-white/35">{label}</dt>
      <dd className={`mt-0.5 text-white/85 ${mono ? 'font-mono' : ''} ${valueClassName || ''}`}>{value}</dd>
    </div>
  )
}

function RotateModal({ tailnet, onClose, onRotated }: { tailnet: string; onClose: () => void; onRotated: () => void }) {
  const [clientID, setClientID] = useState('')
  const [clientSecret, setClientSecret] = useState('')
  const [busy, setBusy] = useState(false)

  const rotate = async () => {
    if (!clientID || !clientSecret) return
    setBusy(true)
    try {
      await api.put('/tailscale/connection', { client_id: clientID, client_secret: clientSecret })
      toast.success('Credential rotated')
      onRotated()
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : 'rotate failed'
      toast.error(msg)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm" onClick={onClose}>
      <div className="bg-[#0a0a10] border border-white/[0.08] rounded-2xl shadow-2xl max-w-md w-full mx-4 p-6 space-y-4" onClick={e => e.stopPropagation()}>
        <h3 className="text-sm font-semibold text-white">Rotate Tailscale credential</h3>
        <p className="text-xs text-white/55 leading-relaxed">
          New credential must own the same tailnet (<span className="font-mono text-white/80">{tailnet}</span>).
          Changing tailnets requires Disconnect + Connect, which re-onboards all agents.
        </p>
        <div className="space-y-2">
          <input
            type="text"
            placeholder="New OAuth client ID"
            value={clientID}
            onChange={e => setClientID(e.target.value)}
            className="w-full bg-white/[0.04] border border-white/[0.08] rounded-lg px-3 py-2 text-xs font-mono text-white"
            autoComplete="off"
          />
          <input
            type="password"
            placeholder="New OAuth client secret"
            value={clientSecret}
            onChange={e => setClientSecret(e.target.value)}
            className="w-full bg-white/[0.04] border border-white/[0.08] rounded-lg px-3 py-2 text-xs font-mono text-white"
            autoComplete="off"
          />
        </div>
        <div className="flex justify-end gap-2">
          <Button size="sm" variant="ghost" onClick={onClose}>Cancel</Button>
          <Button size="sm" onClick={rotate} disabled={busy || !clientID || !clientSecret} className="bg-cyan-600 hover:bg-cyan-500 text-white">
            {busy ? 'Rotating…' : 'Rotate'}
          </Button>
        </div>
      </div>
    </div>
  )
}
