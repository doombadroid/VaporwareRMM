import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Search, Clipboard, RefreshCw, BarChart3, Shield, Plus, Palette, Link as LinkIcon, Sparkles } from 'lucide-react'

const colorMap: Record<string, { text: string; bg: string }> = {
  cyan: { text: 'text-cyan-400', bg: 'bg-cyan-500/10' },
  emerald: { text: 'text-emerald-400', bg: 'bg-emerald-500/10' },
  violet: { text: 'text-violet-400', bg: 'bg-violet-500/10' },
  amber: { text: 'text-amber-400', bg: 'bg-amber-500/10' },
  rose: { text: 'text-rose-400', bg: 'bg-rose-500/10' },
  orange: { text: 'text-orange-400', bg: 'bg-orange-500/10' },
  teal: { text: 'text-teal-400', bg: 'bg-teal-500/10' },
  blue: { text: 'text-blue-400', bg: 'bg-blue-500/10' },
}

interface QuickActionsPanelProps {
  onRemoteControl: () => void
  onNewTicket: () => void
  onDeployUpdates: () => void
  onRunReport: () => void
  onScanSecurity: () => void
  onAddDevice: () => void
  onBranding: () => void
  onClientLinks: () => void
  onSetupWizard: () => void
}

export default function QuickActionsPanel({
  onRemoteControl,
  onNewTicket,
  onDeployUpdates,
  onRunReport,
  onScanSecurity,
  onAddDevice,
  onBranding,
  onClientLinks,
  onSetupWizard,
}: QuickActionsPanelProps) {
  const actions = [
    { label: 'Remote Control', icon: Search, color: 'cyan', handler: onRemoteControl },
    { label: 'New Ticket', icon: Clipboard, color: 'emerald', handler: onNewTicket },
    { label: 'Deploy Updates', icon: RefreshCw, color: 'violet', handler: onDeployUpdates },
    { label: 'Run Report', icon: BarChart3, color: 'amber', handler: onRunReport },
    { label: 'Scan Security', icon: Shield, color: 'cyan', handler: onScanSecurity },
    { label: 'Add Device', icon: Plus, color: 'rose', handler: onAddDevice },
    { label: 'Branding', icon: Palette, color: 'orange', handler: onBranding },
    { label: 'Client Links', icon: LinkIcon, color: 'teal', handler: onClientLinks },
    { label: 'Setup Wizard', icon: Sparkles, color: 'blue', handler: onSetupWizard },
  ]

  return (
    <Card className="bg-[#0a0a10] border-white/[0.06]">
      <CardHeader className="pb-3">
        <CardTitle className="text-base font-semibold text-white">Quick Actions</CardTitle>
        <CardDescription className="text-white/40">Common administrative tasks</CardDescription>
      </CardHeader>
      <CardContent>
        <div className="grid grid-cols-2 gap-3">
          {actions.map((action, i) => {
            const c = colorMap[action.color]
            return (
              <button
                key={i}
                onClick={action.handler}
                className="flex flex-col items-center gap-2 p-3 bg-white/[0.03] hover:bg-white/[0.06] rounded-xl border border-white/[0.06] hover:border-white/[0.12] transition-all group"
              >
                <div className={`w-10 h-10 rounded-lg ${c.bg} flex items-center justify-center group-hover:brightness-125 transition-all`}>
                  <action.icon className={`w-5 h-5 ${c.text}`} />
                </div>
                <span className="text-xs text-white/40 group-hover:text-white transition-colors text-center leading-tight">
                  {action.label}
                </span>
              </button>
            )
          })}
        </div>
      </CardContent>
    </Card>
  )
}
