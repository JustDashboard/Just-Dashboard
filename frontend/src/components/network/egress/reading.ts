import type { Tone } from "@/components/tone"
import type { WireLine } from "@/components/wire-flow-model"
import {
  egressNames,
  type EgressDecision,
  type EgressGroup,
  type EgressMemberView,
  type EgressPolicy,
} from "@/lib/network-egress"

/**
 * The words and lines the list's cards, a group's page and its picture say,
 * kept apart from React so `bun test src` reads them.
 */

export const EGRESS_TABS = ["members", "decisions", "simulation"] as const
export type EgressTab = (typeof EGRESS_TABS)[number]

/** A group's own page; the first view is the address without a `tab`. */
export function egressHref(name: string, tab?: string): string {
  const base = `/network/egress/${encodeURIComponent(name)}`
  return tab && tab !== "members" ? `${base}?tab=${tab}` : base
}

/** The route's segment as a group's name. */
export function egressNameOf(segment: string | undefined): string {
  if (!segment) return ""
  try {
    return decodeURIComponent(segment)
  } catch {
    return segment
  }
}

/** The group a name opens: the server refuses two names that differ only in case. */
export function findEgressGroup<T extends Pick<EgressGroup, "name">>(
  groups: T[],
  name: string,
): T | undefined {
  const wanted = name.trim().toLowerCase()
  return groups.find((g) => g.name.toLowerCase() === wanted)
}

/** The card's mono line under the name: "sticky · 2 members". */
export function egressRowLine(group: Pick<EgressGroup, "sticky" | "members" | "family">): string {
  const parts = [group.sticky ? "sticky" : "not sticky", plural(group.members.length, "member")]
  if (group.family === "inet6") parts.unshift("IPv6")
  return parts.join(" · ")
}

/** What the policy selects, short enough for a fact or a node. */
export function egressPolicyShort(policy: EgressPolicy): { title: string; hint?: string } {
  switch (policy.kind) {
    case "all":
      return { title: "All outbound traffic", hint: "what the main table sends to its default" }
    case "selector":
      return policy.from
        ? { title: `From ${policy.from}`, hint: policy.to ? `to ${policy.to}` : "to anywhere" }
        : { title: `To ${policy.to ?? "anywhere"}` }
    case "rule":
      return policy.fwmark
        ? {
            title: `Marked ${policy.fwmark}`,
            hint: policy.iif ? `arriving on ${policy.iif}` : undefined,
          }
        : { title: `Arriving on ${policy.iif ?? "any device"}` }
  }
}

export type Carrying = {
  /** The state word beside the group's name. */
  label: string
  tone: Tone
  /** The members it routes through now. */
  members: EgressMemberView[]
}

/**
 * Which member carries the group's traffic, as the state a card and the
 * identity line show. A group that is off carries nothing, whatever it has
 * decided; one whose carrying member is down says so in red.
 */
export function egressCarrying(group: EgressGroup): Carrying {
  const members = group.members.filter((m) => m.active)
  if (!group.enabled) return { label: "Not carrying traffic", tone: "default", members }
  if (members.length === 0) return { label: "No member decided", tone: "danger", members }
  const names = egressNames(group, group.active)
  if (members.some((m) => m.state === "down"))
    return { label: `${names}, down`, tone: "danger", members }
  if (members.some((m) => !m.proven))
    return { label: `${names}, unproven`, tone: "warning", members }
  return { label: `Through ${names}`, tone: "success", members }
}

/** How the group switches, in a word. */
export function egressAutomationWord(group: Pick<EgressGroup, "automation" | "enabled">): string {
  if (!group.enabled) return "Off"
  return group.automation ? "Automatic" : "By hand"
}

/** A member's own state, in the words the picture and the table say. */
export function egressMemberState(member: EgressMemberView): { label: string; tone: Tone } {
  if (member.state === "down") return { label: "Down", tone: "danger" }
  if (member.state === "up" && member.proven)
    return { label: member.active ? "Carrying" : "Standby", tone: "success" }
  if (member.state === "up") return { label: "Up, evidence stale", tone: "warning" }
  return { label: "Measuring", tone: "default" }
}

/**
 * The wire from the group to a member. The one carrying traffic moves while
 * it is up and the group is on; a carrying member that failed is red; a
 * member that is down is dashed; a standby, or anything of a group that is
 * off, is configured but idle.
 */
export function egressMemberLine(group: EgressGroup, member: EgressMemberView): WireLine {
  if (member.active && group.enabled) {
    if (member.state === "down") return { state: "danger" }
    if (member.state === "up") return { state: member.proven ? "up" : "warning", moving: true }
    return { state: "idle" }
  }
  if (member.state === "down") return { state: "down" }
  return { state: "idle" }
}

/** The wire from the selected traffic into the group. */
export function egressTrafficLine(group: EgressGroup): WireLine {
  if (!group.enabled) return { state: "idle" }
  const carrying = group.members.filter((m) => m.active)
  if (carrying.length === 0 || carrying.every((m) => m.state === "down")) return { state: "danger" }
  return { state: "up", moving: true }
}

/** The path a member stands for, without the verb: "203.0.113.1 · eth0". */
export function egressPath(member: Pick<EgressMemberView, "kind" | "gateway" | "device">): string {
  return member.kind === "tunnel" ? member.device : `${member.gateway} · ${member.device}`
}

/** What the rules would do now, as a sentence; nothing where they would do nothing. */
export function egressAdvice(
  group: Pick<EgressGroup, "members" | "enabled">,
  advice: EgressDecision | undefined,
): string | undefined {
  if (!advice || !group.enabled) return undefined
  if (advice.action === "none") return undefined
  const to = advice.target?.length ? ` to ${egressNames(group, advice.target)}` : ""
  const verb =
    advice.action === "hold"
      ? "hold"
      : advice.action === "rebalance"
        ? "rebalance"
        : advice.action === "failback"
          ? `fail back${to}`
          : `fail over${to}`
  return `The rules would ${verb} now: ${advice.reason}`
}

/** The sibling before or after a group, for Alt+↑ and Alt+↓. */
export function egressSibling<T extends Pick<EgressGroup, "name">>(
  groups: T[],
  name: string,
  direction: 1 | -1,
): T | undefined {
  const at = groups.findIndex((g) => g.name.toLowerCase() === name.toLowerCase())
  if (at < 0) return direction > 0 ? groups[0] : groups[groups.length - 1]
  return groups[at + direction]
}

function plural(n: number, word: string) {
  return `${n} ${n === 1 ? word : `${word}s`}`
}
