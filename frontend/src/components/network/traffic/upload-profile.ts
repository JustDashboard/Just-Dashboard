import type { UploadProfile } from "@/lib/network-traffic"

export type UploadDraft = Omit<UploadProfile, "overhead" | "mpu" | "rttMillis"> & {
  overhead: string
  mpu: string
  rttMillis: string
}

/** A device's saved upload profile as the form's draft, or CAKE's egress defaults. */
export function uploadDraft(profile?: UploadProfile): UploadDraft {
  return {
    diffserv: profile?.diffserv ?? "besteffort",
    flowMode: profile?.flowMode ?? "dual-srchost",
    nat: profile?.nat ?? false,
    wash: profile?.wash ?? false,
    ackFilter: profile?.ackFilter ?? false,
    linkLayer: profile?.linkLayer ?? "noatm",
    overhead: String(profile?.overhead ?? 0),
    mpu: String(profile?.mpu ?? 0),
    rttMillis: String(profile?.rttMillis ?? 100),
  }
}

export function uploadProblem(draft: UploadDraft): string | undefined {
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
    !["dual-srchost", "triple-isolate", "flows"].includes(draft.flowMode) ||
    !["noatm", "atm", "ptm"].includes(draft.linkLayer)
  )
    return "Choose a supported CAKE upload profile."
  return undefined
}

export function uploadProfile(draft: UploadDraft): UploadProfile {
  return {
    diffserv: draft.diffserv,
    flowMode: draft.flowMode,
    nat: draft.nat,
    wash: draft.wash,
    ackFilter: draft.ackFilter,
    linkLayer: draft.linkLayer,
    overhead: Number(draft.overhead),
    mpu: Number(draft.mpu),
    rttMillis: Number(draft.rttMillis),
  }
}
