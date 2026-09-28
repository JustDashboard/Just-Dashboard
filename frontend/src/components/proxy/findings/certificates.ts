import {
  expiredAgo,
  hookFailureText,
  runPhrase,
  sinceDay,
  standingFailures,
  unreloadedRenewals,
} from "@/lib/certificates"
import type { Certificate, CertbotState } from "@/lib/types"
import type { ProxyFinding } from "@/components/proxy/findings/shared"

export type CertificateFindingInput = {
  certs?: Certificate[]
  /** `null` when certbot is not installed, which is not a finding. */
  certbot?: CertbotState | null
}

/**
 * A certificate that cannot be read, is a test certificate, has expired or is
 * about to; a renewal nothing runs, one whose last run failed or whose hook
 * failed, one that will fail, and renewals nginx never reloads for.
 */
export function certificateFindings({ certs, certbot }: CertificateFindingInput): ProxyFinding[] {
  const out: ProxyFinding[] = []

  for (const cert of certs ?? []) {
    // Caddy renews what it serves on its own schedule: its renewal window is
    // nothing to act on, and one no route uses is Caddy's to clear away.
    const caddy = cert.source === "caddy"
    if (caddy && !cert.error && !cert.staging && !(cert.expired && cert.usedBy.length > 0)) continue
    const usedBy = cert.usedBy.length ? ` Used by ${cert.usedBy.join(", ")}.` : ""
    if (cert.error) {
      out.push({
        id: `cert.error.${cert.path || cert.name}`,
        level: "warning",
        title: caddy ? "Caddy's certificates could not be read" : `${cert.name} could not be read`,
        detail: cert.error,
        advice: caddy
          ? "The list leaves out what the Docker ingress serves until it can read them. Check that the Caddy container is running and Docker answers."
          : "A site pointing at a certificate nginx cannot read fails its next reload. Fix or replace the file, or point the site elsewhere.",
        meta: "certificate",
        href: "/proxy/certificates",
      })
    } else if (cert.staging) {
      // Only a site serving it makes it an outage; one nothing names is a
      // leftover to clear up.
      out.push({
        id: `cert.staging.${cert.path}`,
        level: cert.usedBy.length ? "critical" : "warning",
        title: `${cert.name} is a test certificate`,
        detail: `A staging authority signed it, so every browser refuses it.${usedBy}`,
        advice: stagingAdvice(cert, certbot),
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
        detail: `Expired ${expiredAgo(cert.notAfter)}; every browser refuses it now.${usedBy}`,
        advice: caddy
          ? "Caddy renews it itself and did not. Its container log says why: usually DNS or inbound port 80 or 443."
          : "Renew it, then find out why the renewal did not run on its own.",
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

  const health = certbot?.available ? certbot.health : undefined
  if (health?.state === "failed") {
    // Its reason is the certificates it failed on, or else why the whole
    // run failed.
    const standing = standingFailures(health)
    const why = standing.length
      ? standing.map((f) => `${f.lineage}: ${f.reason}`).join(" ")
      : (health.reason ?? `It exited ${health.exitStatus ?? "with an error"}.`)
    const when = health.lastRun ? ` ${runPhrase(health.lastRun)}` : ""
    const since = health.failingSince
      ? ` Every run since ${sinceDay(health.failingSince)} has failed.`
      : ""
    out.push({
      id: "certbot.renewal-failing",
      level: "critical",
      title: "certbot's last renewal failed",
      detail: `${health.service ?? "The renewal"} failed${when}. ${why}${since}`,
      advice:
        "Fix what it names, then use Dry run on the certificate and Run now under Automatic renewal on the Certificates page to confirm.",
      meta: "renewal",
      href: "/proxy/certificates",
    })
  }

  if (health?.hookFailures?.length) {
    // certbot only warns when a hook fails, so the run itself passed.
    const when = health.lastRun ? ` ${runPhrase(health.lastRun)}` : ""
    out.push({
      id: "certbot.hook-failed",
      level: "warning",
      title: "A renewal hook failed",
      detail: `On ${health.service ?? "the renewal"}'s last run${when}: ${health.hookFailures.map(hookFailureText).join(" ")}`,
      advice:
        "certbot only warns when a hook fails, so the run still reads as passed. A reload hook that failed left nginx on the old certificate: fix what it reports, then reload nginx.",
      meta: "renewal",
      href: "/proxy/certificates",
    })
  }

  for (const lineage of certbot?.available ? certbot.certs : []) {
    if (!lineage.willFail?.length) continue
    // Inside the renewal window certbot tries at every run; before it, the
    // failure is still weeks away.
    const due = !lineage.valid || lineage.daysLeft <= 30
    out.push({
      id: `certbot.will-fail.${lineage.name}`,
      level: due ? "critical" : "warning",
      title: `${lineage.name} will fail to renew`,
      detail: lineage.willFail.join(" "),
      advice: due
        ? "It is inside its renewal window, so certbot tries at every run and fails the same way until this is fixed."
        : "Fix this before it reaches its renewal window, thirty days before it expires.",
      meta: "renewal",
      href: "/proxy/certificates",
    })
  }

  if (certbot?.available) {
    const { lineages, sites } = unreloadedRenewals(certbot)
    if (lineages.length > 0) {
      out.push({
        id: "certbot.no-reload",
        level: "warning",
        title: "Renewed certificates will not reach nginx",
        detail: `certbot renews ${lineages.join(", ")} without reloading nginx, so ${sites.join(", ")} ${sites.length === 1 ? "keeps" : "keep"} serving the old certificate until it expires.`,
        advice:
          "Turn on “Reload nginx after every renewal” under Automatic renewal on the Certificates page.",
        meta: "renewal",
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

/**
 * The way to a real certificate, through what the Certificates page offers:
 * without certbot it issues nothing, and while JD_ACME_DIRECTORY names a
 * staging authority it offers no real issuance, since whatever it ordered
 * would be refused just the same.
 */
function stagingAdvice(cert: Certificate, certbot: CertbotState | null | undefined): string {
  if (cert.source === "caddy") {
    return "Caddy obtained it from the staging authority JD_ACME_DIRECTORY names. Point it at a production directory, restart the dashboard and deploy again, and Caddy obtains a real one."
  }
  const certbotOwns = cert.source === "certbot"
  if (certbot === null) {
    return "certbot is not installed, so this dashboard cannot issue a real certificate. Import one for these names from the Certificates page and point the site at it."
  }
  if (certbot?.testAuthority) {
    return `JD_ACME_DIRECTORY names a staging authority, so what this dashboard issues is a test certificate too. Clear it or point it at a production directory and restart the dashboard, then ${certbotOwns ? "replace this one" : "issue a real one and point the site at it"}.`
  }
  return certbotOwns
    ? "Replace it with a real certificate from the Certificates page, then reload nginx."
    : "Issue a real certificate for these names from the Certificates page and point the site at it."
}
