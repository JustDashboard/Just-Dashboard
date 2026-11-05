"use client"

import { hubHost, serverCount } from "@/lib/hub-link"
import { relativeTime } from "@/lib/format"
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
 * hub's line holds still in amber while it cannot be reached, only the server's line pulses while
 * a request waits for approval in the hub, and every line is dashed while nothing is connected.
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
  const quiet = linked && (status.reachable === false || Boolean(link?.lastError))
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
      cadence="Reports every 15 s"
      peer={{
        eyebrow: linked || pending || address ? host : "Just Hub",
        title: linked ? (
          <span className="block truncate">{status.hubName ?? host}</span>
        ) : pending ? (
          <TextShimmer>Waiting for approval</TextShimmer>
        ) : (
          <span className="text-muted-foreground">Not connected</span>
        ),
        hint: linked ? (
          status.reachable === false ? (
            <span className="text-warning">Not reaching the hub</span>
          ) : status.lastHeartbeatAt ? (
            `${serverCount(servers)} · report ${relativeTime(status.lastHeartbeatAt)}`
          ) : (
            "First report within 15 seconds"
          )
        ) : pending ? (
          "Approve the code below in the hub"
        ) : (
          "Name the hub, pick a scope, connect"
        ),
      }}
    />
  )
}
