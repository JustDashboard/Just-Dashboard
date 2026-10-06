import { covers } from "@/lib/cidr"
import type { FirewallStatus } from "@/lib/types"

export type RequestBlockState = "blocking" | "saved" | undefined

/** A saved source deny, rather than a claim that every ingress packet meets it. */
export function hasSourceDeny(firewall: FirewallStatus | undefined, ip: string): boolean {
  return Boolean(
    firewall?.rules?.some(
      (rule) =>
        ["DENY", "REJECT", "DROP"].includes(rule.action.toUpperCase()) &&
        ["", "IN"].includes((rule.direction ?? "").toUpperCase()) &&
        !rule.port &&
        ["", "ANY", "ALL"].includes((rule.protocol ?? "").toUpperCase()) &&
        ["", "ANY", "ANYWHERE", "ANYWHERE (V6)", "0.0.0.0/0", "::/0"].includes(
          rule.to.trim().toUpperCase(),
        ) &&
        covers(rule.from, ip),
    ),
  )
}

export function blockUnavailable(firewall: FirewallStatus | undefined, error?: Error): string {
  if (error) return "The host firewall could not be read. Open Firewall to check it."
  if (!firewall) return "Checking the host firewall…"
  if (firewall.error) return firewall.error
  if (!firewall.available) return "No supported host firewall is available."
  if (!firewall.enabled) return "The host firewall is disabled. Enable it in Firewall first."
  if (!firewall.capabilities?.editable)
    return firewall.capabilities?.readOnlyReason || "This host firewall is read-only."
  return ""
}
