'use client'

import { useState } from 'react'
import { Button } from '@/components/ui/button'
import {
  X,
  Server,
  LayoutGrid,
  Smartphone,
  Package,
  Copy,
  Check,
  Download,
} from 'lucide-react'
import { branding as brandingApi } from '@/lib/api'
import type { InstallLinks } from '@/lib/api'

interface InstallLinksModalProps {
  open: boolean
  onClose: () => void
  links: InstallLinks | null
}

export default function InstallLinksModal({
  open,
  onClose,
  links,
}: InstallLinksModalProps) {
  const [copied, setCopied] = useState<string | null>(null)

  const copy = (text: string, id: string) => {
    navigator.clipboard.writeText(text)
    setCopied(id)
    setTimeout(() => setCopied(null), 2000)
  }

  if (!open || !links) return null

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm"
      onClick={onClose}
    >
      <div
        className="bg-[#0a0a10] border border-white/[0.08] rounded-2xl shadow-2xl max-w-2xl w-full mx-4 max-h-[80vh] overflow-y-auto"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between p-6 border-b border-white/[0.06]">
          <div>
            <h2 className="text-lg font-semibold text-white">
              Agent Install Links
            </h2>
            <p className="text-sm text-white/40">Share these with your clients</p>
          </div>
          <button
            onClick={onClose}
            className="text-white/40 hover:text-white transition-colors"
          >
            <X className="w-5 h-5" />
          </button>
        </div>
        <div className="p-6 space-y-4">
          <div className="p-4 bg-white/[0.03] rounded-xl border border-white/[0.06]">
            <div className="flex items-center gap-3">
              {links.icon_url ? (
                <img
                  src={links.icon_url}
                  alt="Icon"
                  className="w-10 h-10 rounded-lg"
                  onError={(e) =>
                    ((e.target as HTMLImageElement).style.display = 'none')
                  }
                />
              ) : (
                <div className="w-10 h-10 rounded-lg bg-gradient-to-br from-cyan-500 to-violet-600 flex items-center justify-center text-lg font-bold text-white">
                  {links.app_name.charAt(0).toUpperCase()}
                </div>
              )}
              <div>
                <p className="text-sm font-medium text-white">
                  {links.app_name}
                </p>
                <p className="text-xs text-white/30">{links.company_name}</p>
              </div>
            </div>
          </div>

          <div className="space-y-3">
            {(links.install_options || []).map((option: any, index: number) => (
              <div
                key={index}
                className="p-4 bg-white/[0.03] rounded-xl border border-white/[0.06]"
              >
                <div className="flex items-center justify-between mb-2">
                  <div className="flex items-center gap-2">
                    <span className="text-lg text-white/60">
                      {option.platform === 'linux' ? (
                        <Server className="w-5 h-5" />
                      ) : option.platform === 'windows' ? (
                        <LayoutGrid className="w-5 h-5" />
                      ) : option.platform === 'macos' ? (
                        <Smartphone className="w-5 h-5" />
                      ) : (
                        <Package className="w-5 h-5" />
                      )}
                    </span>
                    <span className="text-sm font-medium text-white">
                      {option.name}
                    </span>
                  </div>
                  <span className="text-xs text-white/20 uppercase">
                    {option.platform}
                  </span>
                </div>
                {option.command && (
                  <div className="flex items-center gap-2 mt-2">
                    <code className="flex-1 bg-black/30 px-3 py-2 rounded-lg text-xs font-mono text-white/50 truncate">
                      {option.command}
                    </code>
                    <Button
                      size="sm"
                      variant="ghost"
                      className="text-white/40 hover:text-white"
                      onClick={() => copy(option.command, `cmd-${index}`)}
                    >
                      {copied === `cmd-${index}` ? (
                        <Check className="w-4 h-4 text-emerald-400" />
                      ) : (
                        <Copy className="w-4 h-4" />
                      )}
                    </Button>
                  </div>
                )}
                {option.url && (
                  <div className="flex items-center gap-2 mt-2">
                    <a
                      href={option.url}
                      className="text-xs text-cyan-400 hover:text-cyan-300 truncate"
                      target="_blank"
                      rel="noopener noreferrer"
                    >
                      {option.url}
                    </a>
                    <Button
                      size="sm"
                      variant="ghost"
                      className="text-white/40 hover:text-white"
                      onClick={() => copy(option.url, `url-${index}`)}
                    >
                      {copied === `url-${index}` ? (
                        <Check className="w-4 h-4 text-emerald-400" />
                      ) : (
                        <Copy className="w-4 h-4" />
                      )}
                    </Button>
                  </div>
                )}
              </div>
            ))}
          </div>

          <div className="pt-4 border-t border-white/[0.06]">
            <a
              href={brandingApi.getInstallScript()}
              className="flex items-center justify-center gap-2 w-full p-3 bg-emerald-500/10 border border-emerald-500/20 rounded-xl text-emerald-400 hover:bg-emerald-500/20 transition-colors"
              download={`${links.app_name}-agent-install.sh`}
            >
              <Download className="w-4 h-4" />
              Download Install Script
            </a>
          </div>
        </div>
      </div>
    </div>
  )
}
