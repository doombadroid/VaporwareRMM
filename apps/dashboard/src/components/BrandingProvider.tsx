'use client'

import { createContext, useContext, useEffect, useState, ReactNode } from 'react'

export interface BrandingConfig {
  app_name: string
  icon_url: string
  company_name: string
  primary_color: string
}

interface BrandingContextType {
  branding: BrandingConfig
  isLoading: boolean
  setBranding: (b: BrandingConfig) => void
}

const defaultBranding: BrandingConfig = {
  app_name: 'vaporRMM',
  icon_url: '',
  company_name: 'Vaporware RMM',
  primary_color: '#3b82f6',
}

const BrandingContext = createContext<BrandingContextType>({
  branding: defaultBranding,
  isLoading: true,
  setBranding: () => {},
})

export function useBranding() {
  return useContext(BrandingContext)
}

function hexToHsl(hex: string): { h: number; s: number; l: number } | null {
  const result = /^#?([a-f\d]{2})([a-f\d]{2})([a-f\d]{2})$/i.exec(hex)
  if (!result) return null

  let r = parseInt(result[1], 16) / 255
  let g = parseInt(result[2], 16) / 255
  let b = parseInt(result[3], 16) / 255

  const max = Math.max(r, g, b)
  const min = Math.min(r, g, b)
  let h = 0
  let s = 0
  const l = (max + min) / 2

  if (max !== min) {
    const d = max - min
    s = l > 0.5 ? d / (2 - max - min) : d / (max + min)
    switch (max) {
      case r: h = (g - b) / d + (g < b ? 6 : 0); break
      case g: h = (b - r) / d + 2; break
      case b: h = (r - g) / d + 4; break
    }
    h *= 60
  }

  return { h: Math.round(h), s: Math.round(s * 100), l: Math.round(l * 100) }
}

function darken(hex: string, amount: number): string {
  const hsl = hexToHsl(hex)
  if (!hsl) return hex
  return `hsl(${hsl.h}, ${hsl.s}%, ${Math.max(0, hsl.l - amount)}%)`
}

function lighten(hex: string, amount: number): string {
  const hsl = hexToHsl(hex)
  if (!hsl) return hex
  return `hsl(${hsl.h}, ${hsl.s}%, ${Math.min(100, hsl.l + amount)}%)`
}

export function BrandingProvider({ children }: { children: ReactNode }) {
  const [branding, setBranding] = useState<BrandingConfig>(defaultBranding)
  const [isLoading, setIsLoading] = useState(true)

  useEffect(() => {
    async function loadBranding() {
      try {
        const res = await fetch('/api/branding/')
        if (res.ok) {
          const data = await res.json()
          setBranding(data)

          // Apply brand colors as CSS variables
          const root = document.documentElement
          const primary = data.primary_color || '#3b82f6'
          root.style.setProperty('--brand-primary', primary)
          root.style.setProperty('--brand-secondary', darken(primary, 15))
          root.style.setProperty('--brand-accent', lighten(primary, 20))
          root.style.setProperty('--brand-primary-light', lighten(primary, 40))
          root.style.setProperty('--brand-primary-dark', darken(primary, 20))
        }
      } catch {
        // Use defaults
      } finally {
        setIsLoading(false)
      }
    }
    loadBranding()
  }, [])

  return (
    <BrandingContext.Provider value={{ branding, isLoading, setBranding }}>
      {children}
    </BrandingContext.Provider>
  )
}
