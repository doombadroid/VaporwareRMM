import Link from 'next/link'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  AreaChart,
  Area,
} from 'recharts'

// Mock data for charts
const systemData = [
  { time: '10:00', cpu: 45, memory: 62 },
  { time: '10:05', cpu: 52, memory: 65 },
  { time: '10:10', cpu: 38, memory: 60 },
  { time: '10:15', cpu: 67, memory: 71 },
  { time: '10:20', cpu: 43, memory: 64 },
  { time: '10:25', cpu: 58, memory: 68 },
  { time: '10:30', cpu: 49, memory: 63 },
]

const devicesData = [
  { name: 'Online', value: 87 },
  { name: 'Offline', value: 12 },
  { name: 'Maintenance', value: 1 },
]

export default function DashboardPage() {
  return (
    <div className="min-h-screen bg-gradient-to-br from-slate-950 via-slate-900 to-slate-800 text-white">
      {/* Header */}
      <header className="border-b border-slate-800 bg-slate-950/50 backdrop-blur-md sticky top-0 z-50">
        <div className="container mx-auto px-6 py-4 flex items-center justify-between">
          <Link href="/dashboard" className="flex items-center gap-2 font-bold text-xl">
            <span className="text-blue-500"> Vapor</span>
            RMM
          </Link>
          <nav className="hidden md:flex items-center gap-6">
            <Link href="/dashboard" className="text-sm font-medium text-blue-400">
              Dashboard
            </Link>
            <Link href="/agents" className="text-sm text-slate-300 hover:text-white transition-colors">
              Agents
            </Link>
            <Link href="/settings" className="text-sm text-slate-300 hover:text-white transition-colors">
              Settings
            </Link>
          </nav>
          <div className="flex items-center gap-4">
            <Button variant="outline" size="sm">Logout</Button>
          </div>
        </div>
      </header>

      {/* Main Content */}
      <main className="container mx-auto px-6 py-8">
        {/* Stats Grid */}
        <div className="grid grid-cols-1 md:grid-cols-4 gap-6 mb-8">
          {[
            { title: 'Total Devices', value: '98', change: '+2 this week', color: 'text-blue-400' },
            { title: 'Online', value: '87', change: '95% uptime', color: 'text-green-400' },
            { title: 'Pending Updates', value: '12', change: 'Needs attention', color: 'text-yellow-400' },
            { title: 'Security Alerts', value: '0', change: 'All clear', color: 'text-purple-400' },
          ].map((stat, idx) => (
            <Card key={idx} className="bg-slate-900/50 border-slate-800 backdrop-blur-sm">
              <CardContent className="pt-6">
                <p className="text-sm text-slate-400 mb-1">{stat.title}</p>
                <div className="flex items-baseline gap-2">
                  <span className="text-3xl font-bold text-white">{stat.value}</span>
                </div>
                <p className={`text-xs mt-2 ${stat.color}`}>{stat.change}</p>
              </CardContent>
            </Card>
          ))}
        </div>

        {/* Charts Section */}
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6 mb-8">
          {/* CPU/Memory Usage Chart */}
          <Card className="bg-slate-900/50 border-slate-800 backdrop-blur-sm">
            <CardHeader>
              <CardTitle>System Resources</CardTitle>
              <CardDescription>Real-time CPU and memory usage across managed devices</CardDescription>
            </CardHeader>
            <CardContent className="h-[300px]">
              <ResponsiveContainer width="100%" height="100%">
                <LineChart data={systemData}>
                  <CartesianGrid strokeDasharray="3 3" stroke="#334155" opacity={0.5} />
                  <XAxis dataKey="time" stroke="#94a3b8" fontSize={12} />
                  <YAxis stroke="#94a3b8" fontSize={12} />
                  <Tooltip
                    contentStyle={{ backgroundColor: '#1e293b', border: '1px solid #334155' }}
                    itemStyle={{ color: '#fff' }}
                  />
                  <Line type="monotone" dataKey="cpu" stroke="#3b82f6" strokeWidth={2} dot={false} name="CPU %" />
                  <Line type="monotone" dataKey="memory" stroke="#a855f7" strokeWidth={2} dot={false} name="Memory %" />
                </LineChart>
              </ResponsiveContainer>
            </CardContent>
          </Card>

          {/* Device Status Pie Chart */}
          <Card className="bg-slate-900/50 border-slate-800 backdrop-blur-sm">
            <CardHeader>
              <CardTitle>Device Status</CardTitle>
              <CardDescription>Current status of all managed devices</CardDescription>
            </CardHeader>
            <CardContent className="h-[300px]">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={devicesData}>
                  <defs>
                    <linearGradient id="colorOnline" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor="#22c55e" stopOpacity={0.3} />
                      <stop offset="95%" stopColor="#22c55e" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid strokeDasharray="3 3" stroke="#334155" opacity={0.5} />
                  <XAxis dataKey="name" stroke="#94a3b8" fontSize={12} />
                  <Tooltip
                    contentStyle={{ backgroundColor: '#1e293b', border: '1px solid #334155' }}
                    itemStyle={{ color: '#fff' }}
                  />
                  <Area type="monotone" dataKey="value" stroke="#22c55e" fillOpacity={1} fill="url(#colorOnline)" name="Devices" />
                </AreaChart>
              </ResponsiveContainer>
            </CardContent>
          </Card>
        </div>

        {/* Recent Devices Table */}
        <Card className="bg-slate-900/50 border-slate-800 backdrop-blur-sm">
          <CardHeader>
            <CardTitle>Recent Devices</CardTitle>
            <CardDescription>Recently connected managed devices</CardDescription>
          </CardHeader>
          <CardContent>
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead className="border-b border-slate-800">
                  <tr>
                    <th className="pb-3 font-medium text-slate-400">Device Name</th>
                    <th className="pb-3 font-medium text-slate-400">OS</th>
                    <th className="pb-3 font-medium text-slate-400">Last Seen</th>
                    <th className="pb-3 font-medium text-slate-400">Status</th>
                    <th className="pb-3 font-medium text-slate-400 text-right">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {[
                    { name: 'workstation-01', os: 'Windows 11', lastSeen: '2 min ago', status: 'Online' },
                    { name: 'server-prod-01', os: 'Ubuntu 24.04', lastSeen: '5 min ago', status: 'Online' },
                    { name: 'laptop-dev-03', os: 'macOS 15', lastSeen: '12 min ago', status: 'Online' },
                    { name: 'desktop-sales-02', os: 'Windows 10', lastSeen: '1 hour ago', status: 'Offline' },
                  ].map((device, idx) => (
                    <tr key={idx} className="border-b border-slate-800/50 hover:bg-slate-800/30 transition-colors">
                      <td className="py-3 font-medium text-white">{device.name}</td>
                      <td className="py-3 text-slate-400">{device.os}</td>
                      <td className="py-3 text-slate-400">{device.lastSeen}</td>
                      <td className="py-3">
                        <span className={`inline-flex items-center px-2 py-1 rounded-full text-xs font-medium ${
                          device.status === 'Online' ? 'bg-green-500/10 text-green-400' : 'bg-red-500/10 text-red-400'
                        }`}>
                          {device.status}
                        </span>
                      </td>
                      <td className="py-3 text-right">
                        <Button variant="ghost" size="sm">Connect</Button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </CardContent>
        </Card>
      </main>

      {/* Footer */}
      <footer className="border-t border-slate-800 bg-slate-950 py-6 mt-auto">
        <div className="container mx-auto px-6 text-center text-sm text-slate-500">
          <p>&copy; 2024 Vapor RMM. All rights reserved.</p>
        </div>
      </footer>
    </div>
  )
}