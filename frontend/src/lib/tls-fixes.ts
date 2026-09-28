/**
 * Which remedy a TLS finding gets, worked out apart from the page so the
 * mapping can be tested. The scan only says what kind of problem it saw
 * (`finding.fix`); whether the dashboard can carry the remedy out depends on
 * what answered — a site it wrote, a file somebody wrote by hand, a
 * certificate certbot keeps, or nothing on this host at all — which is the
 * scan's `origin`.
 */

import type { ScanFinding, SiteSpec, TLSScan } from "./types"

/** The fixes that change the site's own file through the site form's endpoints. */
export type SiteFix = "force-https" | "hsts" | "security-headers" | "fullchain"

export type TLSFix =
  /** nginx still serves a certificate renewed on disk. */
  | { kind: "reload" }
  | { kind: "renew"; certName: string }
  | { kind: "issue"; href: string }
  | { kind: "site"; fix: SiteFix; site: string }
  | { kind: "directive"; names: string[] }

const SITE_FIXES: readonly string[] = ["force-https", "hsts", "security-headers", "fullchain"]

const LIVE_DIR = "/etc/letsencrypt/live/"

/**
 * The certbot lineage a certificate file belongs to: certbot names each one
 * after its directory under live/, and renews by that name.
 */
export function certbotLineage(path: string): string | undefined {
  if (!path.startsWith(LIVE_DIR)) return undefined
  const name = path.slice(LIVE_DIR.length).split("/")[0]
  return name || undefined
}

/**
 * The remedy for one finding, or undefined when there is nothing this page
 * can do that would be true to offer — a certificate nobody on this host
 * serves cannot be renewed from here, and a site setting cannot be changed
 * when no site file answered.
 */
export function fixFor(finding: ScanFinding, scan: TLSScan): TLSFix | undefined {
  const origin = scan.origin
  switch (finding.fix) {
    case "renew": {
      // Renewed on disk and never reloaded: renewing again spends a
      // certificate and changes nothing a visitor sees.
      if (origin?.state === "stale") return { kind: "reload" }
      const name = origin?.file && certbotLineage(origin.file.path)
      return name ? { kind: "renew", certName: name } : undefined
    }
    case "issue":
      return {
        kind: "issue",
        href: `/proxy/certificates?issue=${encodeURIComponent(scan.domain)}`,
      }
    case "protocols":
      return {
        kind: "directive",
        // A handshake refused for a current client is the cipher list as
        // often as the protocol list.
        names:
          finding.id === "tls.legacy-only" ? ["ssl_protocols", "ssl_ciphers"] : ["ssl_protocols"],
      }
    case "server-tokens":
      // Unset, it is on; the sheet says where to add it when no file sets it.
      return { kind: "directive", names: ["server_tokens"] }
    default:
      if (finding.fix && SITE_FIXES.includes(finding.fix) && origin?.site)
        return { kind: "site", fix: finding.fix as SiteFix, site: origin.site }
      return undefined
  }
}

export const FIX_LABEL: Record<TLSFix["kind"] | SiteFix, string> = {
  reload: "Reload nginx",
  renew: "Renew",
  issue: "Issue a certificate",
  directive: "Show where it is set",
  site: "Change the site",
  "force-https": "Force HTTPS",
  hsts: "Turn on HSTS",
  "security-headers": "Add security headers",
  fullchain: "Use fullchain.pem",
}

export function fixLabel(fix: TLSFix): string {
  if (fix.kind === "site") return FIX_LABEL[fix.fix]
  if (fix.kind === "directive") return `Show where ${fix.names[0]} is set`
  return FIX_LABEL[fix.kind]
}

/**
 * The site's spec with the fix applied, or why it cannot be: the switch is
 * already on and what the scan saw comes from somewhere else, or the change
 * needs something the site does not have.
 */
export function applySiteFix(
  spec: SiteSpec,
  fix: SiteFix,
): { spec: SiteSpec; reason?: undefined } | { spec?: undefined; reason: string } {
  if (!spec.tls)
    return {
      reason: `${spec.name} is not served over TLS, so the answer on this port came from another server block.`,
    }
  switch (fix) {
    case "force-https":
      if (spec.forceHttps)
        return {
          reason: `${spec.name} already redirects plain HTTP, so port 80 is answered by another server block — the default server for port 80 is the usual one.`,
        }
      return { spec: { ...spec, forceHttps: true } }
    case "hsts":
      if (spec.hsts)
        return {
          reason: `${spec.name} already sends HSTS for six months. A shorter or missing header in the scan comes from a location that sets add_header of its own, which discards the server's, or from the application.`,
        }
      return { spec: { ...spec, hsts: true } }
    case "security-headers":
      if (spec.securityHeaders)
        return {
          reason: `${spec.name} already sends the security headers. A location that sets add_header of its own discards the server's, which is the usual reason they are missing.`,
        }
      return { spec: { ...spec, securityHeaders: true } }
    case "fullchain": {
      const path = spec.certPath ?? ""
      if (!path.endsWith("/cert.pem"))
        return {
          reason: `${spec.name} names ${path || "no certificate"}, not a cert.pem with a fullchain.pem beside it. Point it at a file holding the certificate followed by its intermediates.`,
        }
      return { spec: { ...spec, certPath: path.replace(/cert\.pem$/, "fullchain.pem") } }
    }
  }
}
