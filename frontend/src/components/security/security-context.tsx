"use client"

import { createContext, useContext } from "react"
import type { Exposure, FirewallStatus } from "@/lib/types"

/**
 * The three verdicts the whole Security section is built around.
 *
 * `posture` (slow), `firewall` (fast) and `exposure` (how this very browser
 * reaches the panel) are read on every sub-page — the Overview shows all three,
 * the Firewall page shows the rules and the findings about them, the jail
 * sheet offers the caller's own address to the allowlist — so the layout polls
 * each once and the pages read them here rather than each starting its own.
 *
 * `applyFix` hands findings to the owning page. SSH and firewall changes use
 * that page's complete preview, confirmation and recovery flow.

 */
export type SecurityContextValue = {
  firewall: FirewallStatus | undefined
  firewallLoading: boolean
  firewallError: Error | undefined
  firewallLastSuccess: number | undefined
  exposure: Exposure | undefined
  refreshFirewall: () => void
}

const SecurityContext = createContext<SecurityContextValue | null>(null)

export const SecurityProvider = SecurityContext.Provider

export function useSecurity() {
  const value = useContext(SecurityContext)
  if (!value) throw new Error("useSecurity must be used inside the security layout")
  return value
}
