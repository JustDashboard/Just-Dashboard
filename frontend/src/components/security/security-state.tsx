"use client"

import { useRouter } from "next/navigation"
import { useAuth } from "@/hooks/use-auth"
import { securityRemedy } from "@/lib/security-remedies"
import { get } from "@/lib/api"
import type { Exposure, FirewallStatus, Posture, SecurityFinding } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { SecurityProvider } from "@/components/security/security-context"

/**
 * The three verdicts the Security and Network sections read (`posture`,
 * `firewall`, `exposure`), polled once for whichever section is open, and the
 * finding hand-off to the owning controls (`applyFix`). Both layouts render it: the firewall moved to the Network
 * section in 0.7.1 and still reads the posture's findings about it, and the
 * Network pages say how this browser reaches the machine from the same
 * exposure the Security overview opens on.
 *
 * It does not gate either section: fail2ban absent, ufw absent and an
 * unreadable sshd are independent absences, and each page reports its own — an
 * unavailable check is information here, not an error.
 */
export function SecurityState({ children }: { children: React.ReactNode }) {
  const router = useRouter()
  const { can } = useAuth()

  const posture = usePoll<Posture>((signal) => get("/security/posture", undefined, signal), 120_000)
  const firewall = usePoll<FirewallStatus>((signal) => get("/firewall/", undefined, signal), 20_000)
  const exposure = usePoll<Exposure>((signal) => get("/exposure", undefined, signal), 60_000)

  // Changes are reviewed at their owning controls, including their access checks and recovery flow.
  const applyFix = (finding: SecurityFinding) => {
    const remedy = securityRemedy(finding, can)
    if (remedy.href) router.push(remedy.href)
  }

  return (
    <SecurityProvider
      value={{
        posture: posture.data,
        postureLoading: posture.loading,
        firewall: firewall.data,
        firewallLoading: firewall.loading,
        firewallError: firewall.error,
        firewallLastSuccess: firewall.lastSuccess,
        exposure: exposure.data,
        refreshPosture: posture.refresh,
        refreshFirewall: firewall.refresh,
        applyFix,
      }}
    >
      {children}
    </SecurityProvider>
  )
}
