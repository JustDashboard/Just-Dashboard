"use client"

import { Key } from "@/components/icons"
import { describeClient, networkOf } from "@/lib/clients"
import { relativeTime } from "@/lib/format"
import type { ApiToken, DashboardUser, SessionInfo } from "@/lib/types"
import { WirePlaceholder } from "@/components/wire/wire"
import { WireFlow, type WireFlowNode, type WireLine } from "@/components/wire/wire-flow"
import { useNow } from "@/hooks/use-now"
import { activeNow, keyLine, sessionLine, usableKeys, type Line } from "@/components/account/access"
import {
  AccountWireMark,
  KeyWireMark,
  SessionState,
  SessionWireMark,
  keyHint,
} from "@/components/account/marks"
import { ROLE_NAME } from "@/components/account/capabilities"
import { displayNameOf } from "@/components/user-avatar"

/** How many marks each side of the picture draws before it counts the rest. */
const SHOWN = 4

/**
 * Everything that is signed in as this account, drawn as itself and wired
 * into the account: the sessions on the left as the browsers and programs
 * they are, the API keys on the right as what holds them, and the account in
 * the middle, its face in the brand's ring with what guards it in the corner.
 *
 * It replaced four tiles — two-factor, sessions, keys, users — which said how
 * many of each and never which. Here a wire is a reading: it pulses while the
 * session is open or the key was called today, goes amber where a password
 * alone let a session in or a key is about to stop, and is dashed for a
 * session still owing its code and a key nothing has ever used.
 *
 * It is a `WireFlow`, the picture Notifications and Configuration draw, so it
 * reads, loads and shrinks as they do. Wide, each side's heading is the way to
 * its page. On a phone the two lists already name everything, so the picture
 * is its marks, the lines running down through the account, and the
 * account's name stands beside its face where no line crosses it (they ran
 * through it when the picture drew its own phone layout). While the sessions
 * are read it stands at its loaded shape: pass no `sessions`.
 */
export function AccessPicture({
  user,
  required,
  sessions,
  keys,
}: {
  user: DashboardUser
  required: boolean
  /** Undefined while they are read: the picture waits at its loaded shape. */
  sessions?: SessionInfo[]
  /** Undefined while they could not be read: the side is left off rather than drawn empty. */
  keys?: ApiToken[]
}) {
  const now = useNow(15_000)
  const loading = sessions === undefined

  // This device first, then whatever is open now, then the most recently seen.
  const ordered = [...(sessions ?? [])].sort(
    (a, b) =>
      Number(b.current) - Number(a.current) ||
      Number(activeNow(b, now)) - Number(activeNow(a, now)) ||
      b.lastSeenAt.localeCompare(a.lastSeenAt),
  )
  const usable = usableKeys(keys ?? [], now).sort((a, b) =>
    (b.lastUsedAt ?? "").localeCompare(a.lastUsedAt ?? ""),
  )
  const name = displayNameOf(user)

  const signedIn: WireFlowNode[] = ordered.map((session) => ({
    id: `session:${session.id}`,
    mark: (size) => <SessionWireMark userAgent={session.userAgent} size={size} />,
    eyebrow: <SessionState session={session} now={now} />,
    title: describeClient(session.userAgent),
    hint: (
      <>
        {networkOf(session.ip).label} · <span className="numeric font-mono">{session.ip}</span>
      </>
    ),
    name: `${describeClient(session.userAgent)}, ${sessionHint(session, now)}`,
    line: wire(sessionLine(session, now)),
  }))

  const keyring: WireFlowNode[] =
    usable.length > 0
      ? usable.map((token) => ({
          id: `key:${token.id}`,
          mark: (size) => <KeyWireMark name={token.name} size={size} />,
          eyebrow: ROLE_NAME[token.role],
          title: <span className="font-mono">{token.name}</span>,
          hint: keyHint(token),
          name: `Key ${token.name}, ${ROLE_NAME[token.role]}, ${keyHint(token)}`,
          line: wire(keyLine(token, now)),
        }))
      : keys
        ? [
            {
              id: "key:none",
              mark: (size) => <WirePlaceholder size={size} fallback={Key} />,
              title: (
                <span className="font-normal text-muted-foreground">
                  {keys.length === 0 ? "No API keys" : "No keys in use"}
                </span>
              ),
              hint: "a script signs in with one",
              name: "No API keys",
              href: "/account/keys",
              label: "Make an API key",
              placeholder: true,
              line: { state: "idle" },
            },
          ]
        : []

  const guard = user.totpEnabled
    ? "two-factor on"
    : required
      ? "enrolment required"
      : "password only"

  return (
    <WireFlow
      phone="marks"
      loading={loading}
      start={{
        title: "Sessions",
        count: sessions?.length,
        href: "/account/sessions",
        heading: "always",
        nodes: signedIn,
        max: SHOWN,
        more: { href: "/account/sessions", words: (hidden) => `${hidden} more sessions` },
        loadingRows: 3,
      }}
      hub={{
        mark: <AccountWireMark user={user} />,
        eyebrow: "You",
        title: name,
        hint: <span className={user.totpEnabled ? "text-success" : "text-warning"}>{guard}</span>,
        // The account is known before its sessions are: its name waits as itself.
        loadingLines: { title: name, hint: 1 },
      }}
      end={{
        title: "API keys",
        count: keys ? usable.length : undefined,
        href: keys ? "/account/keys" : undefined,
        heading: keys ? "always" : "narrow",
        nodes: keyring,
        max: SHOWN,
        more: { href: "/account/keys", words: (hidden) => `${hidden} more keys` },
        loadingRows: 2,
      }}
    />
  )
}

/** A session's or a key's line as a wire's state: amber where it should not be relied on. */
function wire(line: Line): WireLine {
  return {
    state: line.tone === "warning" ? "warning" : "up",
    moving: !line.still,
    dashed: line.dashed,
    duration: 2.4,
  }
}

export function sessionHint(session: SessionInfo, now: number) {
  const network = networkOf(session.ip).label
  if (session.current) return `${network} · this device`
  if (activeNow(session, now)) return `${network} · active now`
  return `${network} · seen ${relativeTime(session.lastSeenAt)}`
}
