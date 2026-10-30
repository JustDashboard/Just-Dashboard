import { relativeTime } from "@/lib/format"
import type { DotTone } from "@/components/status-dot"
import type {
  DeploymentDNSRecordChange,
  DeploymentDNSRecordManaged,
  DeploymentDNSRecordPlan,
  DeploymentDNSRecordSet,
} from "@/lib/types"

/**
 * The words of a domain's DNS record sheet, kept apart from the sheet so they
 * are unit-tested: where the record stands, what the press does, each set's
 * line, and who wrote what this dashboard manages.
 */

/** Where the name's record stands, as the sheet's status. */
export function recordState(plan: DeploymentDNSRecordPlan): { tone: DotTone; label: string } {
  if (plan.ready)
    return { tone: "running", label: plan.tunnel ? "Points at the tunnel" : "Points here" }
  if (plan.refusalCode === "conflict")
    return { tone: "warning", label: "Another record is in the way" }
  if (plan.refusalCode) return { tone: "warning", label: "Cannot be written from here" }
  const changing = plan.changes.filter(
    (change) => change.action === "create" || change.action === "update",
  )
  if (changing.some((change) => change.action === "update"))
    return { tone: "notice", label: plan.tunnel ? "Points elsewhere" : "Names another server" }
  return { tone: "notice", label: "Missing" }
}

/**
 * The press, in the dry run's own terms: it creates, corrects, or replaces a
 * record this dashboard did not write — which the write is told it may.
 */
export function recordButton(plan: DeploymentDNSRecordPlan): string {
  if (plan.foreign) return "Replace the record"
  if (plan.changes.some((change) => change.action === "update")) return "Correct the record"
  return "Create the record"
}

/** A TTL as the sheet reads it: Cloudflare's automatic, or the seconds. */
export function ttlLabel(ttl: number | undefined): string {
  if (!ttl) return ""
  if (ttl === 1) return "TTL auto"
  if (ttl % 3600 === 0) return `TTL ${ttl / 3600} h`
  if (ttl % 60 === 0) return `TTL ${ttl / 60} min`
  return `TTL ${ttl} s`
}

/** One set as a line: `A  app.example.com  →  203.0.113.7`. */
export function setLine(set: Pick<DeploymentDNSRecordSet, "type" | "name" | "values">): string {
  return `${set.type}  ${set.name}  →  ${set.values.join(", ")}`
}

/** What a change says beside its record. */
export function changeVerb(change: DeploymentDNSRecordChange): string {
  switch (change.action) {
    case "create":
      return "To create"
    case "update":
      return change.ours ? "To correct" : "To replace"
    case "keep":
      return change.ours ? "Managed here" : "Already right"
    case "conflict":
      return "In the way"
    case "stale":
      return "Left as it is"
  }
}

/** The tone of a change's word. */
export function changeTone(change: DeploymentDNSRecordChange): DotTone {
  switch (change.action) {
    case "keep":
      return "running"
    case "create":
    case "update":
      return "notice"
    default:
      return "warning"
  }
}

/** What the zone holds now, for a set that is changing. */
export function currentLine(change: DeploymentDNSRecordChange): string | null {
  if (change.action === "create") return "Nothing at the name yet"
  if (change.action === "update" || change.action === "stale" || change.action === "conflict")
    return change.current.length > 0 ? `Now ${change.current.join(", ")}` : null
  return null
}

/**
 * Who wrote the record this dashboard manages, and when: the sets it wrote
 * that the zone still holds, the newest change first.
 */
export function managedLine(managed: DeploymentDNSRecordManaged[]): string | null {
  const held = managed.filter((row) => row.current)
  if (held.length === 0) {
    if (managed.length === 0) return null
    return "Written here before, and changed since outside this dashboard"
  }
  const first = held.reduce((a, b) => (a.createdAt < b.createdAt ? a : b))
  const last = held.reduce((a, b) => (a.updatedAt > b.updatedAt ? a : b))
  let line = `Record managed here · created ${relativeTime(first.createdAt)}`
  if (first.createdBy) line += ` by ${first.createdBy}`
  if (last.updatedAt !== first.createdAt) {
    line += ` · changed ${relativeTime(last.updatedAt)}`
    if (last.updatedBy && last.updatedBy !== first.createdBy) line += ` by ${last.updatedBy}`
  }
  return line
}

/** Where the record is written, as one line: the zone, the provider and its account. */
export function whereLine(plan: DeploymentDNSRecordPlan): string | null {
  if (!plan.zone || !plan.provider) return null
  const account = plan.account && plan.account !== plan.zone ? ` (${plan.account})` : ""
  return `Zone ${plan.zone} at ${plan.provider}${account}, with the credentials saved under Certificates`
}

/** A refusal that saving credentials under Certificates would answer. */
export function opensCertificates(plan: DeploymentDNSRecordPlan): boolean {
  return (
    plan.refusalCode === "no_credentials" ||
    plan.refusalCode === "no_zone" ||
    plan.refusalCode === "ambiguous"
  )
}

/** The refusal's notice title: what stands in the way, which the status does not repeat. */
export function refusalTitle(plan: DeploymentDNSRecordPlan): string {
  switch (plan.refusalCode) {
    case "no_credentials":
      return "No DNS credentials are saved"
    case "no_zone":
      return "No saved credential reaches this zone"
    case "ambiguous":
      return "Two providers hold this zone"
    case "tunnel_zone":
      return "The zone is not on Cloudflare"
    case "no_public_address":
      return "This server has no public address"
    case "tunnel_not_connected":
      return "No tunnel is connected"
    case "provider_error":
      return "The provider could not be read"
    case "conflict":
      return "Nothing here deletes a record"
    default:
      return "Cannot be written from here"
  }
}

/**
 * The records to make by hand where the zone is kept, for a plan that names
 * them but cannot write them: what the sheet copies when the press cannot.
 */
export function byHand(plan: DeploymentDNSRecordPlan): DeploymentDNSRecordSet[] {
  return plan.refusalCode && plan.refusalCode !== "conflict" && plan.changes.length === 0
    ? plan.want
    : []
}
