"use client"

import { Key, LockClosed, LockOpen, Shield } from "@/components/icons"
import { WireMark } from "@/components/deploy/wire"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { accountHref, reachesRoot, rootSummary, whyClosed } from "@/components/system-users/access"
import { AccountWireMark } from "@/components/system-users/wire-marks"
import { WireFlow, type WireFlowNode, type WireLine } from "@/components/wire-flow"
import { plural } from "@/lib/format"
import type { SystemUser } from "@/lib/types"

/** The accounts the picture has room for before the rest are one "more". */
const SHOWN = 4

/**
 * How an account becomes root, by the strongest way that works today: the
 * line that carries it and the words under its name. `rootSummary` leads with
 * sudo's own rule, which for an account that may run two commands and is also
 * in `docker` says "Some commands" about somebody who is root.
 */
function reachOf(user: SystemUser): { line: WireLine; words: string } {
  const open = user.rootRoutes.filter((route) => route.usable && !route.limited)
  if (open.some((route) => route.kind === "sudo" && route.noPassword)) {
    return { line: { state: "danger" }, words: "No password asked" }
  }
  if (open.some((route) => route.kind === "sudo")) {
    return { line: { state: "warning" }, words: "Asks for password" }
  }
  if (open.length > 0) return { line: { state: "warning" }, words: "Root by another name" }
  // A rule that asks for a password the account does not have: written, not usable.
  return { line: { state: "idle" }, words: rootSummary(user).label }
}

/**
 * Who can be root, as one picture: on the left the accounts that become root
 * (through sudo, or through a group that is root by another name), in the middle
 * root, and on the right the ways somebody signs in as root directly. A line is
 * red where nothing is asked, amber where it works, dashed where the rule is
 * written and cannot be used today.
 *
 * It reads the list the page already holds and asks the host nothing. On a
 * phone it is marks only: the rows under it say every name again.
 */
export function RootPicture({
  users,
  loading,
  expected,
  onMore,
}: {
  users?: SystemUser[]
  loading: boolean
  /** How many accounts reached root the last time, so the waiting picture is that tall. */
  expected: number
  onMore: () => void
}) {
  const root = users?.find((user) => user.uid === 0)
  const reachers = (users ?? []).filter((user) => user.uid !== 0 && reachesRoot(user))
  const start: WireFlowNode[] = reachers.map((user) => {
    const reach = reachOf(user)
    const ways = Array.from(
      new Set(
        user.rootRoutes
          .filter((route) => !route.limited)
          .map((route) => (route.kind === "sudo" ? "sudo" : (route.via ?? ""))),
      ),
    ).filter(Boolean)
    return {
      id: `account:${user.username}`,
      mark: (size) => <AccountWireMark user={user} size={size === "lg" ? "md" : size} />,
      eyebrow: ways.join(" · "),
      title: <span className="font-mono">{user.username}</span>,
      hint: reach.words,
      name: user.username,
      href: accountHref(user.username),
      label: `Open ${user.username}`,
      line: reach.line,
    }
  })
  const password = root?.signIn.password
  const key = root?.signIn.key
  const end: WireFlowNode[] = !root
    ? []
    : [
        {
          id: "root:password",
          mark: (size) => (
            <WireMark
              tone={password?.allowed ? "warning" : "neutral"}
              size={size === "lg" ? "md" : size}
            >
              {password?.allowed ? <LockOpen aria-hidden /> : <LockClosed aria-hidden />}
            </WireMark>
          ),
          eyebrow: "Password",
          title: password?.allowed ? "Signs in over SSH" : "Closed",
          hint: password?.allowed ? undefined : whyClosed(password?.why),
          name: "root's password",
          line: { state: password?.allowed ? "warning" : "idle" },
        },
        {
          id: "root:key",
          mark: (size) => (
            <WireMark
              tone={key?.allowed ? "success" : "neutral"}
              size={size === "lg" ? "md" : size}
            >
              <Key aria-hidden />
            </WireMark>
          ),
          eyebrow: "SSH key",
          title: key?.allowed
            ? root.sshKeyCount > 0
              ? plural(root.sshKeyCount, "key")
              : "Signs in"
            : "Closed",
          hint: key?.allowed ? undefined : whyClosed(key?.why),
          name: "root's SSH keys",
          href: accountHref(root.username),
          label: "Open root",
          line: { state: key?.allowed ? "up" : "idle" },
        },
      ]

  return (
    <Panel plain aria-label="Who can be root">
      <PanelHeader
        title="Who can be root"
        actions={
          users ? (
            <span className="numeric text-hint text-muted-foreground">
              {reachers.length === 0 ? "only root" : plural(reachers.length, "account")}
            </span>
          ) : undefined
        }
      />
      <PanelBody flush>
        <WireFlow
          size="sm"
          phone="marks"
          loading={loading}
          start={{
            title: "Become root",
            nodes: start,
            max: SHOWN,
            more: { onPress: onMore, words: (hidden) => `${hidden} more reach root` },
            empty: "No other account can become root.",
            loadingRows: Math.max(1, Math.min(expected, SHOWN + 1)),
          }}
          hub={{
            mark: (
              <WireMark tone="logo" size="lg">
                <Shield aria-hidden />
              </WireMark>
            ),
            eyebrow: "Everything on this server",
            title: <span className="font-mono">root</span>,
          }}
          end={{
            title: "Sign in as root",
            nodes: end,
            loadingRows: 2,
          }}
        />
      </PanelBody>
    </Panel>
  )
}
