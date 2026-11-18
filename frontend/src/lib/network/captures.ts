import { parseAddress } from "@/lib/cidr"
import type { DotTone } from "@/components/status-dot"

export type CaptureRequest = {
  interface: string
  family: "inet" | "inet6"
  protocol: "all" | "tcp" | "udp" | "icmp" | "icmp6"
  source?: string
  destination?: string
  port?: number
  packets: number
  seconds: number
  maxBytes: number
  snapshotLength: number
  incidentRunId?: string
}
export type CaptureRun = {
  id: string
  name: string
  request: CaptureRequest
  status: "queued" | "running" | "cancelling" | "completed" | "failed" | "cancelled" | "interrupted"
  createdAt: string
  startedAt?: string
  endedAt?: string
  createdBy: string
  jobId?: string
  error?: string
  limitations: string[]
  result?: {
    checkedAt: string
    packets: number
    bytes: number
    linkType: number
    sha256?: string
    stopReason: string
    partialPacket: boolean
    kernelDropped?: number
    nativeSummary?: string
    cleanup: { termSent: boolean; killSent: boolean; exitCode: number }
    interfaceIndex: number
    identityVerified: boolean
    artifactAvailable: boolean
  }
}
export type CaptureList = {
  runs: CaptureRun[]
  maxRetained: number
  retentionHours: number
  maxArtifactBytes: number
  maxRunning: number
}
export type CaptureInterface = { name: string; index: number; up: boolean }
export type CaptureDraft = Omit<
  CaptureRequest,
  "port" | "packets" | "seconds" | "maxBytes" | "snapshotLength"
> & {
  name: string
  port: string
  packets: string
  seconds: string
  maxBytes: string
  snapshotLength: string
}
export const newCaptureDraft = (): CaptureDraft => ({
  name: "",
  interface: "",
  family: "inet",
  protocol: "tcp",
  source: "",
  destination: "",
  port: "",
  packets: "1000",
  seconds: "30",
  maxBytes: "1048576",
  snapshotLength: "128",
  incidentRunId: "",
})
const integer = (value: string, min: number, max: number) =>
  /^\d+$/.test(value) && Number(value) >= min && Number(value) <= max

export function captureDraftProblem(draft: CaptureDraft) {
  if (
    !draft.name.trim() ||
    new TextEncoder().encode(draft.name.trim()).length > 100 ||
    /\p{Cc}/u.test(draft.name)
  )
    return "Use a name of 1 to 100 UTF-8 bytes without control characters."
  if (!/^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,14}$/.test(draft.interface))
    return "Select one native interface."
  if (!integer(draft.packets, 1, 10000) || !integer(draft.seconds, 1, 120))
    return "Capture 1 to 10000 packets for 1 to 120 seconds."
  if (!["96", "128", "256", "512"].includes(draft.snapshotLength))
    return "Select a supported snapshot length."
  if (!integer(draft.maxBytes, 40 + Number(draft.snapshotLength), 2097152))
    return "Set a byte limit that fits one complete packet, up to 2 MiB."
  if (draft.port && (!integer(draft.port, 1, 65535) || !["tcp", "udp"].includes(draft.protocol)))
    return "A port of 1 to 65535 requires TCP or UDP."
  if (
    (draft.protocol === "icmp" && draft.family !== "inet") ||
    (draft.protocol === "icmp6" && draft.family !== "inet6")
  )
    return "The ICMP protocol must match the address family."
  if (draft.incidentRunId && !/^[a-f0-9]{32}$/.test(draft.incidentRunId))
    return "Select an existing saved diagnostic."
  for (const value of [draft.source, draft.destination]) {
    if (!value?.trim()) continue
    const address = parseAddress(value.trim())
    const mapped =
      address?.length === 16 &&
      address.slice(0, 10).every((byte) => byte === 0) &&
      address[10] === 255 &&
      address[11] === 255
    if (!address || mapped || address.length !== (draft.family === "inet6" ? 16 : 4))
      return "Use literal filter addresses in the selected family without names, zones or mapped addresses."
  }
}
export function captureRequest(draft: CaptureDraft): CaptureRequest | undefined {
  if (captureDraftProblem(draft)) return
  return {
    interface: draft.interface,
    family: draft.family,
    protocol: draft.protocol,
    source: draft.source?.trim() || undefined,
    destination: draft.destination?.trim() || undefined,
    port: draft.port ? Number(draft.port) : undefined,
    packets: Number(draft.packets),
    seconds: Number(draft.seconds),
    maxBytes: Number(draft.maxBytes),
    snapshotLength: Number(draft.snapshotLength),
    incidentRunId: draft.incidentRunId || undefined,
  }
}
export const captureFinished = (run: Pick<CaptureRun, "status">) =>
  ["completed", "failed", "cancelled", "interrupted"].includes(run.status)
export function captureReading(run: Pick<CaptureRun, "status">): { label: string; tone: DotTone } {
  switch (run.status) {
    case "running":
      return { label: "Capturing", tone: "running" }
    case "queued":
      return { label: "Queued", tone: "warning" }
    case "cancelling":
      return { label: "Stopping", tone: "warning" }
    case "completed":
      return { label: "Capture completed", tone: "notice" }
    case "failed":
      return { label: "Capture failed", tone: "warning" }
    case "interrupted":
      return { label: "Interrupted", tone: "warning" }
    case "cancelled":
      return { label: "Cancelled", tone: "stopped" }
  }
}
