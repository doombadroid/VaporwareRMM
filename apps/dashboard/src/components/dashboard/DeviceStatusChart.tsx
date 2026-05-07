import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { PieChart, Pie, Cell, Tooltip, ResponsiveContainer } from 'recharts'

interface DeviceStatusChartProps {
  data: Array<{ name: string; value: number; color: string }>
}

export default function DeviceStatusChart({ data }: DeviceStatusChartProps) {
  return (
    <Card className="bg-[#0a0a10] border-white/[0.06]">
      <CardHeader className="pb-3">
        <CardTitle className="text-base font-semibold text-white">Device Fleet</CardTitle>
        <CardDescription className="text-white/40">Status distribution</CardDescription>
      </CardHeader>
      <CardContent className="h-[250px] flex flex-col items-center justify-center">
        <ResponsiveContainer width="100%" height={220}>
          <PieChart>
            <Pie data={data} cx="50%" cy="50%" innerRadius={60} outerRadius={90} paddingAngle={5} dataKey="value" stroke="none">
              {data.map((entry, index) => (
                <Cell key={`cell-${index}`} fill={entry.color} />
              ))}
            </Pie>
            <Tooltip contentStyle={{ backgroundColor: '#0a0a10', border: '1px solid rgba(255,255,255,0.08)', borderRadius: '8px' }} itemStyle={{ color: '#e2e8f0' }} />
          </PieChart>
        </ResponsiveContainer>
        <div className="flex gap-4 mt-2">
          {data.map((entry) => (
            <div key={entry.name} className="flex items-center gap-1.5">
              <div className="w-2.5 h-2.5 rounded-full" style={{ backgroundColor: entry.color }} />
              <span className="text-xs text-white/40">{entry.name}: {entry.value}</span>
            </div>
          ))}
        </div>
      </CardContent>
    </Card>
  )
}
