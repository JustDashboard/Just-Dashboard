import type { ProxyFindingSnooze } from "@/lib/types"
import type { ProxyFinding } from "@/components/proxy/findings/shared"
import { unreadableSource } from "@/components/proxy/findings/engine"

/** How long a snooze lasts, by the name PUT /proxy/findings/snoozes takes. */
export type SnoozeSpan = "day" | "week" | "change"

export const SNOOZE_LABEL: Record<SnoozeSpan, string> = {
  day: "1 day",
  week: "7 days",
  change: "Until it changes",
}

/**
 * The finding as a snooze remembers it. The level is always part of it, so a
 * warning put aside comes back the moment it turns critical; the rest is the
 * area's own fingerprint where it gave one, and otherwise what the row says.
 */
export function findingFingerprint(finding: ProxyFinding): string {
  return [finding.level, finding.fingerprint ?? `${finding.title}\n${finding.detail}`].join("\n")
}

/**
 * The spans a finding may be put aside for. A critical one comes back within
 * a day whatever happens, which is the server's rule too; a source the page
 * could not read is not a finding about the host and is never put aside.
 */
export function snoozeSpans(finding: ProxyFinding): SnoozeSpan[] {
  if (unreadableSource(finding)) return []
  return finding.level === "critical" ? ["day"] : ["day", "week", "change"]
}

export type SnoozedFinding = { finding: ProxyFinding; snooze: ProxyFindingSnooze }

/**
 * The findings to show and the ones a snooze still holds. A snooze holds
 * while its time has not run out and the finding reads as it did when it
 * was put aside; a finding that changed is shown again whatever the span.
 */
export function splitSnoozed(
  findings: ProxyFinding[],
  snoozes: ProxyFindingSnooze[],
  now: number,
): { shown: ProxyFinding[]; snoozed: SnoozedFinding[] } {
  const byId = new Map(snoozes.map((s) => [s.findingId, s]))
  const shown: ProxyFinding[] = []
  const snoozed: SnoozedFinding[] = []
  for (const finding of findings) {
    const snooze = byId.get(finding.id)
    const holds =
      snooze &&
      snoozeSpans(finding).length > 0 &&
      snooze.fingerprint === findingFingerprint(finding) &&
      (!snooze.until || new Date(snooze.until).getTime() > now)
    if (holds) snoozed.push({ finding, snooze })
    else shown.push(finding)
  }
  return { shown, snoozed }
}
