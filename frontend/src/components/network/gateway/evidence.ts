import type { EntryFlow, EntryReadiness, GatewayView } from "@/lib/types"
import type { DotTone } from "@/components/status-dot"
import { readinessWord } from "@/components/network/gateway/reading"

/**
 * What the gateway's policy evidence comes to, entry by entry and for the
 * page: the block of prose that opened the Gateway page said these things in
 * sentences, and each is a dot and a word now, on the row or the line it is
 * about, with the sentences behind it.
 */

export type StateWord = { label: string; tone: DotTone }

/** The modelled flow of one entry, by the key the server files it under. */
export function flowOf(flows: EntryFlow[] | undefined, kind: "forward" | "nat", id: number) {
  return flows?.find((flow) => flow.entry === `${kind}:${id}`)
}

/**
 * One entry's state as the word its row carries: the worst thing known about
 * it. What is installed is read first (rules missing, forwarding off,
 * admission missing), then what the host's other chains would do to its
 * traffic as modelled, then what was measured from outside. An entry
 * switched off says only that.
 */
export function entryState(
  enabled: boolean,
  readiness: EntryReadiness | undefined,
  flow: Pick<EntryFlow, "verdict"> | undefined,
): StateWord {
  if (!enabled) return { label: "Switched off", tone: "stopped" }
  const installed = readinessWord(readiness)
  if (installed.tone === "warning") return { label: installed.label, tone: "warning" }
  if (installed.tone === "danger") return { label: installed.label, tone: "danger" }
  if (flow?.verdict === "blocked") return { label: "Dropped by a layer", tone: "danger" }
  if (flow?.verdict === "unknown") return { label: "May be dropped", tone: "warning" }
  if (installed.tone === "success") return { label: installed.label, tone: "running" }
  if (flow?.verdict === "clear") return { label: "Passes every layer", tone: "running" }
  if (!readiness) return { label: "Unknown", tone: "unknown" }
  return { label: installed.label, tone: "running" }
}

/** Whether the firewall lets a translated connection through, as the identity line says it. */
export function admissionState(admission: GatewayView["admission"]): StateWord {
  if (!admission.needed) return { label: "Nothing to admit", tone: "stopped" }
  if (admission.present) return { label: "Admission present", tone: "running" }
  const unreadable = admission.chains?.some(
    (chain) => chain.needed && chain.status === "unreadable",
  )
  return { label: unreadable ? "Admission unreadable" : "Admission missing", tone: "warning" }
}

/** Whether an owned admission rule is absent where one is needed: what Repair puts back. */
export function admissionRepairable(admission: GatewayView["admission"]) {
  return Boolean(admission.chains?.some((chain) => chain.needed && chain.status === "absent"))
}

/** The gateway as a whole: nothing made, saved but not in the kernel, or in force. */
export function gatewayState(view: Pick<GatewayView, "loaded" | "forwards" | "nat">): StateWord {
  const entries = [...view.forwards, ...view.nat]
  if (entries.length === 0) return { label: "Nothing passed on", tone: "stopped" }
  if (!entries.some((entry) => entry.enabled)) return { label: "All switched off", tone: "stopped" }
  // Amber is for entries that are saved and not in the kernel, never for one
  // the reader switched off on purpose.
  if (!view.loaded) return { label: "Not loaded", tone: "warning" }
  return { label: "In force", tone: "running" }
}
