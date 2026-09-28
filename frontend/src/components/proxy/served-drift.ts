"use client"

import { useRef, useState } from "react"
import { get, post } from "@/lib/api"
import type { DriftReport, ProxyReloadResult } from "@/lib/types"
import { notify } from "@/lib/toast"
import { usePoll } from "@/hooks/use-poll"

/** How long after a reload nginx's new workers have taken over the ports. */
const SETTLE_MS = 1500

/**
 * The served-certificate check, polled every five minutes (the server keeps
 * it that long), and the reload a stale one asks for. After a reload the
 * check runs again at once, for an account that may ask it to.
 */
export function useServedDrift({ onReloaded }: { onReloaded: () => void }) {
  const force = useRef(false)
  const [reloading, setReloading] = useState(false)
  const poll = usePoll((signal) => {
    const query = force.current ? { refresh: "1" } : undefined
    force.current = false
    return get<DriftReport>("/proxy/tls/drift", query, signal)
  }, 300_000)

  const reload = async () => {
    if (reloading) return
    setReloading(true)
    try {
      await post<ProxyReloadResult>("/proxy/reload", { kind: "nginx" })
      notify.success("nginx reloaded", {
        description: "The served certificates are checked again in a moment.",
      })
      onReloaded()
      setTimeout(() => {
        force.current = true
        poll.refresh()
      }, SETTLE_MS)
    } catch (err) {
      notify.error("nginx was not reloaded", err)
      onReloaded()
    } finally {
      setReloading(false)
    }
  }

  return { report: poll.error ? undefined : poll.data, reload, reloading }
}
