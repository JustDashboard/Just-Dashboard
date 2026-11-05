"use client"

import {
  firstReportDue,
  hubHost,
  hubLabel,
  isIdle,
  reportCadence,
  serverCount,
} from "@/lib/hub-link"
import { duration, relativeTime } from "@/lib/format"
import type { HubLinkScope, HubLinkStatus } from "@/lib/types"
import { LinkPicture } from "@/components/account-link/connect-picture"
import { TextShimmer } from "@/components/ui/text-shimmer"

/**
 * The hub link drawn as what it is, with Settings → Connect's picture: what this server reads,
 * wired into the server and out to the hub. The heartbeat is the account link's bytes, so the
 * left half is the same drawing; the right is the hub — its address over its name and how many
 * servers it holds — instead of the site and an account.
 *
 * The lines are the link's state, as there: every line pulses while reports reach the hub, the
 * hub's line holds still in amber while it cannot be reached or the link is idle — but not before
 * the first report, which is due rather than failed (`firstReportDue`) — only the server's line
 * pulses while a request waits for approval in the hub, and every line is dashed while nothing is
 * connected. The hub's name is its own word, so it is said as "Just Hub (hub)".
 * How often the server reports is the hub's interval (§4.8), anywhere from 10 seconds to 10
 * minutes, so it is read from the status rather than assumed.
 */
export function HubPicture({
  status,
  scope,
  address,
}: {
  status: HubLinkStatus
  /** The scope in force, or the one being chosen while nothing is connected. */
  scope: HubLinkScope
  /** The address being typed, while nothing is connected. */
  address?: string
}) {
  const linked = status.state === "linked"
  const pending = status.state === "pending"
  const link = status.admin?.link
  const due = firstReportDue(status)
  const quiet = linked && !due && (status.reachable === false || Boolean(link?.lastError))
  const idle = isIdle(status)
  const every = status.heartbeatSeconds
  const url = status.hubUrl ?? status.admin?.request?.hubUrl ?? address
  const host = hubHost(url) || "Just Hub"
  const servers = status.fleet?.length ?? 0

  return (
    <LinkPicture
      label={`How this server connects to ${linked || pending ? host : "Just Hub"}`}
      linked={linked}
      pending={pending}
      quiet={quiet}
      scope={scope}
      snapshot={link?.lastSnapshot}
      cadence={`Reports ${reportCadence(every)}`}
      peer={{
        eyebrow: linked || pending || address ? host : "Just Hub",
        title: linked ? (
          <span className="block truncate">{status.hubName ? hubLabel(status.hubName) : host}</span>
        ) : pending ? (
          <TextShimmer>Waiting for approval</TextShimmer>
        ) : (
          <span className="text-muted-foreground">Not connected</span>
        ),
        hint: linked ? (
          idle ? (
            <span className="text-destructive">Idle, nothing is sent</span>
          ) : !due && status.reachable === false ? (
            <span className="text-warning">Not reaching the hub</span>
          ) : status.lastHeartbeatAt ? (
            `${serverCount(servers)} · report ${relativeTime(status.lastHeartbeatAt)}`
          ) : every ? (
            `First report within ${duration(every)}`
          ) : (
            "First report soon"
          )
        ) : pending ? (
          "Type the code below into the hub"
        ) : (
          "Name the hub, pick a scope, connect"
        ),
      }}
    />
  )
}
