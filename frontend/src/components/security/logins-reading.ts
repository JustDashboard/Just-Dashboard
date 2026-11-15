import { prefixRelation } from "@/components/network/tools/subnet-math"
import type { AttackSummary, LoginRecord, LoginSession } from "@/lib/types"

/**
 * What the Logins page says about its three reads, kept apart from React so
 * it is tested in `bun test src`: where the open sessions are held from, which
 * accounts they are, what the line says about them, and whether an attacking
 * address is one the firewall already turns away.
 */

/** A place sessions are held from: an address, or the machine's own console. */
export type Origin = {
  /** The address as the record writes it; "" for the console. */
  address: string
  /** The accounts signed in from here, in the order they signed in. */
  users: string[]
  sessions: number
  /** The oldest of its sessions, which is how long somebody has been in from here. */
  since?: string
}

/** The open sessions folded by where they are held from, in the order the host lists them. */
export function originsOf(sessions: LoginSession[]): Origin[] {
  const byAddress = new Map<string, Origin>()
  for (const session of sessions) {
    const address = session.from.trim()
    const origin = byAddress.get(address) ?? { address, users: [], sessions: 0 }
    origin.sessions++
    if (!origin.users.includes(session.user)) origin.users.push(session.user)
    if (session.loginTime && (!origin.since || session.loginTime < origin.since))
      origin.since = session.loginTime
    byAddress.set(address, origin)
  }
  return [...byAddress.values()]
}

/** An account as the picture draws it: in now, or last in at some time. */
export type Account = {
  user: string
  /** Sessions it holds now. */
  open: number
  /** Where its open sessions are held from; "" is the console. */
  origins: string[]
  /** Its terminals, for an account that is in now. */
  terminals: string[]
  /** When it last signed in, from the record, for an account that is not in now. */
  last?: string
}

/**
 * The accounts on this server the page knows of: those signed in now, then
 * those the login record names, most recent first. A boot or a shutdown is the
 * machine's own line in the record and is nobody's account.
 */
export function accountsOf(sessions: LoginSession[], records: LoginRecord[]): Account[] {
  const byUser = new Map<string, Account>()
  for (const session of sessions) {
    const account = byUser.get(session.user) ?? {
      user: session.user,
      open: 0,
      origins: [],
      terminals: [],
    }
    account.open++
    const origin = session.from.trim()
    if (!account.origins.includes(origin)) account.origins.push(origin)
    if (session.tty && !account.terminals.includes(session.tty)) account.terminals.push(session.tty)
    byUser.set(session.user, account)
  }
  const past = records
    .filter((record) => record.kind === "login" && record.user && record.loginTime)
    .sort((a, b) => (a.loginTime! < b.loginTime! ? 1 : -1))
  for (const record of past) {
    const account = byUser.get(record.user)
    if (!account)
      byUser.set(record.user, {
        user: record.user,
        open: 0,
        origins: [],
        terminals: [],
        last: record.loginTime,
      })
    else if (!account.last) account.last = record.loginTime
  }
  return [...byUser.values()]
}

/**
 * How many origins and how many attackers the picture's left column draws
 * when it has fewer rows than there are of both: its last row then says how
 * many more are in the lists, and the rows before it are shared, so a server
 * with four shells open still shows who is trying hardest and one under
 * attack from fifty addresses still shows who is in.
 */
export function drawnFirst(origins: number, attackers: number, rows: number) {
  if (origins + attackers <= rows) return { origins, attackers }
  const slots = Math.max(rows - 1, 0)
  let o = Math.min(origins, attackers > 0 ? Math.max(slots - 1, 0) : slots)
  const a = Math.min(attackers, slots - o)
  o = Math.min(origins, slots - a)
  return { origins: o, attackers: a }
}

/** Whether pointing at one of an origin and an account lights the other. */
export function heldFrom(account: Account, address: string) {
  return account.open > 0 && account.origins.includes(address)
}

/** "Nobody signed in", "1 signed in": the line's state, as its words. */
export function signedInWords(sessions: number) {
  return sessions === 0 ? "Nobody signed in" : `${sessions.toLocaleString()} signed in`
}

/** The window the failed-login record was folded over: "7 days", "36 hours". */
export function windowWords(hours: number) {
  if (hours >= 48) return `${Math.round(hours / 24)} days`
  return hours === 1 ? "hour" : `${hours} hours`
}

/** "2,312", or "at least 2,312" where the sample ran out inside the window. */
export function attemptsWords(summary: Pick<AttackSummary, "attempts" | "capped">) {
  return `${summary.capped ? "at least " : ""}${summary.attempts.toLocaleString()}`
}

/** An attempt this recent is one in flight, and its line carries. */
export const ATTACKING_NOW_MS = 15 * 60_000

export function attackingNow(last: string | undefined, now: number) {
  if (!last) return false
  const at = new Date(last).getTime()
  return !Number.isNaN(at) && now - at <= ATTACKING_NOW_MS
}

/**
 * Whether the firewall already turns an address away by name: a deny rule for
 * exactly it, or for a network it sits in. A source that is not an address
 * (a rule written against a name) holds nothing.
 */
export function refusedBy(address: string, denied: string[]) {
  const single = `${address}/${address.includes(":") ? 128 : 32}`
  return denied.some((source) => {
    const prefix = source.includes("/") ? source : `${source}/${source.includes(":") ? 128 : 32}`
    try {
      const { relation } = prefixRelation(prefix, single)
      return relation === "same" || relation === "contains"
    } catch {
      return false
    }
  })
}

/** How a session's idle time reads: the host says "active" for one typed into just now. */
export function idleWords(idle: string | undefined) {
  if (!idle) return "idle time unknown"
  return idle === "active" ? "active now" : `idle ${idle}`
}

/** One login record's identity, for the list that holds new rows back. */
export function recordKey(record: LoginRecord) {
  return `${record.user}:${record.tty}:${record.loginTime}:${record.from}`
}
