import type { SQMProfile } from "@/lib/types"

export type SQMDraft = Omit<SQMProfile, "overhead" | "mpu" | "rttMillis"> & {
  overhead: string
  mpu: string
  rttMillis: string
}

export function sqmDraft(profile?: SQMProfile): SQMDraft {
  return {
    diffserv: profile?.diffserv ?? "besteffort",
    flowMode: profile?.flowMode ?? "dual-dsthost",
    nat: profile?.nat ?? false,
    preserveDscp: profile?.preserveDscp ?? false,
    linkLayer: profile?.linkLayer ?? "noatm",
    overhead: String(profile?.overhead ?? 0),
    mpu: String(profile?.mpu ?? 0),
    rttMillis: String(profile?.rttMillis ?? 100),
  }
}

export function sqmProblem(draft: SQMDraft, downloadKbit: number): string | undefined {
  if (!Number.isFinite(downloadKbit) || downloadKbit <= 0)
    return "Download SQM needs a positive download limit."
  if (downloadKbit > 100_000_000) return "The download limit cannot exceed 100 Gbit/s."
  for (const [key, min, max, label] of [
    ["overhead", -64, 256, "Overhead"],
    ["mpu", 0, 256, "Minimum packet size"],
    ["rttMillis", 10, 1000, "RTT"],
  ] as const) {
    const text = draft[key].trim()
    const value = Number(text)
    if (!/^-?\d+$/.test(text) || !Number.isSafeInteger(value) || value < min || value > max)
      return `${label} must be a whole number from ${min} to ${max}.`
  }
  if (
    !["besteffort", "diffserv3", "diffserv4"].includes(draft.diffserv) ||
    !["dual-dsthost", "triple-isolate", "flows"].includes(draft.flowMode) ||
    !["noatm", "atm", "ptm"].includes(draft.linkLayer)
  )
    return "Choose a supported CAKE profile."
  return undefined
}

/** Build only the operator profile; saved server identities never become request fields. */
export function sqmProfile(draft: SQMDraft): SQMProfile {
  return {
    diffserv: draft.diffserv,
    flowMode: draft.flowMode,
    nat: draft.nat,
    preserveDscp: draft.preserveDscp,
    linkLayer: draft.linkLayer,
    overhead: Number(draft.overhead),
    mpu: Number(draft.mpu),
    rttMillis: Number(draft.rttMillis),
  }
}
