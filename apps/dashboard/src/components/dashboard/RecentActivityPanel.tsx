import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Activity, Ticket, AlertTriangle, Wrench, ShieldAlert, Settings } from 'lucide-react'
import type { RecentActivityEntry } from '@/lib/api'

interface RecentActivityPanelProps {
  activity?: RecentActivityEntry[]
}

// iconFor maps audit-log resource_type to a lucide icon + tint. Unknown
// types fall through to the generic Activity glyph so a new event source
// renders without code changes.
const iconFor = (resourceType: string): { Icon: typeof Activity; color: string } => {
  switch (resourceType) {
    case 'ticket':
      return { Icon: Ticket, color: 'text-cyan-400' }
    case 'alert':
    case 'alert_rule':
    case 'alert_settings':
      return { Icon: AlertTriangle, color: 'text-amber-400' }
    case 'patch':
      return { Icon: Wrench, color: 'text-violet-400' }
    case 'compliance':
    case 'security':
      return { Icon: ShieldAlert, color: 'text-rose-400' }
    case 'branding':
    case 'tenant':
    case 'user':
      return { Icon: Settings, color: 'text-emerald-400' }
    default:
      return { Icon: Activity, color: 'text-white/50' }
  }
}

const relTime = (unixSec: number): string => {
  const diffSec = Math.max(0, Math.floor(Date.now() / 1000) - unixSec)
  if (diffSec < 60) return `${diffSec}s ago`
  if (diffSec < 3600) return `${Math.floor(diffSec / 60)}m ago`
  if (diffSec < 86400) return `${Math.floor(diffSec / 3600)}h ago`
  return `${Math.floor(diffSec / 86400)}d ago`
}

export default function RecentActivityPanel({ activity }: RecentActivityPanelProps) {
  const items = activity ?? []
  return (
    <Card className="bg-[#0a0a10] border-white/[0.06]">
      <CardHeader className="pb-3">
        <CardTitle className="text-base font-semibold text-white">Recent Activity</CardTitle>
        <CardDescription className="text-white/40">Latest tenant events from the audit log</CardDescription>
      </CardHeader>
      <CardContent>
        {items.length === 0 ? (
          <p className="text-sm text-white/40">No recent activity.</p>
        ) : (
          <div className="space-y-4">
            {items.map((entry, i) => {
              const { Icon, color } = iconFor(entry.resource_type)
              return (
                <div key={i} className="flex items-start gap-3">
                  <Icon className={`w-4 h-4 mt-0.5 shrink-0 ${color}`} />
                  <div className="flex-1 min-w-0">
                    <p className="text-sm text-white/60 truncate">
                      <span className="font-mono text-white/80">{entry.action}</span>
                      <span className="text-white/40"> · {entry.resource_type}</span>
                    </p>
                    <p className="text-[10px] text-white/20 mt-0.5 font-mono">{relTime(entry.created_at)}</p>
                  </div>
                </div>
              )
            })}
          </div>
        )}
      </CardContent>
    </Card>
  )
}
