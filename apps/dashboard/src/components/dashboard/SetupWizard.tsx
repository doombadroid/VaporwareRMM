'use client'

import { useState } from 'react'
import { Button } from '@/components/ui/button'
import {
  X,
  Sparkles,
  Palette,
  Server,
  CheckCircle,
  ChevronRight,
  ChevronLeft,
  Copy,
  ExternalLink,
} from 'lucide-react'
import { toast } from 'sonner'
import { branding as brandingApi, type BrandingConfig } from '@/lib/api'
import { useBranding } from '@/components/BrandingProvider'

interface SetupWizardProps {
  open: boolean
  onClose: () => void
}

export default function SetupWizard({ open, onClose }: SetupWizardProps) {
  const [step, setStep] = useState(0)
  const { branding, setBranding } = useBranding()
  const [localBranding, setLocalBranding] = useState<BrandingConfig>(branding)
  const [saving, setSaving] = useState(false)
  const [copied, setCopied] = useState(false)

  if (!open) return null

  const apiBase = typeof window !== 'undefined' ? (process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8080/api').replace('/api', '') : ''
  const installCmd = `curl -fsSL ${apiBase}/api/branding/agent-install?format=script | sudo bash -s -- --server ${apiBase}`

  const steps = [
    { title: 'Welcome', icon: Sparkles },
    { title: 'Branding', icon: Palette },
    { title: 'Install Agent', icon: Server },
    { title: 'Complete', icon: CheckCircle },
  ]

  const handleSaveBranding = async () => {
    setSaving(true)
    try {
      await brandingApi.update(localBranding)
      setBranding(localBranding)
      toast.success('Branding saved')
    } catch {
      toast.error('Failed to save branding')
    } finally {
      setSaving(false)
    }
  }

  const copyInstallCmd = () => {
    navigator.clipboard.writeText(installCmd)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
    toast.success('Command copied to clipboard')
  }

  const finish = () => {
    localStorage.setItem('setup_completed', 'true')
    onClose()
  }

  const next = () => {
    if (step === 1) {
      handleSaveBranding().then(() => setStep((s) => Math.min(s + 1, steps.length - 1)))
    } else {
      setStep((s) => Math.min(s + 1, steps.length - 1))
    }
  }

  const prev = () => setStep((s) => Math.max(s - 1, 0))

  return (
    <div className="fixed inset-0 z-[60] flex items-center justify-center bg-black/80 backdrop-blur-sm">
      <div className="bg-[#0a0a10] border border-white/[0.08] rounded-2xl shadow-2xl max-w-lg w-full mx-4 max-h-[85vh] overflow-y-auto">
        {/* Header */}
        <div className="flex items-center justify-between p-6 border-b border-white/[0.06]">
          <div className="flex items-center gap-3">
            <div className="w-10 h-10 rounded-lg bg-gradient-to-br from-cyan-500 to-violet-600 flex items-center justify-center">
              <Sparkles className="w-5 h-5 text-white" />
            </div>
            <div>
              <h2 className="text-lg font-semibold text-white">Setup Wizard</h2>
              <p className="text-xs text-white/40">
                Step {step + 1} of {steps.length}: {steps[step].title}
              </p>
            </div>
          </div>
          <button onClick={onClose} className="text-white/40 hover:text-white transition-colors">
            <X className="w-5 h-5" />
          </button>
        </div>

        {/* Progress */}
        <div className="px-6 pt-4">
          <div className="flex gap-2">
            {steps.map((s, i) => (
              <div key={i} className="flex-1 flex items-center gap-2">
                <div
                  className={`w-8 h-8 rounded-full flex items-center justify-center text-xs font-bold transition-colors ${
                    i <= step
                      ? 'bg-cyan-500/20 text-cyan-400 border border-cyan-500/30'
                      : 'bg-white/5 text-white/30 border border-white/10'
                  }`}
                >
                  {i < step ? <CheckCircle className="w-4 h-4" /> : i + 1}
                </div>
                {i < steps.length - 1 && (
                  <div
                    className={`flex-1 h-0.5 rounded transition-colors ${
                      i < step ? 'bg-cyan-500/40' : 'bg-white/10'
                    }`}
                  />
                )}
              </div>
            ))}
          </div>
        </div>

        {/* Content */}
        <div className="p-6">
          {step === 0 && (
            <div className="space-y-4 text-center">
              <div className="w-20 h-20 rounded-full bg-gradient-to-br from-cyan-500/20 to-violet-500/20 flex items-center justify-center mx-auto border border-cyan-500/20">
                <Sparkles className="w-10 h-10 text-cyan-400" />
              </div>
              <h3 className="text-xl font-bold text-white">Welcome to vaporRMM</h3>
              <p className="text-sm text-white/50 leading-relaxed">
                This wizard will help you get your RMM platform configured in just a few steps.
                You can always change these settings later from the Settings page.
              </p>
              <div className="bg-white/[0.03] rounded-xl p-4 text-left space-y-2 border border-white/[0.06]">
                <p className="text-xs text-white/40 uppercase tracking-wider font-medium">What we'll set up</p>
                <ul className="space-y-2 text-sm text-white/60">
                  <li className="flex items-center gap-2"><Palette className="w-4 h-4 text-cyan-400" /> Branding & appearance</li>
                  <li className="flex items-center gap-2"><Server className="w-4 h-4 text-violet-400" /> First agent installation</li>
                  <li className="flex items-center gap-2"><CheckCircle className="w-4 h-4 text-emerald-400" /> Ready-to-use dashboard</li>
                </ul>
              </div>
            </div>
          )}

          {step === 1 && (
            <div className="space-y-5">
              <p className="text-sm text-white/50">
                Customize how your RMM appears to clients and in the dashboard.
              </p>

              <div>
                <label className="block text-sm font-medium text-white/60 mb-1">App Name</label>
                <input
                  type="text"
                  value={localBranding.app_name}
                  onChange={(e) => setLocalBranding({ ...localBranding, app_name: e.target.value })}
                  className="w-full bg-white/[0.04] border border-white/[0.08] rounded-lg px-3 py-2 text-sm text-white placeholder:text-white/20 focus:outline-none focus:border-cyan-500/40"
                  placeholder="vaporRMM"
                />
              </div>

              <div>
                <label className="block text-sm font-medium text-white/60 mb-1">Company Name</label>
                <input
                  type="text"
                  value={localBranding.company_name}
                  onChange={(e) => setLocalBranding({ ...localBranding, company_name: e.target.value })}
                  className="w-full bg-white/[0.04] border border-white/[0.08] rounded-lg px-3 py-2 text-sm text-white placeholder:text-white/20 focus:outline-none focus:border-cyan-500/40"
                  placeholder="Your Company"
                />
              </div>

              <div>
                <label className="block text-sm font-medium text-white/60 mb-1">Primary Color</label>
                <div className="flex items-center gap-2">
                  <input
                    type="color"
                    value={localBranding.primary_color}
                    onChange={(e) => setLocalBranding({ ...localBranding, primary_color: e.target.value })}
                    className="w-10 h-10 rounded-lg cursor-pointer bg-transparent"
                  />
                  <input
                    type="text"
                    value={localBranding.primary_color}
                    onChange={(e) => setLocalBranding({ ...localBranding, primary_color: e.target.value })}
                    className="flex-1 bg-white/[0.04] border border-white/[0.08] rounded-lg px-3 py-2 text-sm text-white font-mono focus:outline-none focus:border-cyan-500/40"
                    placeholder="#3b82f6"
                  />
                </div>
              </div>

              <div className="p-4 bg-white/[0.03] rounded-xl border border-white/[0.06]">
                <p className="text-xs text-white/30 mb-2">Preview</p>
                <div className="flex items-center gap-2">
                  <div
                    className="w-8 h-8 rounded flex items-center justify-center text-sm font-bold text-white"
                    style={{ background: localBranding.primary_color }}
                  >
                    {(localBranding.app_name || 'V').charAt(0).toUpperCase()}
                  </div>
                  <span className="text-sm font-semibold text-white">{localBranding.app_name}</span>
                </div>
              </div>
            </div>
          )}

          {step === 2 && (
            <div className="space-y-5">
              <p className="text-sm text-white/50">
                Install your first agent by running this command on any Linux device:
              </p>

              <div className="bg-black/40 rounded-xl border border-white/[0.08] p-4 space-y-3">
                <code className="block text-xs font-mono text-white/60 break-all leading-relaxed">
                  {installCmd}
                </code>
                <div className="flex gap-2">
                  <Button size="sm" variant="outline" className="text-xs" onClick={copyInstallCmd}>
                    {copied ? <CheckCircle className="w-3.5 h-3.5 mr-1 text-emerald-400" /> : <Copy className="w-3.5 h-3.5 mr-1" />}
                    {copied ? 'Copied' : 'Copy'}
                  </Button>
                  <Button size="sm" variant="outline" className="text-xs" asChild>
                    <a href={`${apiBase}/api/branding/agent-install?format=script`} download>
                      <ExternalLink className="w-3.5 h-3.5 mr-1" />
                      Download Script
                    </a>
                  </Button>
                </div>
              </div>

              <div className="bg-cyan-500/5 border border-cyan-500/20 rounded-xl p-4">
                <p className="text-sm text-cyan-400 font-medium mb-1">Alternative: Manual Registration</p>
                <p className="text-xs text-white/40">
                  You can also register agents manually from the Agents page or use the Settings {'>'} Agents tab
                  for platform-specific install options.
                </p>
              </div>

              <p className="text-xs text-white/30">
                After installation, the device will appear in your Agents list automatically.
              </p>
            </div>
          )}

          {step === 3 && (
            <div className="space-y-4 text-center">
              <div className="w-20 h-20 rounded-full bg-emerald-500/10 flex items-center justify-center mx-auto border border-emerald-500/20">
                <CheckCircle className="w-10 h-10 text-emerald-400" />
              </div>
              <h3 className="text-xl font-bold text-white">You're All Set!</h3>
              <p className="text-sm text-white/50 leading-relaxed">
                Your RMM dashboard is configured and ready. You can now manage devices,
                send commands, and monitor your fleet from one place.
              </p>
              <div className="bg-white/[0.03] rounded-xl p-4 text-left space-y-2 border border-white/[0.06]">
                <p className="text-xs text-white/40 uppercase tracking-wider font-medium">Quick Tips</p>
                <ul className="space-y-2 text-sm text-white/60">
                  <li className="flex items-center gap-2"><Server className="w-4 h-4 text-cyan-400" /> Go to Agents to view registered devices</li>
                  <li className="flex items-center gap-2"><Palette className="w-4 h-4 text-violet-400" /> Change branding anytime in Settings</li>
                  <li className="flex items-center gap-2"><Sparkles className="w-4 h-4 text-amber-400" /> Reopen this wizard from Quick Actions</li>
                </ul>
              </div>
            </div>
          )}
        </div>

        {/* Footer */}
        <div className="flex items-center justify-between p-6 border-t border-white/[0.06]">
          <Button variant="ghost" onClick={prev} disabled={step === 0} className="text-sm">
            <ChevronLeft className="w-4 h-4 mr-1" />
            Back
          </Button>

          {step < steps.length - 1 ? (
            <Button onClick={next} disabled={saving} className="text-sm bg-cyan-600 hover:bg-cyan-500 text-white">
              {saving ? 'Saving...' : 'Next'}
              <ChevronRight className="w-4 h-4 ml-1" />
            </Button>
          ) : (
            <Button onClick={finish} className="text-sm bg-emerald-600 hover:bg-emerald-500 text-white">
              <CheckCircle className="w-4 h-4 mr-1" />
              Finish
            </Button>
          )}
        </div>
      </div>
    </div>
  )
}
