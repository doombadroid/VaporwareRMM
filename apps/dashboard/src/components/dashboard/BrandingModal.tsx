'use client'

import { Button } from '@/components/ui/button'
import { X } from 'lucide-react'
import { toast } from 'sonner'
import { branding as brandingApi } from '@/lib/api'
import type { BrandingConfig } from '@/lib/api'

interface BrandingModalProps {
  open: boolean
  onClose: () => void
  branding: BrandingConfig
  onBrandingChange: (b: BrandingConfig) => void
}

export default function BrandingModal({
  open,
  onClose,
  branding,
  onBrandingChange,
}: BrandingModalProps) {
  const handleSave = async () => {
    try {
      await brandingApi.update(branding)
      onClose()
    } catch {
      toast.error('Failed to save branding')
    }
  }

  if (!open) return null

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm"
      onClick={onClose}
    >
      <div
        className="bg-[#0a0a10] border border-white/[0.08] rounded-2xl shadow-2xl max-w-lg w-full mx-4"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between p-6 border-b border-white/[0.06]">
          <div>
            <h2 className="text-lg font-semibold text-white">
              Branding Settings
            </h2>
            <p className="text-sm text-white/40">
              Customize your RMM appearance
            </p>
          </div>
          <button
            onClick={onClose}
            className="text-white/40 hover:text-white transition-colors"
          >
            <X className="w-5 h-5" />
          </button>
        </div>
        <div className="p-6 space-y-4">
          <div>
            <label className="block text-sm font-medium text-white/60 mb-1">
              App Name
            </label>
            <input
              type="text"
              value={branding.app_name}
              onChange={(e) =>
                onBrandingChange({ ...branding, app_name: e.target.value })
              }
              className="w-full bg-white/[0.04] border border-white/[0.08] rounded-lg px-3 py-2 text-sm text-white placeholder:text-white/20 focus:outline-none focus:border-cyan-500/40"
              placeholder="vaporRMM"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-white/60 mb-1">
              Company Name
            </label>
            <input
              type="text"
              value={branding.company_name}
              onChange={(e) =>
                onBrandingChange({ ...branding, company_name: e.target.value })
              }
              className="w-full bg-white/[0.04] border border-white/[0.08] rounded-lg px-3 py-2 text-sm text-white placeholder:text-white/20 focus:outline-none focus:border-cyan-500/40"
              placeholder="Your Company"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-white/60 mb-1">
              Icon URL
            </label>
            <input
              type="text"
              value={branding.icon_url}
              onChange={(e) =>
                onBrandingChange({ ...branding, icon_url: e.target.value })
              }
              className="w-full bg-white/[0.04] border border-white/[0.08] rounded-lg px-3 py-2 text-sm text-white placeholder:text-white/20 focus:outline-none focus:border-cyan-500/40"
              placeholder="https://example.com/logo.png"
            />
            {branding.icon_url && (
              <div className="mt-2 flex items-center gap-2">
                <span className="text-xs text-white/30">Preview:</span>
                <img
                  src={branding.icon_url}
                  alt="Icon preview"
                  className="w-8 h-8 rounded"
                  onError={(e) =>
                    ((e.target as HTMLImageElement).style.display = 'none')
                  }
                />
              </div>
            )}
          </div>
          <div>
            <label className="block text-sm font-medium text-white/60 mb-1">
              Primary Color
            </label>
            <div className="flex items-center gap-2">
              <input
                type="color"
                value={branding.primary_color}
                onChange={(e) =>
                  onBrandingChange({
                    ...branding,
                    primary_color: e.target.value,
                  })
                }
                className="w-10 h-10 rounded-lg cursor-pointer bg-transparent"
              />
              <input
                type="text"
                value={branding.primary_color}
                onChange={(e) =>
                  onBrandingChange({
                    ...branding,
                    primary_color: e.target.value,
                  })
                }
                className="flex-1 bg-white/[0.04] border border-white/[0.08] rounded-lg px-3 py-2 text-sm text-white font-mono focus:outline-none focus:border-cyan-500/40"
                placeholder="#3b82f6"
              />
            </div>
          </div>
          <div className="p-4 bg-white/[0.03] rounded-xl border border-white/[0.06]">
            <p className="text-xs text-white/30 mb-2">Preview</p>
            <div className="flex items-center gap-2">
              {branding.icon_url ? (
                <img
                  src={branding.icon_url}
                  alt="Icon"
                  className="w-6 h-6 rounded"
                  onError={(e) =>
                    ((e.target as HTMLImageElement).style.display = 'none')
                  }
                />
              ) : (
                <div className="w-6 h-6 rounded bg-gradient-to-br from-cyan-500 to-violet-600 flex items-center justify-center text-xs font-bold text-white">
                  {branding.app_name.charAt(0).toUpperCase()}
                </div>
              )}
              <span
                className="text-sm font-semibold"
                style={{ color: branding.primary_color }}
              >
                {branding.app_name}
              </span>
            </div>
          </div>
        </div>
        <div className="flex justify-end gap-3 p-6 border-t border-white/[0.06]">
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            onClick={handleSave}
            style={{ backgroundColor: branding.primary_color }}
            className="text-black font-medium"
          >
            Save Changes
          </Button>
        </div>
      </div>
    </div>
  )
}
