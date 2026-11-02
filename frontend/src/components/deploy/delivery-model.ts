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
