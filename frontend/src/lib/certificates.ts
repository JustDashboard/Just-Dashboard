import type {
  CertbotCert,
  CertbotState,
  HookFailure,
  Job,
  RenewalFailure,
  RenewalHealth,
  ServedCertificate,
} from "@/lib/types"
import type { Tone } from "@/components/tone"

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
  if (!certbotRunning(job)) return undefined
  // One delete job takes every lineage the clean-up checked, in turn.
  if (job?.kind === "certbot.delete" && job.target?.split(", ").includes(lineage.name)) {
    return "Deleting…"
  }
  if (job?.target !== lineage.name) return undefined
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

const WEEKDAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"]
const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"]

/** The day a time falls on, as a count of local days. */
function localDay(d: Date): number {
  return Math.round(new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime() / DAY)
}

/** A time as the renewal readings place it: its clock, and how many days away. */
function placed(iso: string, now: number) {
  const at = new Date(iso)
  return {
    at,
    clock: `${String(at.getHours()).padStart(2, "0")}:${String(at.getMinutes()).padStart(2, "0")}`,
    days: localDay(at) - localDay(new Date(now)),
  }
}

/**
 * When a renewal run was, or will be, in the reader's time: "21:13" today,
 * "yesterday 21:13", "tomorrow 09:12", "Mon 21:13" inside the week either
 * way, "2 Jul 21:13" beyond it. The timer fires twice a day, so the hour is
 * the reading and the day only a qualifier.
 */
export function runTime(iso: string, now: number = Date.now()): string {
  const { at, clock, days } = placed(iso, now)
  if (days === 0) return clock
  if (days === -1) return `yesterday ${clock}`
  if (days === 1) return `tomorrow ${clock}`
  if (Math.abs(days) < 7) return `${WEEKDAYS[at.getDay()]} ${clock}`
  return `${at.getDate()} ${MONTHS[at.getMonth()]} ${clock}`
}

/** runTime inside a sentence: "at 21:13", "yesterday at 21:13", "on 2 Jul at 07:20". */
export function runPhrase(iso: string, now: number = Date.now()): string {
  const { at, clock, days } = placed(iso, now)
  if (days === 0) return `at ${clock}`
  if (days === -1) return `yesterday at ${clock}`
  if (days === 1) return `tomorrow at ${clock}`
  if (Math.abs(days) < 7) return `on ${WEEKDAYS[at.getDay()]} at ${clock}`
  return `on ${at.getDate()} ${MONTHS[at.getMonth()]} at ${clock}`
}

/** Where a streak began: the hour today, otherwise the day. */
export function sinceDay(iso: string, now: number = Date.now()): string {
  const { at, clock, days } = placed(iso, now)
  if (days === 0) return clock
  if (days === -1) return "yesterday"
  if (Math.abs(days) < 7) return WEEKDAYS[at.getDay()]
  return `${at.getDate()} ${MONTHS[at.getMonth()]}`
}

/** The failures of the last run that still stand: not renewed since. */
export function standingFailures(health: RenewalHealth | undefined): RenewalFailure[] {
  return (health?.failures ?? []).filter((f) => !f.renewedSince)
}

/** A stat tile's reading. */
export type Reading = { value: string; hint?: string; tone: Tone }

/**
 * The Renewal tile: not whether a timer is active — the host this was built
 * on had an active timer and a service that had failed every run for months
 * — but what the last run did, and when the next one is.
 */
