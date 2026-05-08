'use client'

import { createContext, useCallback, useContext, useEffect, useState, ReactNode } from 'react'
import { usePathname } from 'next/navigation'
import { usersApi, type CurrentUser } from '@/lib/api'

interface CurrentUserContextType {
  user: CurrentUser | null
  isLoading: boolean
  refresh: () => Promise<void>
}

const CurrentUserContext = createContext<CurrentUserContextType>({
  user: null,
  isLoading: true,
  refresh: async () => {},
})

export function useCurrentUser() {
  return useContext(CurrentUserContext)
}

// Pages where we don't have a session yet — skip /users/me there to avoid
// pointless 401 noise (and recursive redirects in the api.ts interceptor).
const UNAUTH_PATHS = ['/login', '/forgot-password', '/reset-password']

export function CurrentUserProvider({ children }: { children: ReactNode }) {
  const pathname = usePathname()
  const [user, setUser] = useState<CurrentUser | null>(null)
  const [isLoading, setIsLoading] = useState(true)

  const refresh = useCallback(async () => {
    if (typeof window !== 'undefined' && UNAUTH_PATHS.some((p) => window.location.pathname.startsWith(p))) {
      setIsLoading(false)
      return
    }
    try {
      const u = await usersApi.me()
      setUser(u)
    } catch {
      setUser(null)
    } finally {
      setIsLoading(false)
    }
  }, [])

  // Re-fetch when pathname changes so login → / picks up the now-authenticated session
  useEffect(() => { refresh() }, [refresh, pathname])

  return (
    <CurrentUserContext.Provider value={{ user, isLoading, refresh }}>
      {children}
    </CurrentUserContext.Provider>
  )
}
