import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import type { SLAMetrics } from '@/lib/api'

interface SlaCardProps {
  sla?: SLAMetrics
}

interface Metric {
  label: string
  display: string
  pct: number
  color: string
  sub: string
}

const buildMetrics = (sla?: SLAMetrics): Metric[] => {
  if (!sla) return []
  const onlinePct = sla.online_pct
  const resPct = sla.resolution_rate_pct
  const minutes = sla.avg_response_minutes
  // Color thresholds are heuristic — green at the high end, amber mid,
  // rose for trouble. Tunable later.
  const colorFor = (p: number) => (p >= 90 ? '#10b981' : p >= 60 ? '#f59e0b' : '#f43f5e')
  // Response-time score: closer to zero is better. Treat 0 min as 100%
  // and 8h+ as 0%; clamp in between. Pure visual mapping; the numeric
  // value in the label is the truth.
  const respPct = Math.max(0, Math.min(100, 100 - (minutes / 480) * 100))
  return [
    {
      label: 'Fleet Online',
      display: `${onlinePct.toFixed(1)}%`,
      pct: onlinePct / 100,
      color: colorFor(onlinePct),
      sub: 'Devices reporting',
    },
    {
      label: 'Resolution Rate',
      display: `${resPct.toFixed(0)}%`,
      pct: resPct / 100,
      color: colorFor(resPct),
      sub: `${sla.resolved_count}/${sla.created_count} in 30d`,
    },
    {
      label: 'Avg Response',
      display: minutes >= 60 ? `${(minutes / 60).toFixed(1)}h` : `${minutes.toFixed(0)}m`,
      pct: respPct / 100,
      color: colorFor(respPct),
      sub: 'Resolved tickets, last 30d',
    },
    {
      label: 'Tickets Closed',
      display: `${sla.resolved_count}`,
      pct: sla.created_count > 0 ? Math.min(1, sla.resolved_count / Math.max(sla.created_count, 1)) : 0,
      color: '#a78bfa',
      sub: `Window: ${sla.window_days}d`,
    },
  ]
}

export default function SlaCard({ sla }: SlaCardProps) {
  const metrics = buildMetrics(sla)
  return (
    <Card className="bg-[#0a0a10] border-white/[0.06]">
      <CardHeader className="pb-3">
        <div className="flex items-center justify-between">
          <div>
            <CardTitle className="text-base font-semibold text-white">SLA Performance</CardTitle>
            <CardDescription className="text-white/40">
              {sla ? `Last ${sla.window_days} days` : 'No data'}
            </CardDescription>
          </div>
        </div>
      </CardHeader>
      <CardContent>
        {metrics.length === 0 ? (
          <p className="text-sm text-white/40 py-6 text-center">SLA metrics will appear once tickets and devices are active.</p>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-4 gap-6">
            {metrics.map((metric, i) => (
              <div key={i} className="text-center">
                <div className="flex items-center justify-center mb-2">
                  <div className="relative w-20 h-20">
                    <svg className="w-20 h-20 -rotate-90" viewBox="0 0 80 80">
                      <circle cx="40" cy="40" r="34" fill="none" stroke="rgba(255,255,255,0.06)" strokeWidth="6" />
                      <circle
                        cx="40"
                        cy="40"
                        r="34"
                        fill="none"
                        stroke={metric.color}
                        strokeWidth="6"
                        strokeLinecap="round"
                        strokeDasharray={`${metric.pct * 213.628} 213.628`}
                      />
                    </svg>
                    <div className="absolute inset-0 flex items-center justify-center">
                      <span className="text-lg font-bold font-mono" style={{ color: metric.color }}>
                        {metric.display}
                      </span>
                    </div>
                  </div>
                </div>
                <p className="text-sm text-white/40">{metric.label}</p>
                <p className="text-xs mt-1" style={{ color: metric.color }}>{metric.sub}</p>
              </div>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  )
}
