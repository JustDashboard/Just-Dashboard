"use client"

import { createContext, useContext } from "react"
import type { Exposure, FirewallStatus, Posture, SecurityFinding } from "@/lib/types"

/**
 * The three verdicts the whole Security section is built around.
 *
 * `posture` (slow), `firewall` (fast) and `exposure` (how this very browser
 * reaches the panel) are read on every sub-page — the Overview shows all three,
 * the Firewall page shows the rules and the findings about them, the jail
 * sheet offers the caller's own address to the allowlist — so the layout polls
 * each once and the pages read them here rather than each starting its own.
 *
 * `applyFix` lives here too, not copied into each page: it is the whole
 * difference between a warning and a remedy, and the two on it that can cost
 * access to the machine (enabling the firewall, changing sshd) go through the
 * typed-phrase confirmation. A second copy is where one of them quietly stops
 * asking.
 */
export type SecurityContextValue = {
  posture: Posture | undefined
  postureLoading: boolean
  firewall: FirewallStatus | undefined
  firewallLoading: boolean
  firewallError: Error | undefined
  exposure: Exposure | undefined
  refreshPosture: () => void
  refreshFirewall: () => void
  /** A finding's server-named remedy, wrapped in the confirmation it deserves. */
  applyFix: (finding: SecurityFinding) => void
}

const SecurityContext = createContext<SecurityContextValue | null>(null)

export const SecurityProvider = SecurityContext.Provider

export function useSecurity() {
  const value = useContext(SecurityContext)
  if (!value) throw new Error("useSecurity must be used inside the security layout")
  return value
}
