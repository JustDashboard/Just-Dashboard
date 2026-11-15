import { groupGrant, isPrivateGroup } from "@/components/system-users/access"
import type { SystemGroup } from "@/lib/types"

/**
 * What a host's groups mean, in words, apart from any page: the list, a
 * group's own page and the column beside it all read a group the same way,
 * and it is here so that reading is tested without a browser.
 */

export type GroupReach = {
  /** `danger` is root with nothing asked, `warning` is root, `muted` is nothing. */
  tone: "danger" | "warning" | "default" | "muted"
  /** A few words, for a column or the line under a name. */
  label: string
  /** The whole sentence, where there is room for it. */
  text: string
  /** What follows "Root by another name", or the commands a rule names. */
  detail?: string
}

/** What being in the group hands over: sudo's own rule first, then what its name is known for. */
export function groupReach(group: SystemGroup): GroupReach {
  const rule = group.sudoRules[0]
  if (rule) {
    if (rule.full && rule.noPassword) {
      return { tone: "danger", label: "Root, no password", text: "Root with no password" }
    }
    if (rule.full) {
      return {
        tone: "warning",
        label: "Root, asks for a password",
        text: "Root, asks for a password",
      }
    }
    const commands = rule.commands.join(", ")
    return {
      tone: "default",
      label: "Some commands as root",
      text: `Runs ${commands} as root`,
      detail: commands,
    }
  }
  const words = groupGrant(group.name)
  if (group.rootEquivalent) {
    const text = words ?? "Root by another name"
    return {
      tone: "warning",
      label: "Root by another name",
      text,
      detail: text.includes(": ") ? text.slice(text.indexOf(": ") + 2) : undefined,
    }
  }
  return words
    ? { tone: "default", label: words, text: words }
    : { tone: "muted", label: "", text: "" }
}

/** Whether membership is the whole of root: a rule for everything, or root by another name. */
export function groupReachesRoot(group: SystemGroup) {
  return group.rootEquivalent || group.sudoRules.some((rule) => rule.full)
}

/** Everyone in the group: its members, then the accounts it is the main group of. */
export function groupPeople(group: Pick<SystemGroup, "members" | "primary">) {
  return [...group.members, ...group.primary.filter((name) => !group.members.includes(name))]
}

export function groupHref(name: string, tab?: string) {
  const path = `/system-users/groups/${encodeURIComponent(name)}`
  return tab && tab !== "overview" ? `${path}?tab=${tab}` : path
}

export type GroupScope = "all" | "root"

/** What hands over power first, then the operator's own, then what a package made. */
const rank = (group: SystemGroup) =>
  group.sudoRules.length > 0 || group.rootEquivalent ? 0 : group.system ? 2 : 1

/**
 * The groups a reader came for, in the order they are read. A package's groups
 * and the private group every account has are behind the switch, except the
 * ones that reach root: those are never hidden.
 */
export function listGroups(
  groups: SystemGroup[],
  {
    showSystem,
    query = "",
    scope = "all",
  }: { showSystem: boolean; query?: string; scope?: GroupScope },
) {
  const q = query.trim().toLowerCase()
  return groups
    .filter(
      (group) =>
        showSystem ||
        group.sudoRules.length > 0 ||
        group.rootEquivalent ||
        (!group.system && !isPrivateGroup(group)),
    )
    .filter((group) => scope === "all" || groupReachesRoot(group))
    .filter(
      (group) =>
        !q ||
        group.name.toLowerCase().includes(q) ||
        groupPeople(group).some((member) => member.toLowerCase().includes(q)),
    )
    .sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name))
}

/** The line the Groups page opens on. */
export function groupFigures(groups: SystemGroup[]) {
  const reach = groups.filter(groupReachesRoot)
  return {
    total: groups.length,
    reachRoot: reach.length,
    /** Accounts that are root through a group, each counted once. */
    rootMembers: new Set(reach.flatMap(groupPeople)).size,
    system: groups.filter((group) => group.system).length,
    own: groups.filter((group) => isPrivateGroup(group)).length,
  }
}

/** A group number as the form holds it: a whole number from 0 up, or not a number at all. */
export function validGid(value: string) {
  return /^\d+$/.test(value.trim()) && Number(value) <= 4294967294
}
