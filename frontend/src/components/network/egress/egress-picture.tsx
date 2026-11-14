"use client"

import { relativeTime } from "@/lib/format"
import { egressPolicySummary, type EgressGroup, type EgressMemberView } from "@/lib/network-egress"
import type { NetworkLink } from "@/lib/types"
import { cn } from "@/lib/utils"
import { Filter, Globe, Lifebuoy } from "@/components/icons"
import { WireMark } from "@/components/deploy/wire"
import { WireFlow, type WireFlowNode } from "@/components/wire-flow"
import { MemberGlyph, TONE_TEXT } from "@/components/network/egress/marks"
import {
  egressMemberLine,
  egressMemberState,
  egressPath,
  egressPolicyShort,
  egressTrafficLine,
} from "@/components/network/egress/reading"

/** Room under the group's mark for its three lines, which hang below it wide. */
const HUB = "lg:py-16"

/**
 * What a group does, in the wiring every Network picture speaks: the traffic
 * its policy selects on the left, the group in the middle, its members on the
 * right in the order of their tiers. The wire to the member carrying the
 * traffic moves while the group is on and the member is up; a member that is
 * down is dashed, and red where it is the one carrying; a standby, and every
 * wire of a group that is off, is configured but idle.
 *
 * Each member is drawn as what carries it — the tunnel's product, or a router
 * for a gateway — with its tier, its state and last round trip, and its path,
 * so the picture alone answers "which way does this traffic leave, and is the
 * other way healthy".
 *
 * Every node says the same number of lines whatever it holds, and the
 * loading picture is `WireFlow`'s own with those lines given to it
 * (`loadingLines`), so a group lands without moving the views under it.
 */
export function EgressPicture({
  group,
  links,
  expected = 2,
}: {
  group?: EgressGroup
  links: NetworkLink[]
  /** How many members the group is known to have before it is read. */
  expected?: number
}) {
  if (!group) return <LoadingPicture members={Math.max(1, expected)} />
  const policy = egressPolicyShort(group.policy)
  const traffic: WireFlowNode = {
    id: "traffic",
    mark: (
      <WireMark tone="logo" size="md" shape="square">
        {group.policy.kind === "all" ? <Globe aria-hidden /> : <Filter aria-hidden />}
      </WireMark>
    ),
    // The stacked picture heads the column with the same words.
    eyebrow: <span className="max-lg:hidden">Selected traffic</span>,
    title: (
      <span
        className={cn("block truncate", policy.mono && "font-mono")}
        title={egressPolicySummary(group.policy)}
      >
        {policy.title}
      </span>
    ),
    name: policy.title,
    hint: <span className="block truncate">{policy.hint}</span>,
    line: egressTrafficLine(group),
  }
  const members = [...group.members]
    .sort((a, b) => a.priority - b.priority || a.id - b.id)
    .map((m) => memberNode(group, m, links))
  // When the members it routes through were last decided, and by whom: the
  // monitor, or the account that switched it by hand.
  const decided = group.decidedAt
    ? {
        words: `decided ${relativeTime(group.decidedAt)}`,
        title: `${new Date(group.decidedAt).toLocaleString()}${group.decidedBy ? ` by ${group.decidedBy}` : ""}`,
      }
    : undefined
  return (
    <WireFlow
      className="animate-rise"
      start={{ title: "Selected traffic", nodes: [traffic] }}
      hub={{
        className: HUB,
        mark: <GroupMark />,
        eyebrow: `Group table ${group.table}`,
        title: <span className="block truncate">{group.name}</span>,
        hint: (
          <>
            <span className="block truncate">
              {group.sticky ? "sticky" : "not sticky"} · fails back{" "}
              {group.failback === "automatic" ? "on its own" : "by hand"}
            </span>
            <span className="block truncate" title={decided?.title}>
              {decided?.words ?? "nothing decided yet"}
            </span>
          </>
        ),
      }}
      end={{ title: "Members", nodes: members }}
    />
  )
}

function memberNode(group: EgressGroup, m: EgressMemberView, links: NetworkLink[]): WireFlowNode {
  const state = egressMemberState(group, m)
  const rtt = m.last?.latencyMillis
  const detail =
    m.state === "down" && m.last?.why
      ? ` · ${m.last.why}`
      : rtt !== undefined
        ? ` · ${rtt.toFixed(1)} ms`
        : ""
  return {
    id: `member:${m.id}`,
    mark: (
      <WireMark
        size="md"
        shape="square"
        tone={m.state === "down" ? "danger" : m.active && group.enabled ? "success" : "logo"}
      >
        <MemberGlyph member={m} links={links} />
      </WireMark>
    ),
    eyebrow: `Tier ${m.priority}${m.weight > 1 ? ` · weight ${m.weight}` : ""}`,
    title: <span className="block truncate">{m.name}</span>,
    name: m.name,
    hint: (
      <>
        <span
          className={cn("block truncate", TONE_TEXT[state.tone])}
          title={`${state.label}${detail}`}
        >
          {state.label}
          {detail}
        </span>
        <span className="block truncate font-mono">{egressPath(m)}</span>
      </>
    ),
    line: egressMemberLine(group, m),
  }
}

function GroupMark() {
  return (
    <WireMark tone="brand" size="lg" shape="square">
      <Lifebuoy aria-hidden />
    </WireMark>
  )
}

/**
 * The picture's final shape before the group is read: one selection, the
 * group, and as many members as it is known to have (two, the least a group
 * can, where nothing says). A member says two lines of detail, its state
 * and its path; the selection says one; the group two under its name.
 */
function LoadingPicture({ members }: { members: number }) {
  return (
    <div aria-busy aria-label="Reading the group">
      <WireFlow
        loading
        start={{
          title: "Selected traffic",
          nodes: [],
          loadingRows: 1,
          loadingLines: { eyebrow: <span className="max-lg:hidden">Selected traffic</span> },
        }}
        hub={{
          className: HUB,
          mark: <GroupMark />,
          eyebrow: "Group table",
          loadingLines: { hint: 2 },
        }}
        end={{
          title: "Members",
          nodes: [],
          loadingRows: members,
          loadingLines: (row) => ({ eyebrow: `Tier ${row + 1}`, hint: 2 }),
        }}
      />
    </div>
  )
}
