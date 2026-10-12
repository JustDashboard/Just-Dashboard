import type { SecurityFinding } from "./types"

/** Only an observed risk belongs in Attention; read-only ownership and logging preferences do not. */
export function needsSecurityAttention(finding: Pick<SecurityFinding, "level">) {
  return finding.level === "critical" || finding.level === "warning"
}

const SSH_SETTINGS: Record<string, { key: string; label: string }> = {
  "ssh.permitrootlogin=prohibit-password": {
    key: "permitrootlogin",
    label: "Review root SSH login",
  },
  "ssh.permitemptypasswords=no": {
    key: "permitemptypasswords",
    label: "Review empty SSH passwords",
  },
  "ssh.passwordauthentication=no": {
    key: "passwordauthentication",
    label: "Review SSH password login",
  },
}

export function securityRemedy(
  finding: SecurityFinding,
  can: (capability: "system.admin" | "destructive") => boolean,
): { href?: string; label: string; apply: boolean } {
  // The owning controls include access previews and lockout guards. A finding must not bypass them.
  if (finding.area === "ssh") {
    if (!can("system.admin"))
      return { label: "An administrator can review the SSH settings", apply: false }
    const setting = finding.fix ? SSH_SETTINGS[finding.fix] : undefined
    return {
      href: setting
        ? `/security/ssh?setting=${setting.key}#ssh-${setting.key}`
        : "/security/ssh#ssh-settings",
      label: setting?.label ?? "Review SSH controls",
      apply: false,
    }
  }
  if (finding.fix === "firewall.enable")
    return {
      href: can("system.admin") ? "/network/firewall#firewall-enabled" : "/network/firewall",
      label: can("system.admin") ? "Review firewall enforcement" : "Inspect firewall policy",
      apply: false,
    }
  const pages: Record<string, { href: string; label: string }> = {
    exposure: {
      href: "/dashboard/configuration#allowlist",
      label: "Review network allowlist",
    },
    firewall: { href: "/network/firewall", label: "Review firewall controls" },
    ssh: { href: "/security/ssh", label: "Review SSH controls" },
    intrusion: { href: "/security/intrusion", label: "Review intrusion protection" },
    ports: { href: "/proxy/ports", label: "Inspect listener and owner" },
    tls: { href: "/proxy/certificates", label: "Review certificate controls" },
    updates: { href: "/packages", label: "Review host updates" },
  }
  if (finding.area === "exposure" && !can("system.admin"))
    return { label: "An administrator can change the network allowlist", apply: false }
  return {
    ...(pages[finding.area] ?? { href: "/security", label: "Review security evidence" }),
    apply: false,
  }
}
