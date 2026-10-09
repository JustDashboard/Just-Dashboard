import type { CrowdSecEnforcementState, CrowdSecView } from "@/lib/types"
import type { Verdict } from "@/components/status-dot"

/**
 * What each enforcement verdict lets the page say. Only `enforcing` claims
 * protection; `partial` claims it for the HTTP its proxies carry and nothing
 * else, and every other state is a reason the decisions are not, or cannot
 * be shown to be, dropping anything.
 */
export const ENFORCEMENT: Record<
  CrowdSecEnforcementState,
  { label: string; verdict: Verdict; title: string; protects: boolean }
> = {
  enforcing: {
    label: "enforcing",
    verdict: "ok",
    title: "Decisions are being dropped",
    protects: true,
  },
  partial: {
    label: "HTTP only",
    verdict: "notice",
    title: "Decisions are enforced only at the proxy",
    protects: true,
  },
  unverified: {
    label: "unverified",
    verdict: "notice",
    title: "Enforcement could not be verified",
    protects: false,
  },
  degraded: {
    label: "not dropping",
    verdict: "critical",
    title: "The bouncer is pulling and nothing is dropped",
    protects: false,
  },
  stale: {
    label: "not enforcing",
    verdict: "critical",
    title: "No bouncer is pulling decisions",
    protects: false,
  },
  unenforced: {
    label: "not enforcing",
    verdict: "critical",
    title: "No bouncer enforces the decisions",
    protects: false,
  },
  stopped: {
    label: "not running",
    verdict: "warning",
    title: "CrowdSec is installed and not running",
    protects: false,
  },
}

/**
 * The verdict a view carries. A server that predates the verdict sends none,
 * and its silence is not evidence of protection.
 */
export function enforcementOf(view: CrowdSecView): CrowdSecEnforcementState {
  if (!view.active) return "stopped"
  return view.enforcement?.state ?? "unverified"
}

/** "8 s", "4 min", "3 h", "2 days" — how long ago a bouncer pulled. */
export function pullAge(seconds: number): string {
  if (seconds < 0) return "never"
  if (seconds < 60) return `${seconds} s`
  if (seconds < 3600) return `${Math.floor(seconds / 60)} min`
  if (seconds < 48 * 3600) return `${Math.floor(seconds / 3600)} h`
  return `${Math.floor(seconds / 86400)} days`
}

/** "3m0s" as a reader says it: "3 min". */
export function goDuration(spelling: string): string {
  const match = /^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?$/.exec(spelling)
  if (!match) return spelling
  const [, h, m, s] = match.map((part) => Number(part ?? 0))
  return pullAge(h * 3600 + m * 60 + s)
}
