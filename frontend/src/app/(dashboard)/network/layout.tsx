"use client"

import { SecurityState } from "@/components/security/security-state"

/**
 * The Network section's pages. The firewall moved here from Security and
 * still reads the posture's findings about it, and every page says how this
 * browser reaches the machine — the address the guards protect — from the
 * same exposure, so the section renders Security's state rather than polling
 * a second copy of it. Each page reads its own part of the network itself.
 */
export default function NetworkLayout({ children }: { children: React.ReactNode }) {
  return <SecurityState>{children}</SecurityState>
}
