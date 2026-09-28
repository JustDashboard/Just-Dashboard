"use client"

import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import { ApiError, get, post } from "@/lib/api"
import { forgetWorkingState } from "@/lib/view-state"
import type { AuthStatus, Capability } from "@/lib/types"

type AuthContextValue = {
  status: AuthStatus | null
  loading: boolean
  refresh: () => Promise<AuthStatus | null>
  login: (username: string, password: string) => Promise<AuthStatus>
  verifyTotp: (code: string) => Promise<AuthStatus>
  logout: () => Promise<void>
  can: (capability: Capability) => boolean
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [status, setStatus] = useState<AuthStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const router = useRouter()

  const refresh = useCallback(async () => {
    try {
      const next = await get<AuthStatus>("/auth/session")
      setStatus(next)
      return next
    } catch (err) {
      // A 401 here is the normal "not signed in" answer, not a failure.
      if (err instanceof ApiError && err.isAuthProblem) {
        setStatus(null)
        return null
      }
      throw err
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    const requirePasswordChange = () => {
      setStatus((current) =>
        current ? { ...current, authenticated: false, needsPasswordChange: true } : current,
      )
      router.replace("/login")
    }
    window.addEventListener("jd:password-change-required", requirePasswordChange)
    return () => window.removeEventListener("jd:password-change-required", requirePasswordChange)
  }, [router])

  useEffect(() => {
    // Establishing who the caller is on mount: the canonical case for an
    // effect, since it subscribes this app to state the server owns.
    let cancelled = false
    get<AuthStatus>("/auth/session")
      .then((next) => !cancelled && setStatus(next))
      .catch((err) => {
        if (cancelled) return
        if (!(err instanceof ApiError && err.isAuthProblem)) {
          console.error("session probe failed", err)
        }
        setStatus(null)
      })
      .finally(() => !cancelled && setLoading(false))
    return () => {
      cancelled = true
    }
  }, [])

  const login = useCallback(async (username: string, password: string) => {
    const next = await post<AuthStatus>("/auth/login", { username, password })
    setStatus(next)
    return next
  }, [])

  const verifyTotp = useCallback(async (code: string) => {
    const next = await post<AuthStatus>("/auth/2fa/verify", { code })
    setStatus(next)
    return next
  }, [])

  const logout = useCallback(async () => {
    try {
      await post("/auth/logout")
    } finally {
      // Whatever was half done belongs to the account that signed out, not to
      // the next one to use this browser.
      forgetWorkingState()
      setStatus(null)
      router.push("/login")
    }
  }, [router])

  const can = useCallback(
    (capability: Capability) => status?.capabilities?.includes(capability) ?? false,
    [status],
  )

  const value = useMemo(
    () => ({ status, loading, refresh, login, verifyTotp, logout, can }),
    [status, loading, refresh, login, verifyTotp, logout, can],
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error("useAuth must be used inside AuthProvider")
  return ctx
}
