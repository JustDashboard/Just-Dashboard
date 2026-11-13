"use client"

import { egressDuration, type EgressGroup } from "@/lib/network-egress"
import type { NetworkLink } from "@/lib/types"
import { cn } from "@/lib/utils"
import { Filter, Globe, Route } from "@/components/icons"
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

/**
 * What a group does, in the wiring every Network picture speaks: the traffic
 * its policy selects on the left, the group in the middle, its members on the
 * right in the order of their tiers. The wire to the member carrying the
 * traffic moves while the group is on and the member is up; a member that is
 * down is dashed, and red where it is the one carrying; a standby, and every
 * wire of a group that is off, is configured but idle.
 *
 * Each member is drawn as what carries it — the tunnel's product, or a router
 * for a gateway — with its tier, its path and its last round trip, so the
 * picture alone answers "which way does this traffic leave, and is the other
 * way healthy".
 */
export function EgressPicture({
  group,
  links,
  loading,
}: {
  group?: EgressGroup
  links: NetworkLink[]
  loading?: boolean
}) {
  if (!group || loading)
    return (
      <WireFlow
        loading
        start={{ title: "Selected traffic", nodes: [], loadingRows: 1 }}
        hub={{ mark: <GroupMark />, eyebrow: "Group" }}
        end={{ title: "Members", nodes: [], loadingRows: 2 }}
      />
    )
  const policy = egressPolicyShort(group.policy)
  const exceptions = group.protected?.length ?? 0
  const traffic: WireFlowNode = {
    id: "traffic",
    mark: (
      <WireMark tone="logo" size="md" shape="square">
        {group.policy.kind === "all" ? <Globe aria-hidden /> : <Filter aria-hidden />}
      </WireMark>
    ),
    eyebrow: "Selected traffic",
    title: <span className="block truncate font-mono">{policy.title}</span>,
    name: policy.title,
    hint: (
      <>
        {policy.hint && <span className="block truncate">{policy.hint}</span>}
        {exceptions > 0 && (
          <span className="block truncate" title={group.protected?.join(", ")}>
            except {exceptions === 1 ? group.protected![0] : `${exceptions} protected addresses`}
          </span>
        )}
      </>
    ),
    line: egressTrafficLine(group),
  }
  const members: WireFlowNode[] = [...group.members]
    .sort((a, b) => a.priority - b.priority || a.id - b.id)
    .map((m) => {
      const state = egressMemberState(m)
      const rtt = m.last?.latencyMillis
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
            <span className={cn("block truncate", TONE_TEXT[state.tone])}>
              {state.label}
              {m.state === "down" && m.last?.why
                ? ` · ${m.last.why}`
                : rtt !== undefined
                  ? ` · ${rtt.toFixed(1)} ms`
                  : ""}
            </span>
            <span className="block truncate font-mono">{egressPath(m)}</span>
          </>
        ),
        line: egressMemberLine(group, m),
      }
    })
  return (
    <WireFlow
      className="animate-rise"
      start={{ title: "Selected traffic", nodes: [traffic] }}
      hub={{
        mark: <GroupMark />,
        eyebrow: `Group table ${group.table}`,
        title: <span className="block truncate">{group.name}</span>,
        hint: (
          <>
            <span className="block">
              {group.sticky ? "sticky" : "not sticky"} · fails back{" "}
              {group.failback === "automatic" ? "on its own" : "by hand"}
            </span>
            <span className="block">
              probed every {egressDuration(group.thresholds.intervalSeconds)}
            </span>
          </>
        ),
      }}
      end={{ title: "Members", nodes: members }}
    />
  )
}

function GroupMark() {
  return (
    <WireMark tone="brand" size="lg" shape="square">
      <Route aria-hidden />
    </WireMark>
  )
}
