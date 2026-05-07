import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { CheckCircle, Wrench, AlertTriangle, RefreshCw, Lock } from 'lucide-react'

const activities = [
  { Icon: CheckCircle, color: 'text-emerald-400', text: 'Backup completed for DC-01', time: '2m ago' },
  { Icon: Wrench, color: 'text-cyan-400', text: 'Agent v3.2.1 deployed to 12 workstations', time: '15m ago' },
  { Icon: AlertTriangle, color: 'text-amber-400', text: 'Disk cleanup triggered on FILE-SRV-01', time: '1h ago' },
  { Icon: RefreshCw, color: 'text-violet-400', text: 'Windows KB5034441 deployed successfully', time: '2h ago' },
  { Icon: Lock, color: 'text-rose-400', text: 'Firewall rule updated on FW-01', time: '3h ago' },
]

export default function RecentActivityPanel() {
  return (
    <Card className="bg-[#0a0a10] border-white/[0.06]">
      <CardHeader className="pb-3">
        <CardTitle className="text-base font-semibold text-white">Recent Activity</CardTitle>
        <CardDescription className="text-white/40">Latest system events</CardDescription>
      </CardHeader>
      <CardContent>
        <div className="space-y-4">
          {activities.map((activity, i) => (
            <div key={i} className="flex items-start gap-3">
              <activity.Icon className={`w-4 h-4 mt-0.5 ${activity.color}`} />
              <div className="flex-1 min-w-0">
                <p className="text-sm text-white/60">{activity.text}</p>
                <p className="text-[10px] text-white/20 mt-0.5 font-mono">{activity.time}</p>
              </div>
            </div>
          ))}
        </div>
      </CardContent>
    </Card>
  )
}
