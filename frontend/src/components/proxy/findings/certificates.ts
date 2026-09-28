import type { Certificate, CertbotState } from "@/lib/types"
import type { ProxyFinding } from "@/components/proxy/findings/shared"

export type CertificateFindingInput = {
  certs?: Certificate[]
  /** `null` when certbot is not installed, which is not a finding. */
  certbot?: CertbotState | null
}

/** A certificate that cannot be read, has expired or is about to, and a renewal nothing runs. */
export function certificateFindings({ certs, certbot }: CertificateFindingInput): ProxyFinding[] {
  const out: ProxyFinding[] = []

  for (const cert of certs ?? []) {
    const usedBy = cert.usedBy.length ? ` Used by ${cert.usedBy.join(", ")}.` : ""
    if (cert.error) {
      out.push({
        id: `cert.error.${cert.path || cert.name}`,
        level: "warning",
        title: `${cert.name} could not be read`,
        detail: cert.error,
        advice:
          "A site pointing at a certificate nginx cannot read fails its next reload. Fix or replace the file, or point the site elsewhere.",
        meta: "certificate",
        href: "/proxy/certificates",
      })
    } else if (cert.expired) {
      out.push({
        id: `cert.expired.${cert.path}`,
        // The days left move every day; the certificate is what the finding is about.
        fingerprint: cert.fingerprint ?? cert.path,
        level: "critical",
        title: `${cert.name} has expired`,
        detail: `Expired ${-cert.daysLeft} day${cert.daysLeft === -1 ? "" : "s"} ago; every browser refuses it now.${usedBy}`,
        advice: "Renew it, then find out why the renewal did not run on its own.",
        meta: "certificate",
        href: "/proxy/certificates",
      })
    } else if (cert.expiring) {
      out.push({
        id: `cert.expiring.${cert.path}`,
        fingerprint: cert.fingerprint ?? cert.path,
        level: cert.daysLeft <= 7 ? "critical" : "warning",
        title: `${cert.name} expires in ${cert.daysLeft} day${cert.daysLeft === 1 ? "" : "s"}`,
        detail: `Inside Let's Encrypt's renewal window and still not renewed.${usedBy}`,
        advice:
          "certbot renews at thirty days. A certificate still here a week later means the timer is not running.",
        meta: "certificate",
        href: "/proxy/certificates",
      })
    }
  }

  if (certbot && certbot.available && !certbot.autoRenew && certbot.certs.length > 0) {
    out.push({
      id: "certbot.no-timer",
      level: "critical",
      title: "Nothing is scheduled to renew certbot's certificates",
      detail: `No certbot timer and no cron entry was found for ${certbot.certs.length} certificate${certbot.certs.length === 1 ? "" : "s"}.`,
      advice: certbot.renewUnit
        ? `${certbot.renewUnit} is installed but not running. Turn it on from the Certificates page.`
        : "Install certbot's timer or a cron entry; without one every certificate here expires in ninety days.",
      meta: "renewal",
      href: "/proxy/certificates",
      remedy: certbot.renewUnit ? { kind: "start-unit", unit: certbot.renewUnit } : undefined,
    })
  }

  return out
}
