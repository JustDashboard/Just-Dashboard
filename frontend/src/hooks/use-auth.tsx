"use client"

import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react"
import { useRouter } from "next/navigation"
import { ApiError, get, post } from "@/lib/api"
import { forgetWorkingState } from "@/lib/view-state"
import type { AuthStatus, Capability } from "@/lib/types"

type AuthContextValue = {
  status: AuthStatus | null
  loading: boolean
  sessionEnded: boolean
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
  const [sessionEnded, setSessionEnded] = useState(false)
  const recovering = useRef(false)
  const generation = useRef(0)
  const currentStatus = useRef<AuthStatus | null>(null)
  const logoutPending = useRef<Promise<void> | null>(null)
  const workingOwner = useRef<number | null>(null)
  const router = useRouter()

  const applyStatus = useCallback((next: AuthStatus | null) => {
    if (next?.user?.id !== undefined) {
      if (workingOwner.current !== null && workingOwner.current !== next.user.id)
        forgetWorkingState()
      workingOwner.current = next.user.id
    }
    currentStatus.current = next
    setStatus(next)
  }, [])

  const refresh = useCallback(async () => {
    const requested = generation.current
    try {
      const next = await get<AuthStatus>("/auth/session")
      if (requested !== generation.current) return currentStatus.current
      applyStatus(next)
      return next
    } catch (err) {
      // A 401 here is the normal "not signed in" answer, not a failure.
      if (err instanceof ApiError && err.isAuthProblem) {
        if (requested !== generation.current) return currentStatus.current
        applyStatus(null)
        return null
      }
      throw err
    } finally {
      setLoading(false)
    }
  }, [applyStatus])

  useEffect(() => {
    const recover = () => {
      if (!currentStatus.current?.authenticated || recovering.current || logoutPending.current)
        return
      recovering.current = true
      const requested = generation.current
      // A late request can fail after another sign-in completed. Recheck once
      // before replacing that new session; ordinary transport failures never
      // emit this event.
      void refresh()
        .then((next) => {
          if (requested !== generation.current) return
          if (!next?.authenticated) {
            setSessionEnded(true)
            router.replace("/login?reason=session")
          }
        })
        .catch(() => {
          // A failed recheck is not evidence that a newer sign-in ended.
          // Keep the current identity and allow the next request to retry.
        })
        .finally(() => {
          recovering.current = false
        })
    }
    window.addEventListener("jd:authentication-lost", recover)
    return () => window.removeEventListener("jd:authentication-lost", recover)
  }, [status?.authenticated, refresh, router])

  useEffect(() => {
    const requirePasswordChange = () => {
      generation.current++
      const current = currentStatus.current
      applyStatus(
        current ? { ...current, authenticated: false, needsPasswordChange: true } : current,
      )
      router.replace("/login")
    }
    window.addEventListener("jd:password-change-required", requirePasswordChange)
    return () => window.removeEventListener("jd:password-change-required", requirePasswordChange)
  }, [applyStatus, router])

  useEffect(() => {
    // Establishing who the caller is on mount: the canonical case for an
    // effect, since it subscribes this app to state the server owns.
    let cancelled = false
    const requested = generation.current
    get<AuthStatus>("/auth/session")
      .then((next) => {
        if (!cancelled && requested === generation.current) applyStatus(next)
      })
      .catch((err) => {
        if (cancelled || requested !== generation.current) return
        if (!(err instanceof ApiError && err.isAuthProblem)) {
          console.error("session probe failed", err)
        }
        applyStatus(null)
      })
      .finally(() => !cancelled && setLoading(false))
    return () => {
      cancelled = true
    }
  }, [applyStatus])

  const login = useCallback(
    async (username: string, password: string) => {
      // The logout response expires the cookie. Finish it before accepting a
      // newer login cookie, including when Sign out was pressed more than once.
      await logoutPending.current?.catch(() => undefined)
      const requested = ++generation.current
      const next = await post<AuthStatus>("/auth/login", { username, password })
      if (requested !== generation.current) return next
      setSessionEnded(false)
      applyStatus(next)
      return next
    },
    [applyStatus],
  )

  const verifyTotp = useCallback(
    async (code: string) => {
      await logoutPending.current?.catch(() => undefined)
      const requested = ++generation.current
      const next = await post<AuthStatus>("/auth/2fa/verify", { code })
      if (requested === generation.current) applyStatus(next)
      return next
    },
    [applyStatus],
  )

  const logout = useCallback(async () => {
    if (logoutPending.current) return logoutPending.current
    const requested = ++generation.current
    forgetWorkingState()
    const pending = (async () => {
      try {
        await post("/auth/logout")
      } finally {
        if (requested === generation.current) {
          generation.current++
          applyStatus(null)
          router.push("/login")
        }
      }
    })()
    logoutPending.current = pending
    try {
      await pending
    } finally {
      if (logoutPending.current === pending) logoutPending.current = null
    }
  }, [applyStatus, router])

  const can = useCallback(
    (capability: Capability) => status?.capabilities?.includes(capability) ?? false,
    [status],
  )

  const value = useMemo(
    () => ({ status, loading, sessionEnded, refresh, login, verifyTotp, logout, can }),
    [status, loading, sessionEnded, refresh, login, verifyTotp, logout, can],
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error("useAuth must be used inside AuthProvider")
  return ctx
}
