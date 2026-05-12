'use client'

import { useEffect, useState } from 'react'
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
import { brandAppNameError, slugifyAppName } from '@/lib/utils'

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
  // appNameTouched stays false until the user types directly into
  // the Internal Identifier field. While false, Company Name edits
  // auto-fill the identifier via slugifyAppName so the common case
  // (operator types their real company name once) ends with a valid
  // slug without them ever touching the technical field. Clearing
  // the identifier resets the flag so they can re-engage the sync.
  const [appNameTouched, setAppNameTouched] = useState(false)
  const appNameError = brandAppNameError(localBranding.app_name)
  // Origin set in effect so SSR doesn't bake a stale build-time API URL into
  // the install command. window.location.origin is whatever the operator's
  // dashboard is actually reachable at — that's what the curl command needs.
  const [origin, setOrigin] = useState('')

  useEffect(() => {
    setOrigin(window.location.origin)
  }, [])

  if (!open) return null

  const installCmd = origin
    ? `curl -fsSL ${origin}/api/branding/agent-install?format=script | sudo bash -s -- --server ${origin}`
    : ''

  const steps = [
    { title: 'Welcome', icon: Sparkles },
    { title: 'Branding', icon: Palette },
    { title: 'Install Agent', icon: Server },
    { title: 'AI (optional)', icon: Sparkles },
    { title: 'Complete', icon: CheckCircle },
  ]

  // Returns true on success so callers (next()) don't advance the step on a
  // save failure (e.g. non-admin user without branding write permission).
  const handleSaveBranding = async (): Promise<boolean> => {
    if (appNameError) {
      toast.error(`Internal identifier: ${appNameError}`)
      return false
    }
    setSaving(true)
    try {
      await brandingApi.update(localBranding)
      setBranding(localBranding)
      toast.success('Branding saved')
      return true
    } catch {
      toast.error('Failed to save branding (admin only?)')
      return false
    } finally {
      setSaving(false)
    }
  }

  const onCompanyNameChange = (value: string) => {
    setLocalBranding((prev) => {
      const next = { ...prev, company_name: value }
      if (!appNameTouched) {
        const slug = slugifyAppName(value)
        if (slug) next.app_name = slug
      }
      return next
    })
  }

  const onAppNameChange = (value: string) => {
    setLocalBranding((prev) => ({ ...prev, app_name: value }))
    if (value === '') {
      // Re-engage the company→app_name auto-sync. Treating a cleared
      // field as "give me the default again" matches what users
      // actually mean when they wipe an identifier they don't like.
      setAppNameTouched(false)
    } else {
      setAppNameTouched(true)
    }
  }

  const copyInstallCmd = () => {
    if (!installCmd) return
    navigator.clipboard.writeText(installCmd)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
    toast.success('Command copied to clipboard')
  }

  const finish = () => {
    localStorage.setItem('setup_completed', 'true')
    onClose()
  }

  const next = async () => {
    if (step === 1) {
      const ok = await handleSaveBranding()
      if (!ok) return
    }
    setStep((s) => Math.min(s + 1, steps.length - 1))
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
                <p className="text-xs text-white/40 uppercase tracking-wider font-medium">What we&apos;ll set up</p>
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
                <label className="block text-sm font-medium text-white mb-1">
                  Company name
                </label>
                <input
                  type="text"
                  value={localBranding.company_name}
                  onChange={(e) => onCompanyNameChange(e.target.value)}
                  className="w-full bg-white/[0.04] border border-white/[0.08] rounded-lg px-3 py-2 text-sm text-white placeholder:text-white/20 focus:outline-none focus:border-cyan-500/40"
                  placeholder="Smith & Jones IT"
                />
                <p className="mt-1 text-xs text-white/40">
                  Shown to clients in the dashboard, install scripts, and
                  agent installer header.
                </p>
              </div>

              <div className="opacity-90">
                <label className="block text-xs font-medium text-white/50 mb-1">
                  Internal identifier
                </label>
                <input
                  type="text"
                  value={localBranding.app_name}
                  onChange={(e) => onAppNameChange(e.target.value)}
                  className={`w-full bg-white/[0.02] border rounded-lg px-3 py-1.5 text-xs font-mono text-white/80 placeholder:text-white/20 focus:outline-none ${
                    appNameError
                      ? 'border-rose-500/50 focus:border-rose-400/70'
                      : 'border-white/[0.06] focus:border-cyan-500/30'
                  }`}
                  placeholder="auto-generated from company name"
                  aria-invalid={appNameError ? 'true' : 'false'}
                  aria-describedby="app-name-help app-name-error"
                />
                <p id="app-name-help" className="mt-1 text-[11px] text-white/30 leading-snug">
                  Used for the systemd service name and file paths.
                  Letters, numbers, dashes, underscores only.
                  Auto-generated from company name if left blank.
                </p>
                {appNameError && (
                  <p
                    id="app-name-error"
                    className="mt-1 text-[11px] text-rose-400 leading-snug"
                    role="alert"
                  >
                    {appNameError}
                  </p>
                )}
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
                  {installCmd || '…'}
                </code>
                <div className="flex gap-2">
                  <Button size="sm" variant="outline" className="text-xs" onClick={copyInstallCmd} disabled={!installCmd}>
                    {copied ? <CheckCircle className="w-3.5 h-3.5 mr-1 text-emerald-400" /> : <Copy className="w-3.5 h-3.5 mr-1" />}
                    {copied ? 'Copied' : 'Copy'}
                  </Button>
                  <Button size="sm" variant="outline" className="text-xs" asChild disabled={!origin}>
                    <a href={origin ? `${origin}/api/branding/agent-install?format=script` : '#'} download>
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
            <div className="space-y-5">
              <div className="space-y-2">
                <h3 className="text-lg font-semibold text-white">AI agent layer (optional)</h3>
                <p className="text-sm text-white/50 leading-relaxed">
                  vaporRMM ships an opt-in AI layer for alert deduplication, ticket triage,
                  natural-language fleet search, and (later) auto-remediation. You bring
                  your own API keys (OpenAI, Anthropic, Google, xAI, Mistral, or self-hosted
                  Ollama / vLLM). Nothing runs until a super_admin enables AI for a tenant.
                </p>
              </div>
              <div className="bg-white/[0.03] rounded-xl p-4 space-y-2 border border-white/[0.06] text-sm text-white/60">
                <p className="text-xs text-white/40 uppercase tracking-wider font-medium">Notes</p>
                <ul className="space-y-1 list-disc list-inside">
                  <li>AI features need PostgreSQL (the SQLite default doesn&apos;t support pgvector).</li>
                  <li>Every capability defaults to <code className="text-white/70">shadow</code> mode and can&apos;t act until you promote it.</li>
                  <li>Tenant data leaves your network for whichever provider you choose. Verify your DPAs.</li>
                </ul>
              </div>
              <p className="text-xs text-white/30">
                Skip this for now and configure it from the <strong>AI</strong> nav entry whenever you&apos;re ready.
              </p>
            </div>
          )}

          {step === 4 && (
            <div className="space-y-4 text-center">
              <div className="w-20 h-20 rounded-full bg-emerald-500/10 flex items-center justify-center mx-auto border border-emerald-500/20">
                <CheckCircle className="w-10 h-10 text-emerald-400" />
              </div>
              <h3 className="text-xl font-bold text-white">You&apos;re All Set!</h3>
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
            <Button
              onClick={next}
              disabled={saving || (step === 1 && !!appNameError)}
              className="text-sm bg-cyan-600 hover:bg-cyan-500 text-white"
            >
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
