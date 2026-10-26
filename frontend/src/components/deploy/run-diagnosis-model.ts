import type { AssistantSettings } from "@/components/database/query/assistant/use-assistant"
import type { DeploymentRunDiagnosis } from "@/lib/types"

/**
 * Where a question to the assistant goes, as the person asking would name
 * it: Anthropic, or the host of the OpenAI-compatible server — a model on
 * this machine reads as 127.0.0.1:11434, which is the point.
 */
export function diagnosisDestination(settings: Pick<AssistantSettings, "provider" | "baseUrl">) {
  if (settings.provider === "anthropic") return "Anthropic"
  try {
    return settings.baseUrl ? new URL(settings.baseUrl).host : "the model's server"
  } catch {
    return "the model's server"
  }
}

/** The line under the button: what is sent, where, and what is not. */
export function diagnosisSends(settings: Pick<AssistantSettings, "provider" | "baseUrl">) {
  return `Sends the failed step, the cause above, the settings this run used, its variables' names and the end of its transcript to ${diagnosisDestination(settings)}, with secret values masked. Nothing is changed.`
}

/** What the answer's footer says it was written from. */
export function diagnosisProvenance(
  diagnosis: Pick<DeploymentRunDiagnosis, "sentLines" | "masked">,
) {
  const lines = `${diagnosis.sentLines} ${diagnosis.sentLines === 1 ? "line" : "lines"} sent`
  if (diagnosis.masked === 0) return lines
  return `${lines} · ${diagnosis.masked} ${diagnosis.masked === 1 ? "value" : "values"} masked`
}

/** The product glyph a provider is drawn with. */
export function diagnosisGlyph(provider: string | undefined) {
  return provider === "anthropic" ? "claude" : "openai"
}
