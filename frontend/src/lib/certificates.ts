import type { Job } from "@/lib/types"

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
 * The present participle a lineage shows while the running job acts on it:
 * a renewal or dry run naming it, or its revocation.
 */
export function lineageActivity(job: Job | null | undefined, name: string): string | undefined {
  if (!certbotRunning(job) || job?.target !== name) return undefined
  if (job.kind === "certbot.revoke") return "Revoking…"
  if (job.kind === "certbot.renew") {
    return job.title.startsWith("Dry run") ? "Testing renewal…" : "Renewing…"
  }
  return undefined
}

/** The names typed into the issue form: spaces, commas or new lines between them. */
export function parseDomains(text: string): string[] {
  return text.split(/[\s,]+/).filter(Boolean)
}
