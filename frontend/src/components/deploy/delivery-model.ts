import type { DeploymentInsights } from "@/lib/types"

/**
 * The delivery figures as the overview says them, in the sentence a reader
 * would use: the server computes the figures, this is the wording. Pure, so
 * `bun test` holds it. The formatter is the runs' own, handed in, so a median
 * here never reads differently from the runs listed under it.
 */
export function deliveryLead(data: DeploymentInsights, format: (seconds: number) => string) {
  const days = `the last ${data.windowDays} ${data.windowDays === 1 ? "day" : "days"}`
  const decided = data.succeeded + data.failed
  const title =
    decided === 0
      ? `No release finished in ${days}`
      : data.failed > 0
        ? `${data.succeeded} of ${decided} releases succeeded in ${days}`
        : data.succeeded === 1
          ? `1 release in ${days}, and it succeeded`
          : `${data.succeeded} releases in ${days}, all successful`
  const parts: string[] = []
  if (data.succeeded > 0) parts.push(`About ${data.deploysPerWeek.toFixed(1)} a week`)
  if (data.medianDurationSeconds > 0)
    parts.push(
      `usually ${format(data.medianDurationSeconds)} each, 95% within ${format(data.p95DurationSeconds)}`,
    )
  if (data.recoveredFailures > 0)
    parts.push(`back up in ${format(data.meanRecoverySeconds)} on average after a failure`)
  else if (data.failed > 0) parts.push("no failure followed by a success yet")
  return { title, hint: parts.join(" · ") }
}

/** One fact of the line under the history's lead: its words, and what they were worked out from. */
export type DeliveryFact = {
  key: "rate" | "frequency" | "duration" | "recovery"
  /** The figure, drawn in the page's ink. */
  figure: string
  /** The words after it. */
  words: string
  /** The figure's basis, for a `title`. */
  basis: string
  tone?: "warning"
}

/**
 * The same figures as a line of facts, for the Deployments page: the success
 * rate, how often, how long and how fast it recovers, each with the basis the
 * four captioned figures used to print under themselves. A fact with nothing
 * behind it is left out rather than drawn as a dash: a window nothing
 * succeeded in has no release time, and one with no failure has no recovery.
 * `within` words the slow tail, so the page says it as it says every other.
 */
export function deliveryFacts(
  data: DeploymentInsights,
  format: (seconds: number) => string,
  within: (figure: string) => string,
): DeliveryFact[] {
  const decided = data.succeeded + data.failed
  const facts: DeliveryFact[] = []
  if (decided > 0)
    facts.push({
      key: "rate",
      figure: `${Math.round(data.successRate * 100)}%`,
      words: "succeeded",
      basis: `${data.succeeded} of ${decided} decided ${decided === 1 ? "release" : "releases"}`,
      tone: data.failureStreak > 0 ? "warning" : undefined,
    })
  if (data.succeeded > 0)
    facts.push({
      key: "frequency",
      figure: data.deploysPerWeek.toFixed(1),
      words: "a week",
      basis: `${data.succeeded} successful over ${data.windowDays} ${data.windowDays === 1 ? "day" : "days"}`,
    })
  if (data.medianDurationSeconds > 0)
    facts.push({
      key: "duration",
      figure: format(data.medianDurationSeconds),
      words: `usually, ${within(format(data.p95DurationSeconds))}`,
      basis: "The median release, from claim to finish",
    })
  if (data.recoveredFailures > 0)
    facts.push({
      key: "recovery",
      figure: format(data.meanRecoverySeconds),
      words: "to recover",
      basis: `Mean over ${data.recoveredFailures} recovered ${data.recoveredFailures === 1 ? "failure" : "failures"}`,
    })
  else if (data.failed > 0)
    facts.push({
      key: "recovery",
      figure: "",
      words: "no failure followed by a success yet",
      basis: "A failure is recovered when a later release succeeds",
    })
  return facts
}
