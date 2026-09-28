import type { CertbotCert, Job } from "@/lib/types"

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
 * certbot reloads nothing after certonly, so a site naming it still serves
 * the test certificate until nginx reloads.
 */
export function testCertificateReplaced(job: Job | null | undefined): string[] | undefined {
  return job?.kind === "certbot.issue" &&
    job.status === "succeeded" &&
    job.title.startsWith(REPLACEMENT)
    ? issuedNames(job)
    : undefined
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
