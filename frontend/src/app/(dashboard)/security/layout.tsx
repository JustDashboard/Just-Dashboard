"use client"

import { SecurityState } from "@/components/security/security-state"

/**
 * Security is four pages, not one screen of tabs — the posture and the ways
 * in, sshd, intrusion prevention and the login record. The rail lists them;
 * `SecurityState` owns what every one of them reads. The firewall, the
 * connections, the interfaces and the tools moved to the Network section,
 * whose layout renders the same state.
 */
export default function SecurityLayout({ children }: { children: React.ReactNode }) {
  return <SecurityState>{children}</SecurityState>
}
