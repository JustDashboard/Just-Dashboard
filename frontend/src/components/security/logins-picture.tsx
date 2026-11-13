"use client"

import { useCallback, useMemo } from "react"
import { Globe, SecureConnection, Slash, Terminal } from "@/components/icons"
import { networkOf } from "@/lib/clients"
import { plural, relativeTime } from "@/lib/format"
import type { AttackSummary } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useNow } from "@/hooks/use-now"
import { InitialsMark } from "@/components/account/user-avatar"
import { NETWORK_GLYPH } from "@/components/client-mark"
import { WireMark } from "@/components/deploy/wire"
import { ProductGlyph } from "@/components/product-logo"
import { WireFlow, type WireFlowNode } from "@/components/wire-flow"
import {
  attackingNow,
  attemptsWords,
  drawnFirst,
  heldFrom,
  windowWords,
  type Account,
  type Origin,
} from "@/components/security/logins-reading"

/**
 * The rows a column holds. The picture is this tall whatever it draws, so a
 * column with more than this says how many more on its last row.
 */
const ROWS = 4

/** A person's face at the size of the marks beside it, on the tile every mark stands on. */
const FACE = { lg: "size-14", md: "size-11", sm: "size-9" } as const

/**
 * Who is in and who is trying, drawn as the path a login takes: where it comes
 * from on the left, sshd in the middle, the accounts on this server on the
 * right. It is the SSH page's picture of the doors with the people in it:
 * that one says which doors are open, this one says who is standing in them.
 *
 * It replaced four tiles (logged in now, last login, failed attempts,
 * attacking addresses), which were each a count of a list under them. Here a
 * session is a line that moves from the address it is held from, through
 * sshd, to the account it signed in as; an attacking address is drawn in the
 * danger colour and its line stops at sshd, moving while it is still trying
 * (an attempt in the last quarter of an hour); one the firewall already turns
 * away is grey and dashed. An account that is not in now keeps a dashed line:
 * it has a way in and is not using it.
 *
 * Pointing at an address lights the accounts held from it, and the reverse.
 * On a phone it is the marks in rows: the lists under it name each of them.
 */
