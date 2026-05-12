'use client'

import { useState, useMemo } from 'react'
import { Button } from '@/components/ui/button'
import {
  Monitor,
  Network,
  Terminal,
  MoreHorizontal,
  Search,
  Trash2,
  Download,
  Filter,
} from 'lucide-react'
import { formatTimeAgo } from '@/lib/dashboard-utils'
import type { Device } from '@/lib/api'
import { formatOSVersion } from '@/lib/utils'

interface DeviceFleetTableProps {
  devices: Device[]
  selectedDevices: Set<string>
  onToggleDevice: (id: string, checked: boolean) => void
  onToggleAll: (checked: boolean) => void
  onBulkDelete: () => void
  onExportCSV: () => void
  onRemoteControl: (device: Device) => void
  onTailscale: (device: Device) => void
}

export default function DeviceFleetTable({
  devices,
  selectedDevices,
  onToggleDevice,
  onToggleAll,
  onBulkDelete,
  onExportCSV,
  onRemoteControl,
  onTailscale,
}: DeviceFleetTableProps) {
  const [search, setSearch] = useState('')

  const filtered = useMemo(() => {
    if (!devices) return []
    if (!search.trim()) return devices
    const q = search.toLowerCase()
    return devices.filter(
      (d) =>
        (d.hostname || '').toLowerCase().includes(q) ||
        (d.os_name || '').toLowerCase().includes(q)
    )
  }, [devices, search])

  const allSelected =
    filtered?.length > 0 && filtered.every((d) => selectedDevices.has(d.id))

  return (
    <div className="bg-[#0a0a10] border border-white/[0.06] rounded-xl">
      <div className="p-5 border-b border-white/[0.06] flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <div>
          <h3 className="text-base font-semibold text-white">Device Fleet</h3>
          <p className="text-xs text-white/40">
            {devices?.length || 0} monitored devices
          </p>
        </div>
        <div className="flex items-center gap-2">
          {selectedDevices.size > 0 && (
            <>
              <Button
                size="sm"
                variant="outline"
                className="text-xs h-8 border-rose-500/30 text-rose-400 hover:bg-rose-500/10"
                onClick={onBulkDelete}
              >
                <Trash2 className="w-3.5 h-3.5 mr-1" /> Delete (
                {selectedDevices.size})
              </Button>
              <Button
                size="sm"
                variant="ghost"
                className="text-xs h-8"
                onClick={() => onToggleAll(false)}
              >
                Clear
              </Button>
            </>
          )}
          <Button
            size="sm"
            variant="outline"
            className="text-xs h-8 border-white/[0.08] text-white/60 hover:text-white hover:bg-white/[0.04]"
            onClick={onExportCSV}
          >
            <Download className="w-3.5 h-3.5 mr-1" /> Export CSV
          </Button>
          <div className="relative">
            <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-4 h-4 text-white/30" />
            <input
              type="text"
              placeholder="Search devices..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="bg-white/[0.04] border border-white/[0.06] rounded-lg pl-8 pr-3 py-1.5 text-sm text-white placeholder:text-white/30 focus:outline-none focus:border-cyan-500/40 w-48 transition-colors"
            />
          </div>
          <Button
            size="sm"
            variant="outline"
            className="text-xs h-8 border-white/[0.08] text-white/60 hover:text-white hover:bg-white/[0.04]"
          >
            <Filter className="w-3.5 h-3.5" />
          </Button>
        </div>
      </div>
      <div className="overflow-x-auto">
        <table className="w-full text-left text-sm">
          <thead className="border-b border-white/[0.06]">
            <tr>
              <th className="pb-3 pr-2 pl-5 pt-3">
                <input
                  type="checkbox"
                  className="rounded border-white/20 bg-white/5"
                  checked={allSelected}
                  onChange={(e) => onToggleAll(e.target.checked)}
                />
              </th>
              <th className="pb-3 pt-3 font-medium text-white/30 text-[10px] uppercase tracking-wider">
                Device
              </th>
              <th className="pb-3 pt-3 font-medium text-white/30 text-[10px] uppercase tracking-wider">
                Status
              </th>
              <th className="pb-3 pt-3 font-medium text-white/30 text-[10px] uppercase tracking-wider">
                CPU
              </th>
              <th className="pb-3 pt-3 font-medium text-white/30 text-[10px] uppercase tracking-wider">
                Memory
              </th>
              <th className="pb-3 pt-3 font-medium text-white/30 text-[10px] uppercase tracking-wider">
                Last Seen
              </th>
              <th className="pb-3 pt-3 font-medium text-white/30 text-[10px] uppercase tracking-wider text-right pr-5">
                Actions
              </th>
            </tr>
          </thead>
          <tbody>
            {filtered.map((device) => (
              <tr
                key={device.id}
                className="border-b border-white/[0.03] hover:bg-white/[0.02] transition-colors"
              >
                <td className="py-3 pr-2 pl-5">
                  <input
                    type="checkbox"
                    className="rounded border-white/20 bg-white/5"
                    checked={selectedDevices.has(device.id)}
                    onChange={(e) =>
                      onToggleDevice(device.id, e.target.checked)
                    }
                  />
                </td>
                <td className="py-3">
                  <div>
                    <p className="font-medium text-white text-sm">
                      {device.hostname}
                    </p>
                    <p className="text-[10px] text-white/30">
                      {formatOSVersion(device.os_name, device.os_version, device.kernel_version)}
                    </p>
                    {device.tags && device.tags.length > 0 && (
                      <div className="flex flex-wrap gap-1 mt-1">
                        {device.tags.map((tag) => (
                          <span
                            key={tag}
                            className="px-1.5 py-0.5 rounded text-[10px] bg-white/[0.06] text-white/40 border border-white/[0.04]"
                          >
                            {tag}
                          </span>
                        ))}
                      </div>
                    )}
                  </div>
                </td>
                <td className="py-3">
                  <span
                    className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium border ${
                      device.status === 'online'
                        ? 'bg-emerald-500/10 text-emerald-400 border-emerald-500/20'
                        : 'bg-rose-500/10 text-rose-400 border-rose-500/20'
                    }`}
                  >
                    <span
                      className={`w-1.5 h-1.5 rounded-full ${
                        device.status === 'online'
                          ? 'bg-emerald-400 animate-pulse'
                          : 'bg-rose-400'
                      }`}
                    />
                    {device.status}
                  </span>
                </td>
                <td className="py-3">
                  <div className="flex items-center gap-2">
                    <div className="w-16 bg-white/[0.06] rounded-full h-1.5">
                      <div
                        className={`h-1.5 rounded-full ${
                          !device.cpu || device.cpu === '--'
                            ? 'bg-white/10'
                            : parseInt(device.cpu) > 80
                              ? 'bg-rose-400'
                              : 'bg-cyan-400'
                        }`}
                        style={{
                          width:
                            !device.cpu || device.cpu === '--'
                              ? '0%'
                              : device.cpu,
                        }}
                      />
                    </div>
                    <span
                      className={`text-xs font-mono ${
                        !device.cpu || device.cpu === '--'
                          ? 'text-white/20'
                          : parseInt(device.cpu) > 80
                            ? 'text-rose-400'
                            : 'text-white/50'
                      }`}
                    >
                      {device.cpu || '--'}
                    </span>
                  </div>
                </td>
                <td className="py-3">
                  <div className="flex items-center gap-2">
                    <div className="w-16 bg-white/[0.06] rounded-full h-1.5">
                      <div
                        className={`h-1.5 rounded-full ${
                          (device.memory || 0) > 80
                            ? 'bg-rose-400'
                            : 'bg-violet-400'
                        }`}
                        style={{ width: `${device.memory || 0}%` }}
                      />
                    </div>
                    <span
                      className={`text-xs font-mono ${
                        (device.memory || 0) > 80
                          ? 'text-rose-400'
                          : 'text-white/50'
                      }`}
                    >
                      {device.memory || 0}%
                    </span>
                  </div>
                </td>
                <td className="py-3 text-xs text-white/30 font-mono">
                  {formatTimeAgo(device.last_seen)}
                </td>
                <td className="py-3 text-right pr-5">
                  <div className="flex items-center justify-end gap-1">
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-7 w-7 p-0 text-cyan-400 hover:text-cyan-300 hover:bg-cyan-500/10"
                      title="Remote Control"
                      onClick={() => onRemoteControl(device)}
                    >
                      <Monitor className="w-4 h-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-7 w-7 p-0 text-violet-400 hover:text-violet-300 hover:bg-violet-500/10"
                      title="Tailscale"
                      onClick={() => onTailscale(device)}
                    >
                      <Network className="w-4 h-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-7 w-7 p-0 text-white/40 hover:text-white hover:bg-white/[0.06]"
                      title="Command"
                    >
                      <Terminal className="w-4 h-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-7 w-7 p-0 text-white/40 hover:text-white hover:bg-white/[0.06]"
                    >
                      <MoreHorizontal className="w-4 h-4" />
                    </Button>
                  </div>
                </td>
              </tr>
            ))}
            {(!filtered || filtered.length === 0) && (
              <tr>
                <td
                  colSpan={7}
                  className="py-8 text-center text-sm text-white/30"
                >
                  No devices found
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
