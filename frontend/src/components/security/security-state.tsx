"use client"
import { useAuth } from "@/hooks/use-auth"
import { get } from "@/lib/api"
import type { Exposure, FirewallStatus } from "@/lib/types"
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
  const {} = useAuth()
  const firewall = usePoll<FirewallStatus>((signal) => get("/firewall/", undefined, signal), 20_000)
  const exposure = usePoll<Exposure>((signal) => get("/exposure", undefined, signal), 60_000)

  return (
    <SecurityProvider
      value={{
        firewall: firewall.data,
        firewallLoading: firewall.loading,
        firewallError: firewall.error,
        firewallLastSuccess: firewall.lastSuccess,
        exposure: exposure.data,
        refreshFirewall: firewall.refresh,
      }}
    >
      {children}
    </SecurityProvider>
  )
}