export function LoginsPicture({
  origins,
  accounts,
  attacks,
  refused,
  unread,
  loading,
}: {
  origins: Origin[]
  accounts: Account[]
  /** The failed-login record folded by address; undefined for a reader who may not read it. */
  attacks: AttackSummary | undefined
  /** Whether the firewall already turns an address away. */
  refused: (address: string) => boolean
  /** The open sessions could not be read, so nobody being in is not known. */
  unread: boolean
  loading: boolean
}) {
  // A minute is fine: it only decides which red lines move.
  const now = useNow(60_000)
  const sessions = origins.reduce((n, origin) => n + origin.sessions, 0)

  const start = useMemo<WireFlowNode[]>(() => {
    const from = origins.map((origin) => originNode(origin))
    const trying: WireFlowNode[] = []
    for (const attacker of attacks?.attackers ?? []) {
      const blocked = refused(attacker.address)
      trying.push({
        id: `attacker:${attacker.address}`,
        name: `${attacker.address}, ${plural(attacker.attempts, "failed attempt")}`,
        href: "#attackers",
        mark: (size) => (
          <WireMark tone={blocked ? "neutral" : "danger"} shape="square" size={size}>
            {blocked ? <Slash aria-hidden /> : <Globe aria-hidden />}
          </WireMark>
        ),
        eyebrow: blocked ? "Blocked" : "Attacking",
        title: (
          <span className={cn("block truncate font-mono", !blocked && "text-destructive")}>
            {attacker.address}
          </span>
        ),
        hint: (
          <span className="numeric block truncate">
            {plural(attacker.attempts, "attempt")}
            {attacker.users.length > 0 && ` · ${attacker.users.slice(0, 2).join(", ")}`}
          </span>
        ),
        line: blocked
          ? { state: "down" }
          : { state: "danger", moving: attackingNow(attacker.last, now) },
      })
    }
    // The rows are shared: the first of each, then the rest behind "N more".
    const first = drawnFirst(from.length, trying.length, ROWS)
    const nodes = [
      ...from.slice(0, first.origins),
      ...trying.slice(0, first.attackers),
      ...from.slice(first.origins),
      ...trying.slice(first.attackers),
    ]
    if (nodes.length > 0) return nodes
    // Nobody in and nobody trying: the place they would come from, at rest.
    return [
      {
        id: "internet",
        mark: (size) => (
          <WireMark tone="logo" shape="square" size={size}>
            <Globe aria-hidden />
          </WireMark>
        ),
        eyebrow: "Anyone",
        title: "The internet",
        hint: unread ? "sessions not read" : "nobody is signed in from it",
        line: { state: "idle" },
      },
    ]
  }, [origins, attacks, refused, unread, now])

  const end = useMemo<WireFlowNode[]>(
    () =>
      accounts.map((account) => ({
        id: `account:${account.user}`,
        name: account.user,
        mark: (size) => (
          <span className="flex shrink-0 rounded-xl bg-background">
            <InitialsMark
              name={account.user}
              className={cn(FACE[size], "rounded-xl", account.open === 0 && "opacity-50")}
            />
          </span>
        ),
        eyebrow: account.open > 0 ? "Signed in" : "Account",
        title: <span className="block truncate">{account.user}</span>,
        hint:
          account.open > 0 ? (
            <span className="block truncate font-mono">
              {account.terminals.join(", ") || "no terminal"}
            </span>
          ) : (
            <span className="block truncate">last in {relativeTime(account.last)}</span>
          ),
        line: account.open > 0 ? { state: "up", moving: true } : { state: "idle" },
      })),
    [accounts],
  )

  const related = useCallback(
    (focus: string, id: string) => {
      const [origin, account] = focus.startsWith("origin:") ? [focus, id] : [id, focus]
      if (!origin.startsWith("origin:") || !account.startsWith("account:")) return false
      const held = accounts.find((a) => `account:${a.user}` === account)
      return !!held && heldFrom(held, origin.slice("origin:".length))
    },
    [accounts],
  )

  return (
    // Four rows tall whatever it holds and while it waits, so the lists under
    // it stay where they are when the reads land; the phone's rows of marks
    // are one height by themselves, given the hub's two lines of words.
    <div
      data-slot="logins-picture"
      className="flex min-h-[20.75rem] flex-col justify-center lg:min-h-[17.75rem]"
    >
      <WireFlow
        className="animate-rise"
        phone="marks"
        loading={loading}
        related={related}
        start={{
          title: "Where from",
          nodes: start,
          max: ROWS - 1,
          loadingRows: 3,
          more: { href: "#attackers", words: (hidden) => `${hidden} more in the lists` },
        }}
        end={{
          title: "Accounts",
          nodes: end,
          max: ROWS - 1,
          loadingRows: 2,
          more: { href: "#recent", words: (hidden) => `${hidden} more in the record` },
          empty: unread ? "The accounts were not read." : "No account has signed in.",
        }}
        hub={{
          mark: (
            <WireMark tone="logo" shape="square">
              <SecureConnection aria-hidden />
            </WireMark>
          ),
          eyebrow: "This server",
          title: "sshd",
          hint: (
            <>
              <span className={cn("numeric block", unread && "text-warning")}>
                {unread
                  ? "sessions not read"
                  : sessions === 0
                    ? "no session open"
                    : `${plural(sessions, "session")} open`}
              </span>
              {/* Always a second line, so the phone's picture, which sets
                  these words over its marks, is one height with and without it. */}
              <span
                className={cn("numeric block", attacks && attacks.attempts > 0 && "text-warning")}
              >
                {!attacks
                  ? "\u00a0"
                  : attacks.attempts > 0
                    ? `${attemptsWords(attacks)} refused in ${windowWords(attacks.windowHours)}`
                    : `none refused in ${windowWords(attacks.windowHours)}`}
              </span>
            </>
          ),
        }}
      />
    </div>
  )
}

/** A place sessions are held from: its network's mark, the address, who and since when. */
function originNode(origin: Origin): WireFlowNode {
  const local = origin.address === ""
  const network = local ? undefined : networkOf(origin.address)
  const Glyph = network ? NETWORK_GLYPH[network.kind] : Terminal
  const who = origin.users.join(", ")
  return {
    id: `origin:${origin.address}`,
    name: `${local ? "the console" : origin.address}, ${who}`,
    href: "#sessions",
    mark: (size) => (
      <WireMark tone="logo" shape="square" size={size}>
        {network?.product ? <ProductGlyph id={network.product} /> : <Glyph aria-hidden />}
      </WireMark>
    ),
    eyebrow: network?.label ?? "Local",
    title: local ? (
      "The console"
    ) : (
      <span className="block truncate font-mono">{origin.address}</span>
    ),
    hint: (
      <span className="block truncate">
        {who}
        {origin.since && ` · ${relativeTime(origin.since)}`}
      </span>
    ),
    line: { state: "up", moving: true },
  }
}
