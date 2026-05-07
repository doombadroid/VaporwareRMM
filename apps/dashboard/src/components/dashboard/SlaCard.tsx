import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Button } from '@/components/ui/button'

const metrics = [
  { label: 'Uptime SLA', value: '99.9%', color: '#10b981', pct: 0.98, sub: 'Target: 99.5%' },
  { label: 'Response Time', value: '92%', color: '#22d3ee', pct: 0.92, sub: 'Avg 14min / Target 15min' },
  { label: 'Resolution Rate', value: '87%', color: '#a78bfa', pct: 0.87, sub: '85% within SLA window' },
  { label: 'CSAT Score', value: '4.8/5', color: '#10b981', pct: 0.95, sub: '+0.3 from last month' },
]

export default function SlaCard() {
  return (
    <Card className="bg-[#0a0a10] border-white/[0.06]">
      <CardHeader className="pb-3">
        <div className="flex items-center justify-between">
          <div>
            <CardTitle className="text-base font-semibold text-white">SLA Performance</CardTitle>
            <CardDescription className="text-white/40">Monthly service level agreement metrics</CardDescription>
          </div>
          <div className="flex items-center gap-2">
            <span className="text-sm text-white/40">April 2026</span>
            <Button size="sm" variant="outline" className="text-xs h-7 border-white/[0.08] text-white/60 hover:bg-white/[0.04]">View Report</Button>
          </div>
        </div>
      </CardHeader>
      <CardContent>
        <div className="grid grid-cols-1 md:grid-cols-4 gap-6">
          {metrics.map((metric, i) => (
            <div key={i} className="text-center">
              <div className="flex items-center justify-center mb-2">
                <div className="relative w-20 h-20">
                  <svg className="w-20 h-20 -rotate-90" viewBox="0 0 80 80">
                    <circle cx="40" cy="40" r="34" fill="none" stroke="rgba(255,255,255,0.06)" strokeWidth="6" />
                    <circle cx="40" cy="40" r="34" fill="none" stroke={metric.color} strokeWidth="6" strokeLinecap="round" strokeDasharray={`${metric.pct * 213.628} 213.628`} />
                  </svg>
                  <div className="absolute inset-0 flex items-center justify-center">
                    <span className="text-lg font-bold font-mono" style={{ color: metric.color }}>{metric.value}</span>
                  </div>
                </div>
              </div>
              <p className="text-sm text-white/40">{metric.label}</p>
              <p className="text-xs mt-1" style={{ color: metric.color }}>{metric.sub}</p>
            </div>
          ))}
        </div>
      </CardContent>
    </Card>
  )
}