export function renewalReading(
  state: CertbotState | undefined,
  certbotGone: boolean,
  now: number = Date.now(),
): Reading {
  if (certbotGone)
    return { value: "No certbot", hint: "install it to issue and renew", tone: "default" }
  if (!state) return { value: "—", tone: "default" }
  if (!state.autoRenew) {
    return {
      value: "Off",
      hint: state.renewUnit ? `${state.renewUnit} is off` : "no timer or cron entry found",
      tone: state.certs.length > 0 ? "danger" : "default",
    }
  }
  const health = state.health
  const scheduled: Reading = {
    value: "Scheduled",
    hint: `via ${state.renewSource}`,
    tone: "default",
  }
  if (!health) return scheduled
  const last = health.lastRun ? runTime(health.lastRun, now) : undefined
  switch (health.state) {
    case "failed": {
      const standing = standingFailures(health).length
      return {
        value: "Failing",
        hint: last
          ? standing > 0
            ? `last run ${last} · ${standing} failed`
            : `last run ${last} failed`
          : "the last run failed",
        tone: "danger",
      }
    }
    case "recovered":
      return {
        value: "Recovered",
        hint: last ? `renewed since the ${last} failure` : "renewed since the last failure",
        tone: "default",
      }
    case "ok":
      if (health.hookFailures?.length) {
        return {
          value: "Hook failed",
          hint: last ? `last run ${last}` : "on the last run",
          tone: "warning",
        }
      }
      return {
        value: "Healthy",
        hint: health.nextRun
          ? `next run ${runTime(health.nextRun, now)}`
          : last
            ? `last run ${last}`
            : undefined,
        tone: "success",
      }
    case "running":
      return {
        value: "Running",
        hint: `${health.service ?? "certbot"} is renewing now`,
        tone: "default",
      }
    case "never":
      return {
        value: "Scheduled",
        hint: health.nextRun ? `first run ${runTime(health.nextRun, now)}` : scheduled.hint,
        tone: "default",
      }
    default:
      return scheduled
  }
}

/**
 * How certbot proves control when it renews a lineage, as a word and the
 * thing it names: "webroot" and its folders, "DNS" and the provider.
 */
export function renewalMethod(
  cert: Pick<CertbotCert, "authenticator" | "webroots" | "dnsProvider">,
): { method: string; detail?: string; folders?: string[] } | undefined {
  const auth = cert.authenticator
  if (!auth) return undefined
  if (auth === "webroot") {
    return cert.webroots?.length
      ? { method: "webroot", folders: cert.webroots }
      : { method: "webroot" }
  }
  if (auth.startsWith("dns-")) {
    return { method: "DNS", detail: cert.dnsProvider ?? auth.slice(4) }
  }
  return { method: auth }
}

/**
 * Whether nginx reads a lineage's renewed certificate without anybody
 * reloading it by hand: certbot's nginx plugin reloads it for the lineages it
 * installed, and the dashboard's hook for every lineage. A deploy or post
 * hook of the lineage's own, a hook in certbot's hooks directories, or one
 * cli.ini sets may — what they do is not read, so nothing is claimed for
 * them.
 */
export function renewalReload(
  cert: Pick<CertbotCert, "installer" | "deployHook" | "postHook">,
  state: Pick<CertbotState, "nginxReloads" | "reloadHook">,
): "certbot" | "hook" | "unknown" | "none" {
  if (cert.installer === "nginx" && state.nginxReloads) return "certbot"
  if (state.reloadHook?.state === "installed") return "hook"
  if (
    cert.deployHook ||
    cert.postHook ||
    state.reloadHook?.state === "modified" ||
    state.reloadHook?.others.length
  ) {
    return "unknown"
  }
  return "none"
}

/**
 * The lineages an enabled site serves whose renewal nothing reloads nginx
 * for, and those sites: each renewal leaves them on the old certificate until
 * it expires. A disabled site serves nothing.
 */
export function unreloadedRenewals(state: CertbotState): { lineages: string[]; sites: string[] } {
  const lineages: string[] = []
  const sites = new Set<string>()
  if (!state.autoRenew) return { lineages, sites: [] }
  for (const lineage of state.certs) {
    if (lineage.error || renewalReload(lineage, state) !== "none") continue
    const served = lineage.servedBy ?? []
    if (served.length === 0) continue
    lineages.push(lineage.name)
    for (const site of served) sites.add(site)
  }
  return { lineages, sites: [...sites] }
}

/**
 * A hook that failed, in a line: certbot's name for it and the file it ran,
 * what it exited with, and what it said.
 */
export function hookFailureText(failure: HookFailure): string {
  const command = failure.command?.startsWith("/")
    ? failure.command.split(/\s/)[0].split("/").pop()
    : failure.command
  const hook = command ? `${failure.kind} ${command}` : failure.kind
  return `${hook} exited ${failure.code}${failure.output ? `: ${failure.output}` : "."}`
}
