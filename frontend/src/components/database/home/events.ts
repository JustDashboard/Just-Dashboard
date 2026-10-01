import type { MetricEvent } from "@/lib/types"
import type { Sample } from "@/components/database/home/samples"

/**
 * What happened to the server while the chart was being drawn, as the marks
 * the chart sets on its time axis: a dump taken, the server restarted. A
 * step in a line is read next to what caused it.
 *
 * Each is set on the first reading taken after it. The chart is a run of
 * readings five seconds apart, and it is at a reading that a pointer asks
 * what happened; an event between two of them belongs to the one that first
 * shows its effect. One from before the first reading is left out — the
 * chart has nothing to set it against.
 */
export function chartEvents(
  samples: readonly Sample[],
  /** The gauge that holds the server's uptime in seconds, in this family's samples. */
  uptime: string,
  dumps: readonly { takenAt: string; summary?: string }[] = [],
): MetricEvent[] {
  if (samples.length < 2) return []
  const first = samples[0].at
  const events: MetricEvent[] = []
  const at = (instant: number) => samples.find((sample) => sample.at >= instant)?.at

  for (let index = 1; index < samples.length; index++) {
    const before = samples[index - 1].gauges[uptime]
    const after = samples[index].gauges[uptime]
    // An uptime that went down is a server that came up again in between.
    if (typeof before !== "number" || typeof after !== "number" || after >= before) continue
    events.push({
      ts: new Date(samples[index].at).toISOString(),
      kind: "reboot",
      title: "Server restarted",
      severity: "warning",
    })
  }
  for (const dump of dumps) {
    const taken = Date.parse(dump.takenAt)
    if (Number.isNaN(taken) || taken <= first) continue
    const shown = at(taken)
    if (shown === undefined) continue
    events.push({
      ts: new Date(shown).toISOString(),
      kind: "backup",
      title: "Backup taken",
      detail: dump.summary,
      severity: "info",
    })
  }
  return events
}
