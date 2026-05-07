import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { AreaChart, Area, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer } from 'recharts'

interface ResourceChartProps {
  data: Array<{ time: string; cpu: number; memory: number; disk: number }>
}

export default function ResourceChart({ data }: ResourceChartProps) {
  return (
    <Card className="bg-[#0a0a10] border-white/[0.06] lg:col-span-2">
      <CardHeader className="pb-3">
        <div className="flex items-center justify-between">
          <div>
            <CardTitle className="text-base font-semibold text-white">Resource Utilization</CardTitle>
            <CardDescription className="text-white/40">CPU, Memory & Disk usage over last 24 hours</CardDescription>
          </div>
          <div className="flex gap-4 text-xs">
            <div className="flex items-center gap-1.5"><div className="w-2.5 h-2.5 rounded-full bg-cyan-400" /><span className="text-white/40">CPU</span></div>
            <div className="flex items-center gap-1.5"><div className="w-2.5 h-2.5 rounded-full bg-violet-400" /><span className="text-white/40">Memory</span></div>
            <div className="flex items-center gap-1.5"><div className="w-2.5 h-2.5 rounded-full bg-emerald-400" /><span className="text-white/40">Disk</span></div>
          </div>
        </div>
      </CardHeader>
      <CardContent className="h-[250px]">
        <ResponsiveContainer width="100%" height={220}>
          <AreaChart data={data}>
            <defs>
              <linearGradient id="cpuGradient" x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor="#22d3ee" stopOpacity={0.3}/>
                <stop offset="95%" stopColor="#22d3ee" stopOpacity={0}/>
              </linearGradient>
              <linearGradient id="memGradient" x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor="#a78bfa" stopOpacity={0.3}/>
                <stop offset="95%" stopColor="#a78bfa" stopOpacity={0}/>
              </linearGradient>
              <linearGradient id="diskGradient" x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor="#34d399" stopOpacity={0.3}/>
                <stop offset="95%" stopColor="#34d399" stopOpacity={0}/>
              </linearGradient>
            </defs>
            <CartesianGrid strokeDasharray="3 3" stroke="rgba(255,255,255,0.04)" />
            <XAxis dataKey="time" stroke="rgba(255,255,255,0.15)" fontSize={12} />
            <YAxis stroke="rgba(255,255,255,0.15)" fontSize={12} domain={[0, 100]} />
            <Tooltip contentStyle={{ backgroundColor: '#0a0a10', border: '1px solid rgba(255,255,255,0.08)', borderRadius: '8px' }} itemStyle={{ color: '#e2e8f0' }} labelStyle={{ color: '#94a3b8' }} />
            <Area type="monotone" dataKey="cpu" stroke="#22d3ee" strokeWidth={2} fill="url(#cpuGradient)" />
            <Area type="monotone" dataKey="memory" stroke="#a78bfa" strokeWidth={2} fill="url(#memGradient)" />
            <Area type="monotone" dataKey="disk" stroke="#34d399" strokeWidth={2} fill="url(#diskGradient)" />
          </AreaChart>
        </ResponsiveContainer>
      </CardContent>
    </Card>
  )
}
