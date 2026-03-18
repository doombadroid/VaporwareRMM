import Link from 'next/link'
import { Button } from '@/components/ui/button'

export default function HomePage() {
  return (
    <div className="min-h-screen bg-gradient-to-br from-slate-950 via-slate-900 to-slate-800 text-white">
      {/* Header */}
      <header className="border-b border-slate-800 bg-slate-950/50 backdrop-blur-md">
        <div className="container mx-auto px-6 py-4 flex items-center justify-between">
          <Link href="/" className="flex items-center gap-2 font-bold text-xl">
            <span className="text-blue-500"> Vapor</span>
            RMM
          </Link>
          <nav className="hidden md:flex items-center gap-6">
            <Link href="/dashboard" className="text-sm text-slate-300 hover:text-white transition-colors">
              Dashboard
            </Link>
            <Link href="/agents" className="text-sm text-slate-300 hover:text-white transition-colors">
              Agents
            </Link>
            <Link href="/settings" className="text-sm text-slate-300 hover:text-white transition-colors">
              Settings
            </Link>
          </nav>
          <Button variant="outline" size="sm">
            Login
          </Button>
        </div>
      </header>

      {/* Hero Section */}
      <main className="container mx-auto px-6 py-24 text-center">
        <h1 className="text-5xl md:text-7xl font-bold tracking-tight mb-6 bg-gradient-to-r from-blue-400 via-purple-400 to-pink-400 bg-clip-text text-transparent">
          Remote Machine Management
        </h1>
        <p className="text-xl text-slate-400 max-w-2xl mx-auto mb-10">
          Manage your remote devices with ease. Real-time monitoring, automated patching,
          and comprehensive device control all in one platform.
        </p>
        <div className="flex flex-col sm:flex-row items-center justify-center gap-4">
          <Button size="lg" className="w-full sm:w-auto">
            Get Started
          </Button>
          <Button variant="outline" size="lg" className="w-full sm:w-auto">
            View Documentation
          </Button>
        </div>

        {/* Features Grid */}
        <div className="grid grid-cols-1 md:grid-cols-3 gap-8 mt-24 max-w-5xl mx-auto">
          {[
            {
              title: 'Device Monitoring',
              description: 'Real-time visibility into all your remote devices with detailed metrics and health status.',
            },
            {
              title: 'Automated Patching',
              description: 'Schedule and automate software updates across all your managed devices.',
            },
            {
              title: 'Secure Remote Access',
              description: 'Encrypted remote desktop access with multi-factor authentication support.',
            },
          ].map((feature, idx) => (
            <div key={idx} className="p-6 rounded-xl bg-slate-900/50 border border-slate-800 hover:border-blue-500/30 transition-colors">
              <h3 className="text-lg font-semibold mb-2 text-white">{feature.title}</h3>
              <p className="text-sm text-slate-400">{feature.description}</p>
            </div>
          ))}
        </div>
      </main>

      {/* Footer */}
      <footer className="border-t border-slate-800 bg-slate-950 py-12">
        <div className="container mx-auto px-6 text-center text-sm text-slate-500">
          <p>&copy; 2024 Vapor RMM. All rights reserved.</p>
        </div>
      </footer>
    </div>
  )
}