'use client'

import { useEffect, useState } from 'react'
import { Button } from '@/components/ui/button'
import {
  Link as LinkIcon,
  X,
  Copy,
  Loader2,
  Lock,
  Download,
} from 'lucide-react'
import { toast } from 'sonner'
import { devices as devicesApi } from '@/lib/api'
import type { Device, TailscaleStatus } from '@/lib/api'

interface TailscaleModalProps {
  isOpen: boolean
  onClose: () => void
  device: Device | null
}

export default function TailscaleModal({
  isOpen,
  onClose,
  device,
}: TailscaleModalProps) {
  const [tailscaleStatus, setTailscaleStatus] = useState<TailscaleStatus | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [installing, setInstalling] = useState(false)
  const [authKey, setAuthKey] = useState<string | null>(null)
  const [generatingKey, setGeneratingKey] = useState(false)
  const [exitNode, setExitNode] = useState(false)

  useEffect(() => {
    if (isOpen && device) {
      fetchTailscaleStatus()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isOpen, device])

  const fetchTailscaleStatus = async () => {
    if (!device) return
    setLoading(true)
    setError(null)
    try {
      const status = await devicesApi.getTailscaleStatus(device.id)
      setTailscaleStatus(status)
    } catch {
      toast.error('Failed to check Tailscale status')
      setError('Failed to check Tailscale status')
    } finally {
      setLoading(false)
    }
  }

  const handleInstallTailscale = async () => {
    if (!device) return
    setInstalling(true)
    setError(null)
    try {
      await devicesApi.installTailscale(device.id, { exit_node: exitNode })
      setTimeout(() => fetchTailscaleStatus(), 5000)
    } catch {
      toast.error('Failed to send Tailscale install command')
      setError('Failed to send Tailscale install command')
    } finally {
      setInstalling(false)
    }
  }

  const handleGenerateAuthKey = async () => {
    if (!device) return
    setGeneratingKey(true)
    setError(null)
    try {
      const response = await devicesApi.generateTailscaleAuthKey(device.id, {
        reusable: true,
        ephemeral: true,
      })
      setAuthKey(response.auth_key)
    } catch {
      toast.error(
        'Failed to generate auth key. Ensure Tailscale CLI is installed on server.'
      )
      setError(
        'Failed to generate auth key. Ensure Tailscale CLI is installed on server.'
      )
    } finally {
      setGeneratingKey(false)
    }
  }

  const copyAuthKey = () => {
    if (authKey) {
      navigator.clipboard.writeText(authKey)
    }
  }

  if (!isOpen) return null

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm"
      onClick={onClose}
    >
      <div
        className="bg-[#0a0a10] border border-white/[0.08] rounded-2xl shadow-2xl max-w-lg w-full mx-4"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between p-6 border-b border-white/[0.06]">
          <div>
            <h2 className="text-lg font-semibold text-white">
              Tailscale Management
            </h2>
            <p className="text-sm text-white/40">
              {device?.hostname || 'Select a device'}
            </p>
          </div>
          <button
            onClick={onClose}
            className="text-white/40 hover:text-white transition-colors"
          >
            <X className="w-5 h-5" />
          </button>
        </div>

        <div className="p-6 space-y-4">
          {loading ? (
            <div className="flex items-center justify-center py-8">
              <Loader2 className="w-8 h-8 text-cyan-400 animate-spin" />
              <span className="ml-3 text-white/40">
                Checking Tailscale status...
              </span>
            </div>
          ) : error ? (
            <div className="p-4 bg-rose-500/10 border border-rose-500/20 rounded-xl">
              <p className="text-sm text-rose-400">{error}</p>
              <Button
                size="sm"
                variant="outline"
                className="mt-2 text-xs border-rose-500/20 text-rose-400 hover:bg-rose-500/10"
                onClick={fetchTailscaleStatus}
              >
                Retry
              </Button>
            </div>
          ) : tailscaleStatus ? (
            <div className="space-y-4">
              <div className="p-4 bg-white/[0.03] rounded-xl border border-white/[0.06]">
                <div className="flex items-center gap-3">
                  <div className="w-10 h-10 rounded-lg bg-violet-500/10 flex items-center justify-center">
                    <LinkIcon className="w-6 h-6 text-violet-400" />
                  </div>
                  <div>
                    <p className="text-sm font-medium text-white">
                      {tailscaleStatus.hostname}
                    </p>
                    <p className="text-xs text-white/30">
                      {tailscaleStatus.device_ip}
                    </p>
                  </div>
                </div>
              </div>

              <div className="space-y-3">
                <h3 className="text-sm font-medium text-white/60">
                  Tailscale Status
                </h3>
                {tailscaleStatus.tailscale ? (
                  <>
                    <div className="flex items-center justify-between p-3 bg-white/[0.02] rounded-lg border border-white/[0.04]">
                      <span className="text-sm text-white/40">Installed</span>
                      <span
                        className={`text-sm font-medium ${
                          tailscaleStatus.tailscale.installed
                            ? 'text-emerald-400'
                            : 'text-rose-400'
                        }`}
                      >
                        {tailscaleStatus.tailscale.installed ? 'Yes' : 'No'}
                      </span>
                    </div>
                    <div className="flex items-center justify-between p-3 bg-white/[0.02] rounded-lg border border-white/[0.04]">
                      <span className="text-sm text-white/40">Connected</span>
                      <span
                        className={`text-sm font-medium ${
                          tailscaleStatus.tailscale.connected
                            ? 'text-emerald-400'
                            : 'text-rose-400'
                        }`}
                      >
                        {tailscaleStatus.tailscale.connected ? 'Yes' : 'No'}
                      </span>
                    </div>
                    {tailscaleStatus.tailscale.ip && (
                      <div className="flex items-center justify-between p-3 bg-white/[0.02] rounded-lg border border-white/[0.04]">
                        <span className="text-sm text-white/40">
                          Tailscale IP
                        </span>
                        <span className="text-sm font-mono text-white/60">
                          {tailscaleStatus.tailscale.ip}
                        </span>
                      </div>
                    )}
                    {tailscaleStatus.tailscale.peers !== undefined && (
                      <div className="flex items-center justify-between p-3 bg-white/[0.02] rounded-lg border border-white/[0.04]">
                        <span className="text-sm text-white/40">
                          Connected Peers
                        </span>
                        <span className="text-sm font-mono text-white/60">
                          {tailscaleStatus.tailscale.peers}
                        </span>
                      </div>
                    )}
                  </>
                ) : (
                  <div className="p-4 bg-amber-500/10 border border-amber-500/20 rounded-xl">
                    <p className="text-sm text-amber-400">
                      Tailscale is not installed on this device
                    </p>
                  </div>
                )}
              </div>

              {authKey ? (
                <div className="p-4 bg-emerald-500/10 border border-emerald-500/20 rounded-xl">
                  <p className="text-sm text-emerald-400 mb-2">
                    Auth Key Generated
                  </p>
                  <div className="flex items-center gap-2">
                    <code className="flex-1 bg-black/30 px-3 py-2 rounded-lg text-xs font-mono text-white/50 break-all">
                      {authKey}
                    </code>
                    <Button
                      size="sm"
                      variant="ghost"
                      className="text-white/40 hover:text-white"
                      onClick={copyAuthKey}
                    >
                      <Copy className="w-4 h-4" />
                    </Button>
                  </div>
                </div>
              ) : (
                <Button
                  variant="outline"
                  className="w-full border-white/[0.08] text-white/80 hover:bg-white/[0.04]"
                  onClick={handleGenerateAuthKey}
                  disabled={generatingKey}
                >
                  {generatingKey ? (
                    <>
                      <Loader2 className="w-4 h-4 animate-spin mr-2" />
                      Generating auth key...
                    </>
                  ) : (
                    <>
                      <Lock className="w-4 h-4 mr-2" />
                      Generate Auth Key
                    </>
                  )}
                </Button>
              )}

              {tailscaleStatus.tailscale?.connected ? (
                <div className="p-4 bg-emerald-500/10 border border-emerald-500/20 rounded-xl">
                  <p className="text-sm text-emerald-400">
                    Tailscale is connected and operational
                  </p>
                  <p className="text-xs text-white/30 mt-1">
                    Device is accessible via Tailscale network
                  </p>
                </div>
              ) : tailscaleStatus.tailscale?.installed ? (
                <div className="p-4 bg-amber-500/10 border border-amber-500/20 rounded-xl">
                  <p className="text-sm text-amber-400 mb-3">
                    Tailscale is installed but not connected
                  </p>
                  <p className="text-xs text-white/30">
                    Please authenticate Tailscale on the device
                  </p>
                </div>
              ) : (
                <div className="space-y-3">
                  <div className="flex items-center gap-2">
                    <input
                      type="checkbox"
                      id="exit-node"
                      checked={exitNode}
                      onChange={(e) => setExitNode(e.target.checked)}
                      className="rounded border-white/20 bg-white/5"
                    />
                    <label
                      htmlFor="exit-node"
                      className="text-sm text-white/60"
                    >
                      Advertise as exit node
                    </label>
                  </div>
                  <Button
                    className="w-full bg-violet-600 hover:bg-violet-700 text-white"
                    onClick={handleInstallTailscale}
                    disabled={installing}
                  >
                    {installing ? (
                      <>
                        <Loader2 className="w-4 h-4 animate-spin mr-2" />
                        Sending install command...
                      </>
                    ) : (
                      <>
                        <Download className="w-4 h-4 mr-2" />
                        Install Tailscale
                      </>
                    )}
                  </Button>
                </div>
              )}
            </div>
          ) : null}
        </div>
      </div>
    </div>
  )
}
