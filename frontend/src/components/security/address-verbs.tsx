"use client"

import { Globe, Inspect, Route, Slash } from "@/components/icons"
import { post } from "@/lib/api"
import type { Verb } from "@/components/verbs"

/**
 * What can be done to an address, wherever one appears on the Security pages.
 *
 * A remote peer on the Connections page, a repeat offender in the ban log and
 * an attacker in the failed-login record are the same kind of thing — an
 * address the operator is looking at because it is doing something to this
 * host — and the three pages used to answer "what can I do about it" three
 * different ways. Declared once, as the verb rule in `components/verbs.tsx`
 * asks: the block is inline because it is the answer, and the lookups go
 * behind the menu with a sentence each, because "asn" is not a word.
 */

/**
 * Deny an address at the firewall, in front of every allow.
 *
 * The comment is the only thing that tells the rule list, a month later, why a
 * bare address is refused. The server puts a source-only deny ahead of the
 * rules it would otherwise sit behind — ufw stops at the first match, so a
 * deny appended after `allow 22` never sees the SSH traffic it was written to
 * refuse — and refuses the caller's own address outright.
 */
export function blockAddress(ip: string, comment: string) {
  return post("/firewall/rules", { action: "deny", direction: "in", from: ip, comment })
}

/** The Tools page opened on one probe with the target filled in. */
export function toolHref(tool: string, target: string, record?: string) {
  const query = new URLSearchParams({ tool, target })
  if (record) query.set("record", record)
  return `/security/tools?${query.toString()}`
}

/**
 * The verbs for one address. `block` is omitted where the caller cannot add a
 * rule, so a limited role sees the lookups and nothing that would be refused.
 */
export function addressVerbs({
  ip,
  block,
  blocking,
  navigate,
}: {
  ip: string
  /** Runs the block. Absent for a role that may not write firewall rules. */
  block?: () => void
  /** The block is in flight, so the row says so rather than offering it twice. */
  blocking?: boolean
  navigate: (href: string) => void
}): Verb[] {
  const verbs: Verb[] = []
  if (block) {
    verbs.push({
      key: "block",
      label: blocking ? "Blocking…" : "Block at the firewall",
      detail: "A deny rule in front of every allow. Unlike a ban, it does not expire.",
      icon: Slash,
      inline: true,
      danger: true,
      disabled: blocking,
      run: block,
    })
  }
  verbs.push(
    {
      key: "owner",
      label: "Who owns this address",
      detail: "Autonomous system, prefix, country and registry.",
      icon: Globe,
      run: () => navigate(toolHref("asn", ip)),
    },
    {
      key: "ptr",
      label: "Reverse lookup",
      detail: "The name the address resolves back to, if it has one.",
      icon: Inspect,
      run: () => navigate(toolHref("dns", ip, "PTR")),
    },
    {
      key: "trace",
      label: "Trace the route to it",
      detail: "What sits between this host and the address.",
      icon: Route,
      run: () => navigate(toolHref("traceroute", ip)),
    },
  )
  return verbs
}
