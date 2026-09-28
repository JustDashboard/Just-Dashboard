import type { CertbotCert, Job, ServedCertificate } from "@/lib/types"

/**
 * Certificate readings that are pure arithmetic over the API's data, kept
 * here so they are tested without a browser.
 */

const DAY = 86_400_000

/**
 * How long ago a certificate expired. `daysLeft` truncates toward zero, so a
 * certificate that ran out this morning has 0 of them — which read "Expired 0
 * days ago".
 */
export function expiredAgo(notAfter: string, now: number = Date.now()): string {
  const days = Math.floor((now - new Date(notAfter).getTime()) / DAY)
  if (days < 1) return "today"
  if (days === 1) return "yesterday"
  return `${days} days ago`
}

/**
 * Whether the job on screen is a certbot run still going. certbot takes one
 * lock for all of its work, so while one runs every other certbot verb would
 * fail on it — and a second job would replace the running one in the console.
 */
export function certbotRunning(job: Job | null | undefined): boolean {
  return Boolean(job && job.status === "running" && job.kind.startsWith("certbot."))
}

/**
 * The titles the issuance route gives a job, which are how the page tells a
 * test run and the replacement of a test certificate from a plain issuance.
 */
const TEST_RUN = "Test issuance for "
const REPLACEMENT = "Replacing the test certificate for "

/** An issuance job's names: the route joins them with a comma. */
function issuedNames(job: Job): string[] {
  return (job.target ?? "").split(/,\s*/).filter(Boolean)
}

/** The same set of names, in any order and case: what certbot treats as one certificate. */
export function sameNames(a: string[], b: string[]): boolean {
  const left = new Set(a.map((name) => name.toLowerCase()))
  const right = new Set(b.map((name) => name.toLowerCase()))
  return left.size === right.size && [...left].every((name) => right.has(name))
}

/** The names a test run on screen passed for: the moment to offer the real issuance. */
export function testRunPassed(job: Job | null | undefined): string | undefined {
  return job?.kind === "certbot.issue" &&
    job.status === "succeeded" &&
    job.title.startsWith(TEST_RUN)
    ? job.target
    : undefined
}

/**
 * The names whose test certificate a finished issuance replaced on disk.
 * Whether nginx serves the new one is a separate question, answered by
 * asking the sites (`stillServingTest`): certonly reloads nothing itself, but
 * a certbot deploy hook may have.
 */
export function testCertificateReplaced(job: Job | null | undefined): string[] | undefined {
  return job?.kind === "certbot.issue" &&
    job.status === "succeeded" &&
    job.title.startsWith(REPLACEMENT)
    ? issuedNames(job)
    : undefined
}

/**
 * The sites that answered with a test certificate while the file they name
 * holds another: the ones a reload would change. A site that could not be
 * asked, or that serves some other certificate, is not claimed either way.
 */
export function stillServingTest(served: ServedCertificate[] | undefined): string[] {
  return (served ?? [])
    .filter((site) => !site.error && !site.current && site.staging)
    .map((site) => site.site)
}

/**
 * What a reload did for the sites that were serving a test certificate, as
 * they answered afterwards: the toast after "Reload nginx" says only that.
 */
export function afterReload(
  sites: string[],
  served: ServedCertificate[],
): { ok: boolean; description: string } {
  const now = sites.filter((site) => served.some((s) => s.site === site && s.current))
  const still = stillServingTest(served)
  const unknown = sites.filter((site) => !now.includes(site) && !still.includes(site))
  const verb = (names: string[], one: string, many: string) => (names.length === 1 ? one : many)
  return {
    ok: still.length === 0 && unknown.length === 0,
    description: [
      now.length > 0 &&
        `${now.join(", ")} ${verb(now, "serves", "serve")} the real certificate now.`,
      still.length > 0 &&
        `${still.join(", ")} still ${verb(still, "serves", "serve")} the test certificate.`,
      unknown.length > 0 &&
        `What ${unknown.join(", ")} ${verb(unknown, "serves", "serve")} could not be checked.`,
    ]
      .filter(Boolean)
      .join(" "),
  }
}

/** Whether the running job is replacing the test certificate for exactly these names. */
export function replacingTestCertificate(job: Job | null | undefined, domains: string[]): boolean {
  return (
    certbotRunning(job) &&
    job?.kind === "certbot.issue" &&
    job.title.startsWith(REPLACEMENT) &&
    sameNames(issuedNames(job), domains)
  )
}

/**
 * The present participle a lineage shows while the running job acts on it:
 * a renewal or dry run naming it, its revocation, or the real issuance that
 * replaces its test certificate.
 */
export function lineageActivity(
  job: Job | null | undefined,
  lineage: Pick<CertbotCert, "name" | "domains">,
): string | undefined {
  if (replacingTestCertificate(job, lineage.domains)) return "Replacing…"
  if (!certbotRunning(job) || job?.target !== lineage.name) return undefined
  if (job.kind === "certbot.revoke") return "Revoking…"
  if (job.kind === "certbot.renew") {
    return job.title.startsWith("Dry run") ? "Testing renewal…" : "Renewing…"
  }
  return undefined
}

/**
 * The authority a configured ACME directory belongs to, by its host: what the
 * issue form names in place of Let's Encrypt. The backend reads the setting
 * as given, so a value that is not a URL is shown as it is.
 */
export function authorityName(directory: string): string {
  try {
    return new URL(directory).host || directory
  } catch {
    return directory
  }
}

/** The names typed into the issue form: spaces, commas or new lines between them. */
export function parseDomains(text: string): string[] {
  return text.split(/[\s,]+/).filter(Boolean)
}
