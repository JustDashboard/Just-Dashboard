import type { PathEvidence, PathRequest } from "./network-investigator-types"

export type PathDraft = {
  source: string
  target: string
  sourceAddress: string
  address: string
  family: PathRequest["family"]
  protocol: PathRequest["protocol"]
  port: string
  mark: string
  measure: boolean
}

export const newPathDraft = (): PathDraft => ({
  source: "host",
  target: "",
  sourceAddress: "",
  address: "",
  family: "inet",
  protocol: "tcp",
  port: "443",
  mark: "",
  measure: false,
})

export function pathRequest(draft: PathDraft): PathRequest | undefined {
  const target = draft.target.trim()
  if (
    !target ||
    target.startsWith("-") ||
    /[\s$`()]/.test(target) ||
    !/^\d+$/.test(draft.port) ||
    Number(draft.port) < 1 ||
    Number(draft.port) > 65535 ||
    (draft.source !== "host" && !/^[a-f0-9]{64}$/.test(draft.source)) ||
    (draft.mark.trim() && !/^(?:\d+|0x[a-fA-F0-9]+)$/.test(draft.mark.trim()))
  ) {
    return undefined
  }
  return {
    sourceKind: draft.source === "host" ? "host" : "container",
    ...(draft.source === "host" ? {} : { containerId: draft.source }),
    target,
    family: draft.family,
    protocol: draft.protocol,
    port: Number(draft.port),
    ...(draft.sourceAddress.trim() ? { sourceAddress: draft.sourceAddress.trim() } : {}),
    ...(draft.address.trim() ? { address: draft.address.trim() } : {}),
    ...(draft.mark.trim() ? { mark: draft.mark.trim() } : {}),
    measure: draft.measure && draft.protocol === "tcp" && !draft.mark.trim(),
  }
}

export function evidenceCounts(evidence: PathEvidence[]) {
  return evidence.reduce(
    (counts, item) => {
      counts[item.basis] += 1
      return counts
    },
    { observed: 0, modeled: 0, measured: 0, unknown: 0 },
  )
}
