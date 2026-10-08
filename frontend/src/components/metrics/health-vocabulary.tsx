import {
  Archive,
  Box,
  Cpu,
  Gauge,
  GridSquare,
  Lightning,
  NetworkDevice,
  Servers,
} from "@/components/icons"
import type { Health, HealthArea, HealthFinding } from "@/lib/types"
import type { ReleaseNodeState } from "@/components/deploy/vocabulary"

/**
 * The words, marks and grounds every Health surface shares, so the strip on the
 * Overview, the list under it and the sheet a finding opens cannot disagree
 * about what "critical" looks like.
 */

/** `modules` is the Overview's own: what the other pages found, folded in. */
export type HealthStripArea = HealthArea | "modules"

export const AREA: Record<
  HealthStripArea,
  { label: string; icon: React.ComponentType<{ className?: string }> }
> = {
  cpu: { label: "CPU", icon: Cpu },
  memory: { label: "Memory", icon: Gauge },
  storage: { label: "Storage", icon: Archive },
  network: { label: "Network", icon: NetworkDevice },
  services: { label: "Services", icon: Servers },
  containers: { label: "Containers", icon: Box },
  hardware: { label: "Hardware", icon: Lightning },
  modules: { label: "Modules", icon: GridSquare },
}

export const AREA_ORDER: HealthArea[] = [
  "cpu",
  "memory",
  "storage",
  "network",
  "services",
  "containers",
  "hardware",
]

/** Where a finding belongs when the server did not say — an older backend's. */
export function findingArea(finding: Pick<HealthFinding, "id" | "area">): HealthArea {
  if (finding.area) return finding.area
  const id = finding.id
  if (id.startsWith("disk:") || id.startsWith("inodes:")) return "storage"
  if (["memory", "swap", "psi-mem"].includes(id)) return "memory"
  if (["load", "steal", "psi-cpu"].includes(id)) return "cpu"
  if (["iowait", "psi-io"].includes(id)) return "storage"
  if (id === "timewait" || id.startsWith("drops:") || id.startsWith("neterr:")) return "network"
  if (id === "systemd.failed") return "services"
  if (id.startsWith("docker.")) return "containers"
  return "hardware"
}

export const LEVEL_WORD: Record<Health["status"], string> = {
  ok: "Healthy",
  notice: "Notice",
  warning: "Warning",
  critical: "Critical",
}

/** A tinted ground and its edge, by level — the banner vocabulary of §3. */
export const LEVEL_WASH: Record<HealthFinding["level"], string> = {
  critical: "border-rule-danger bg-wash-danger",
  warning: "border-rule-warning bg-wash-warning",
  notice: "border-hairline",
}

/**
 * The ground alone, for a card whose edge is already the lit one (§16): a
 * second border inside it would draw two.
 */
export const LEVEL_GROUND: Record<HealthFinding["level"], string> = {
  critical: "bg-wash-danger",
  warning: "bg-wash-warning",
  notice: "",
}

/** The severity as a short bar of its hue, at the leading edge of a card. */
export const LEVEL_BAR: Record<HealthFinding["level"], string> = {
  critical: "bg-destructive",
  warning: "bg-warning",
  notice: "bg-muted-foreground/50",
}

export const LEVEL_TEXT: Record<HealthFinding["level"], string> = {
  critical: "text-destructive",
  warning: "text-warning",
  notice: "text-muted-foreground",
}

/** The release path's own segment states, so a verdict strip reads like a run's. */
export function segmentState(
  status: Health["status"] | "unknown" | undefined,
  checking: boolean,
): ReleaseNodeState {
  if (checking) return "running"
  switch (status) {
    case "critical":
      return "failed"
    case "warning":
      return "warning"
    case "ok":
    case "notice":
      return "passed"
    default:
      return "absent"
  }
}

/**
 * The figure a finding is judged on, drawn against the line it crossed — for
 * the checks where higher is worse and the scale is a percentage. Memory is
 * judged on what is left, so it is turned round to read as what is taken.
 */
export function findingGauge(
  finding: Pick<HealthFinding, "id" | "value" | "threshold">,
): { value: number; mark: number; label: string } | null {
  const { id, value, threshold } = finding
  if (id === "memory")
    return { value: 100 - value, mark: 100 - threshold, label: `${Math.round(value)}% left` }
  if (
    id.startsWith("disk:") ||
    id.startsWith("inodes:") ||
    id.startsWith("psi-") ||
    id === "files" ||
    id === "swap"
  )
    return { value, mark: threshold, label: `${Math.round(value)}%` }
  return null
}

/** How long a condition has held, from the server's `since`, in a few characters. */
export function heldFor(since: string | undefined, now: number): string | null {
  if (!since) return null
  const seconds = Math.max(0, (now - Date.parse(since)) / 1000)
  if (!Number.isFinite(seconds)) return null
  if (seconds < 90) return "just now"
  if (seconds < 3600) return `${Math.round(seconds / 60)} min`
  if (seconds < 86_400) return `${Math.round(seconds / 3600)} h`
  return `${Math.round(seconds / 86_400)} d`
}
