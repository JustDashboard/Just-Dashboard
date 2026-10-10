import type { SecurityFinding } from "./types"

export function securityRemedy(
  finding: SecurityFinding,
  can: (capability: "system.admin" | "destructive") => boolean,
): { href?: string; label: string; apply: boolean } {
  if (finding.fix && can("system.admin") && can("destructive"))
    return { label: finding.fixLabel ?? "Review and apply", apply: true }
  const pages: Record<string, { href: string; label: string }> = {
    exposure: {
      href: can("system.admin") ? "/dashboard/configuration" : "/security",
      label: "Review network allowlist",
    },
    firewall: { href: "/network/firewall", label: "Review firewall controls" },
    ssh: { href: "/security/ssh", label: "Review SSH controls" },
    intrusion: { href: "/security/intrusion", label: "Review intrusion protection" },
    ports: { href: "/proxy/ports", label: "Inspect listener and owner" },
    tls: { href: "/proxy/certificates", label: "Review certificate controls" },
    updates: { href: "/packages", label: "Review host updates" },
  }
  return {
    ...(pages[finding.area] ?? { href: "/security", label: "Review security evidence" }),
    apply: false,
  }
}
