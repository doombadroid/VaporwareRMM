'use client'

import { useEffect, useState } from 'react'
import { Button } from '@/components/ui/button'
import {
  Monitor,
  X,
  ExternalLink,
  Play,
  Download,
  Loader2,
  Globe,
  Info,
} from 'lucide-react'
import { toast } from 'sonner'
import { devices as devicesApi } from '@/lib/api'
import type { Device, SunshineStatus } from '@/lib/api'

interface RemoteControlModalProps {
  isOpen: boolean
  onClose: () => void
  device: Device | null
}

export default function RemoteControlModal({
  isOpen,
  onClose,
  device,
}: RemoteControlModalProps) {
  const [sunshineStatus, setSunshineStatus] = useState<SunshineStatus | null>(
    null
  )
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [installing, setInstalling] = useState(false)

  useEffect(() => {
    if (isOpen && device) {
      fetchSunshineStatus()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isOpen, device])

  const fetchSunshineStatus = async () => {
    if (!device) return
    setLoading(true)
    setError(null)
    try {
      const status = await devicesApi.getSunshineStatus(device.id)
      setSunshineStatus(status)
    } catch {
      toast.error('Failed to check Sunshine status')
      setError('Failed to check Sunshine status')
    } finally {
      setLoading(false)
    }
  }

  const handleInstallSunshine = async () => {
    if (!device) return
    setInstalling(true)
    setError(null)
    try {
      await devicesApi.installSunshine(device.id)
      setTimeout(() => fetchSunshineStatus(), 5000)
    } catch {
      toast.error('Failed to send Sunshine install command')
      setError('Failed to send Sunshine install command')
    } finally {
      setInstalling(false)
    }
  }

  const handleConnect = () => {
    if (!sunshineStatus) return
    window.open(sunshineStatus.web_url, '_blank')
  }

  const handleMoonlightConnect = () => {
    if (!sunshineStatus) return
    window.location.href = sunshineStatus.moonlight_url
  }

  const handleMoonlightWebConnect = () => {
    if (!sunshineStatus?.moonlight_web_url) return
    window.open(sunshineStatus.moonlight_web_url, '_blank')
  }

  if (!isOpen) return null

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm"
      onClick={onClose}
    >
      <div
        className="bg-[#0a0a10] border border-white/[0.08] rounded-2xl shadow-2xl max-w-md w-full mx-4"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between p-6 border-b border-white/[0.06]">
          <div>
            <h2 className="text-lg font-semibold text-white">
              Remote Control
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
                Checking Sunshine status...
              </span>
            </div>
          ) : error ? (
            <div className="p-4 bg-rose-500/10 border border-rose-500/20 rounded-xl">
              <p className="text-sm text-rose-400">{error}</p>
              <Button
                size="sm"
                variant="outline"
                className="mt-2 text-xs border-rose-500/20 text-rose-400 hover:bg-rose-500/10"
                onClick={fetchSunshineStatus}
              >
                Retry
              </Button>
            </div>
          ) : sunshineStatus ? (
            <div className="space-y-4">
              <div className="p-4 bg-white/[0.03] rounded-xl border border-white/[0.06]">
                <div className="flex items-center gap-3">
                  <div className="w-10 h-10 rounded-lg bg-cyan-500/10 flex items-center justify-center">
                    <Monitor className="w-6 h-6 text-cyan-400" />
                  </div>
                  <div>
                    <p className="text-sm font-medium text-white">
                      {sunshineStatus.hostname}
                    </p>
                    <p className="text-xs text-white/30">
                      {sunshineStatus.device_ip}
                    </p>
                  </div>
                </div>
              </div>

              <div className="space-y-3">
                <h3 className="text-sm font-medium text-white/60">
                  Sunshine Status
                </h3>
                {sunshineStatus.sunshine ? (
                  <>
                    <div className="flex items-center justify-between p-3 bg-white/[0.02] rounded-lg border border-white/[0.04]">
                      <span className="text-sm text-white/40">Installed</span>
                      <span
                        className={`text-sm font-medium ${
                          sunshineStatus.sunshine.installed
                            ? 'text-emerald-400'
                            : 'text-rose-400'
                        }`}
                      >
                        {sunshineStatus.sunshine.installed ? 'Yes' : 'No'}
                      </span>
                    </div>
                    <div className="flex items-center justify-between p-3 bg-white/[0.02] rounded-lg border border-white/[0.04]">
                      <span className="text-sm text-white/40">Running</span>
                      <span
                        className={`text-sm font-medium ${
                          sunshineStatus.sunshine.running
                            ? 'text-emerald-400'
                            : 'text-rose-400'
                        }`}
                      >
                        {sunshineStatus.sunshine.running ? 'Yes' : 'No'}
                      </span>
                    </div>
                    <div className="flex items-center justify-between p-3 bg-white/[0.02] rounded-lg border border-white/[0.04]">
                      <span className="text-sm text-white/40">Port</span>
                      <span className="text-sm font-mono text-white/60">
                        {sunshineStatus.sunshine.port}
                      </span>
                    </div>
                  </>
                ) : (
                  <div className="p-4 bg-amber-500/10 border border-amber-500/20 rounded-xl">
                    <p className="text-sm text-amber-400">
                      Sunshine is not installed on this device
                    </p>
                  </div>
                )}
              </div>

              {sunshineStatus.sunshine?.running ? (
                <div className="space-y-3">
                  {/*
                    Sunshine pairing is host-side, not dashboard-initiated.
                    The earlier in-modal pair flow called server endpoints
                    that pushed to the agent's HTTP listener with the
                    wrong-shaped bearer (server holds a SHA-256 hash;
                    agent compares against plaintext) and 401'd every
                    time. Server commit 3ff3923 removed those endpoints;
                    this panel replaces the broken UI. See README.md
                    "Agent trust model" callout.
                    TODO(docs): once docs/REMOTE_DESKTOP.md ships, link
                    it from here instead of the README anchor.
                  */}
                  <div className="p-4 bg-white/[0.02] border border-white/[0.06] rounded-xl space-y-2">
                    <div className="flex items-center gap-2">
                      <Info className="w-4 h-4 text-cyan-400 shrink-0" />
                      <p className="text-sm text-white/85 font-medium">Pair Moonlight host-side</p>
                    </div>
                    <p className="text-xs text-white/55 leading-relaxed">
                      Open Sunshine&apos;s Web UI on the device (the Web UI button below), enter the PIN that Moonlight shows. Dashboard-initiated pairing was removed — the server can&apos;t present the agent&apos;s bearer token, so it could never complete the pair.
                    </p>
                  </div>

                  <div className="grid grid-cols-2 gap-3">
                    <Button
                      className="bg-cyan-600 hover:bg-cyan-700 text-white"
                      onClick={handleConnect}
                    >
                      <ExternalLink className="w-4 h-4 mr-2" />
                      Web UI
                    </Button>
                    <Button
                      variant="outline"
                      className="border-white/[0.08] text-white/80 hover:bg-white/[0.04]"
                      onClick={handleMoonlightConnect}
                    >
                      <Play className="w-4 h-4 mr-2" />
                      Moonlight
                    </Button>
                  </div>
                  {sunshineStatus.moonlight_web_url && (
                    <Button
                      className="w-full bg-indigo-600 hover:bg-indigo-700 text-white"
                      onClick={handleMoonlightWebConnect}
                    >
                      <Globe className="w-4 h-4 mr-2" />
                      Moonlight Web Stream
                    </Button>
                  )}
                </div>
              ) : sunshineStatus.sunshine?.installed ? (
                <div className="p-4 bg-amber-500/10 border border-amber-500/20 rounded-xl">
                  <p className="text-sm text-amber-400 mb-3">
                    Sunshine is installed but not running
                  </p>
                  <p className="text-xs text-white/30">
                    Please start Sunshine on the device to enable remote
                    control
                  </p>
                </div>
              ) : (
                <Button
                  className="w-full bg-emerald-600 hover:bg-emerald-700 text-white"
                  onClick={handleInstallSunshine}
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
                      Install Sunshine
                    </>
                  )}
                </Button>
              )}
            </div>
          ) : null}
        </div>
      </div>
    </div>
  )
}
