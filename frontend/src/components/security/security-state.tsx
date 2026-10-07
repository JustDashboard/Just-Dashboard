"use client"

import { notify } from "@/lib/toast"
import { get, post } from "@/lib/api"
import type { Exposure, FirewallStatus, Posture, SecurityFinding } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { SecurityProvider } from "@/components/security/security-context"

/**
 * The three verdicts the Security and Network sections read (`posture`,
 * `firewall`, `exposure`), polled once for whichever section is open, and the
 * one action that must not be duplicated (`applyFix`, with ordinary
 * confirmations). Both layouts render it: the firewall moved to the Network
 * section in 0.7.1 and still reads the posture's findings about it, and the
 * Network pages say how this browser reaches the machine from the same
 * exposure the Security overview opens on.
 *
 * It does not gate either section: fail2ban absent, ufw absent and an
 * unreadable sshd are independent absences, and each page reports its own — an
 * unavailable check is information here, not an error.
 */
export function SecurityState({ children }: { children: React.ReactNode }) {
  const { confirm, dialog } = useConfirm()

  const posture = usePoll<Posture>((signal) => get("/security/posture", undefined, signal), 120_000)
  const firewall = usePoll<FirewallStatus>((signal) => get("/firewall/", undefined, signal), 20_000)
  const exposure = usePoll<Exposure>((signal) => get("/exposure", undefined, signal), 60_000)

  /**
   * A finding's one-click remedy. The server names the action; this maps it to
   * the request that carries it out and to the confirmation it deserves — the
   * two that can cost access to the machine still need confirmation.
   */
  const applyFix = (finding: SecurityFinding) => {
    const fix = finding.fix ?? ""
    if (fix === "firewall.enable") {
      confirm({
        title: "Enable firewall",
        confirmLabel: "Enable",
        description: (
          <p className="text-destructive">
            ufw applies its default-deny policy immediately. If the port this dashboard listens on
            is not already allowed, you will lose access.
          </p>
        ),
        action: async (c) => {
          await post("/firewall/enabled", { enabled: true }, { confirm: c })
          firewall.refresh()
          posture.refresh()
        },
      })
      return
    }
    if (fix.startsWith("ssh.")) {
      const [key, value] = fix.slice(4).split("=")
      confirm({
        title: finding.fixLabel ?? "Apply SSH change",
        confirmLabel: "Test and apply",
        description: (
          <div className="space-y-2">
            <p>
              Sets <code className="font-mono">{key}</code> to{" "}
              <code className="font-mono">{value}</code>, tests it with sshd&rsquo;s own parser and
              puts the file back if the test fails.
            </p>
            <p className="text-destructive">
              Keep this session open and confirm you can still log in from a second terminal before
              closing it.
            </p>
          </div>
        ),
        action: async (c) => {
          await post("/ssh/config", { settings: { [key]: value } }, { confirm: c })
          notify.success("Applied")
          posture.refresh()
        },
      })
      return
    }
    notify.info(finding.title, { description: finding.advice })
  }

  return (
    <SecurityProvider
      value={{
        posture: posture.data,
        postureLoading: posture.loading,
        firewall: firewall.data,
        firewallLoading: firewall.loading,
        firewallError: firewall.error,
        exposure: exposure.data,
        refreshPosture: posture.refresh,
        refreshFirewall: firewall.refresh,
        applyFix,
      }}
    >
      {children}
      {dialog}
    </SecurityProvider>
  )
}
