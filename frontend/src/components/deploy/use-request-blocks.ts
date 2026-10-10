"use client"

import { useEffect, useRef, useState } from "react"
import { get } from "@/lib/api"
import type { FirewallStatus } from "@/lib/types"
import { notify } from "@/lib/toast"
import { usePoll } from "@/hooks/use-poll"
import { blockAddress } from "@/components/security/address-verbs"
import { blockUnavailable, hasSourceDeny, type RequestBlockState } from "./request-blocks"

export function useRequestBlocks(enabled: boolean, subject: string, dockerIngress = false) {
  const firewall = usePoll<FirewallStatus>(
    (signal) => get("/firewall/", undefined, signal),
    20_000,
    [],
    { enabled },
  )
  const pending = useRef(new Set<string>())
  const successful = useRef(new Map<string, FirewallStatus | undefined>())
  const currentSnapshot = useRef(firewall.data)
  useEffect(() => {
    currentSnapshot.current = firewall.data
  }, [firewall.data])
  const [blocking, setBlocking] = useState<ReadonlySet<string>>(new Set())
  // A successful write is visible immediately. The next firewall snapshot
  // takes over, so removing the rule elsewhere restores the action here too.
  const [saved, setSaved] = useState<Record<string, FirewallStatus | undefined>>({})
  const unavailable = blockUnavailable(firewall.data, firewall.error)
  const state = (ip: string): RequestBlockState => {
    if (blocking.has(ip)) return "blocking"
    if (
      hasSourceDeny(firewall.data, ip) ||
      (Object.hasOwn(saved, ip) && saved[ip] === firewall.data)
    )
      return "saved"
    return undefined
  }

  const block = async (ip: string) => {
    if (
      !enabled ||
      unavailable ||
      pending.current.has(ip) ||
      state(ip) === "saved" ||
      (successful.current.has(ip) && successful.current.get(ip) === firewall.data)
    )
      return
    // The ref guards a second press before React has painted the busy state.
    pending.current.add(ip)
    setBlocking(new Set(pending.current))
    try {
      await blockAddress(ip, `blocked from ${subject} requests`)
      const snapshot = currentSnapshot.current
      successful.current.set(ip, snapshot)
      setSaved((previous) => ({ ...previous, [ip]: snapshot }))
      notify.success(`Deny rule saved for ${ip}`, {
        description: dockerIngress
          ? "Past requests remain in this record. Docker-published ingress ports can bypass this host firewall rule."
          : "Past requests remain in this record. The host firewall rule does not expire.",
      })
    } catch (error) {
      // Another tab may have saved the same deny since our last read. UFW
      // refuses that duplicate; only a fresh rule read can call it saved.
      let current: FirewallStatus | undefined
      try {
        current = await get<FirewallStatus>("/firewall/")
      } catch {
        // Keep the mutation's useful reason when the verification also fails.
      }
      if (current && !current.error && hasSourceDeny(current, ip)) {
        const snapshot = currentSnapshot.current
        successful.current.set(ip, snapshot)
        setSaved((previous) => ({ ...previous, [ip]: snapshot }))
        notify.info(`Deny rule already saved for ${ip}`)
      } else {
        notify.error("Could not block the address", error)
      }
    } finally {
      pending.current.delete(ip)
      setBlocking(new Set(pending.current))
      firewall.refresh()
    }
  }

  return { state, unavailable, block }
}
