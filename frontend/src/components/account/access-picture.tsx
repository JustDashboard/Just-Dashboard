"use client"

import { useRef, type RefObject } from "react"
import Link from "next/link"
import { Key } from "@/components/icons"
import { describeClient, networkOf } from "@/lib/clients"
import { relativeTime } from "@/lib/format"
import type { ApiToken, DashboardUser, SessionInfo } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useMediaQuery } from "@/hooks/use-mobile"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { useNow } from "@/components/deploy/vocabulary"
import { activeNow, keyLine, sessionLine, usableKeys, type Line } from "@/components/account/access"
import { AccountWireMark, KeyWireMark, SessionWireMark, WireMore } from "@/components/account/marks"
import { displayNameOf } from "@/components/account/user-avatar"

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
 * session still owing its code and a key nothing has ever used. The same
 * picture the deployment pages and Configuration draw, so a reader who has
 * understood one of them has understood this one.
 *
 * Wide, the three balance across the page the way Notifications' fan-out
 * does. On a phone the two lists already name everything, so the picture
 * shrinks to the marks, the lines running down through the account.
 */
export function AccessPicture({
  user,
  required,
  sessions,
  keys,
}: {
  user: DashboardUser
  required: boolean
  sessions: SessionInfo[]
  /** Undefined while they could not be read: the side is left off rather than drawn empty. */
  keys?: ApiToken[]
}) {
  const wide = useMediaQuery("(min-width: 1024px)")
  const now = useNow(15_000)
  const container = useRef<HTMLDivElement>(null)
  const hub = useRef<HTMLDivElement>(null)

  // This device first, then whatever is open now, then the most recently seen.
  const ordered = [...sessions].sort(
    (a, b) =>
      Number(b.current) - Number(a.current) ||
      Number(activeNow(b, now)) - Number(activeNow(a, now)) ||
      b.lastSeenAt.localeCompare(a.lastSeenAt),
  )
  const usable = usableKeys(keys ?? [], now).sort((a, b) =>
    (b.lastUsedAt ?? "").localeCompare(a.lastUsedAt ?? ""),
  )
  const shownSessions = ordered.slice(0, SHOWN)
  const shownKeys = usable.slice(0, SHOWN)
  const size = wide ? "md" : "sm"
  const name = displayNameOf(user)

  const left = (
    <>
      {shownSessions.map((session) => (
        <Spoke
          key={session.id}
          containerRef={container}
          hubRef={hub}
          side="in"
          compact={!wide}
          line={sessionLine(session, now)}
          mark={<SessionWireMark userAgent={session.userAgent} size={size} />}
          title={describeClient(session.userAgent)}
          hint={sessionHint(session, now)}
          label={`${describeClient(session.userAgent)}, ${sessionHint(session, now)}`}
        />
      ))}
      {ordered.length > SHOWN && (
        <Spoke
          containerRef={container}
          hubRef={hub}
          side="in"
          compact={!wide}
          line={{ still: true, tone: "default" }}
          mark={
            <WireMore
              count={ordered.length - SHOWN}
              href="/account/sessions"
              label={`${ordered.length - SHOWN} more sessions`}
              size={size}
            />
          }
          title={<span className="font-normal text-muted-foreground">more sessions</span>}
        />
      )}
    </>
  )

  const right = (
    <>
      {shownKeys.map((token) => (
        <Spoke
          key={token.id}
          containerRef={container}
          hubRef={hub}
          side="out"
          compact={!wide}
          line={keyLine(token, now)}
          mark={<KeyWireMark name={token.name} size={size} />}
          title={<span className="font-mono">{token.name}</span>}
          hint={keyHint(token)}
          label={`Key ${token.name}, ${keyHint(token)}`}
        />
      ))}
      {usable.length > SHOWN && (
        <Spoke
          containerRef={container}
          hubRef={hub}
          side="out"
          compact={!wide}
          line={{ still: true, tone: "default" }}
          mark={
            <WireMore
              count={usable.length - SHOWN}
              href="/account/keys"
              label={`${usable.length - SHOWN} more keys`}
              size={size}
            />
          }
          title={<span className="font-normal text-muted-foreground">more keys</span>}
        />
      )}
      {keys?.length === 0 && (
        <Spoke
          containerRef={container}
          hubRef={hub}
          side="out"
          compact={!wide}
          line={{ still: true, dashed: true, tone: "default" }}
          mark={
            <Link
              href="/account/keys"
              aria-label="Make an API key"
              className="rounded-full focus-ring"
            >
              <WirePlaceholder size={size} fallback={Key} />
            </Link>
          }
          title={<span className="font-normal text-muted-foreground">No API keys</span>}
          hint="a script signs in with one"
        />
      )}
    </>
  )

  const guard = user.totpEnabled
    ? "two-factor on"
    : required
      ? "enrolment required"
      : "password only"

  return (
    <div ref={container} className="relative mx-auto w-full max-w-5xl py-2">
      <div aria-hidden className="wire-grid pointer-events-none absolute -inset-x-4 inset-y-0" />
      {wide ? (
        <div className="relative grid grid-cols-[minmax(0,1fr)_minmax(3rem,0.45fr)_auto_minmax(3rem,0.45fr)_minmax(0,1fr)] items-center pb-8">
          <div role="group" aria-label="Signed in" className="flex flex-col items-end gap-5">
            <Lane href="/account/sessions" count={sessions.length} noun="session" align="end" />
            {left}
          </div>
          <div aria-hidden />
          <WireNode
            nodeRef={hub}
            align="center"
            mark={<AccountWireMark user={user} />}
            eyebrow="You"
            title={name}
            hint={
              <span className={user.totpEnabled ? "text-success" : "text-warning"}>{guard}</span>
            }
          />
          <div aria-hidden />
          <div role="group" aria-label="API keys" className="flex flex-col gap-5">
            {keys && <Lane href="/account/keys" count={usable.length} noun="key" align="start" />}
            {right}
          </div>
        </div>
      ) : (
        // The words over the marks, so the lines run down over nothing but
        // the page's own ground.
        <div className="relative flex flex-col items-center text-center">
          <Lane href="/account/sessions" count={sessions.length} noun="session" align="center" />
          <div
            role="group"
            aria-label="Signed in"
            className="mt-3 flex flex-wrap justify-center gap-4"
          >
            {left}
          </div>
          <div ref={hub} className="relative z-10 mt-10 flex">
            <AccountWireMark user={user} />
          </div>
          <p className="mt-2 text-body font-medium">{name}</p>
          <p className={cn("text-hint", user.totpEnabled ? "text-success" : "text-warning")}>
            {guard}
          </p>
          <div
            role="group"
            aria-label="API keys"
            className="mt-8 flex flex-wrap justify-center gap-4"
          >
            {right}
          </div>
          {keys && (
            <Lane
              href="/account/keys"
              count={usable.length}
              noun="key"
              align="center"
              className="mt-3"
            />
          )}
        </div>
      )}
    </div>
  )
}

/** The head of one side of the picture: what it counts, as the way to its page. */
function Lane({
  href,
  count,
  noun,
  align,
  className,
}: {
  href: string
  count: number
  noun: string
  align: "start" | "end" | "center"
  className?: string
}) {
  return (
    <Link
      href={href}
      className={cn(
        "eyebrow rounded-sm focus-ring transition-colors hover:text-foreground",
        align === "end" && "self-end",
        className,
      )}
    >
      <span className="numeric">{count}</span> {count === 1 ? noun : `${noun}s`} →
    </Link>
  )
}

/**
 * One thing on one side of the account, with its wire. A session's pulse runs
 * into the account, a key's out of it: a key is the account, acting somewhere
 * else.
 */
function Spoke({
  containerRef,
  hubRef,
  side,
  compact,
  line,
  mark,
  title,
  hint,
  label,
}: {
  containerRef: RefObject<HTMLDivElement | null>
  hubRef: RefObject<HTMLDivElement | null>
  side: "in" | "out"
  compact: boolean
  line: Line
  mark: React.ReactNode
  title: React.ReactNode
  hint?: React.ReactNode
  label?: string
}) {
  const node = useRef<HTMLDivElement>(null)
  const beam = (
    <AnimatedBeam
      containerRef={containerRef}
      fromRef={side === "in" ? node : hubRef}
      toRef={side === "in" ? hubRef : node}
      shape={compact ? "arc" : "s"}
      still={line.still}
      dashed={line.dashed}
      tone={line.tone}
      duration={2.4}
    />
  )
  if (compact)
    return (
      <div className="min-w-0">
        {beam}
        <div ref={node} className="relative z-10 flex">
          {mark}
        </div>
        {label && <span className="sr-only">{label}</span>}
      </div>
    )
  return (
    <div className="min-w-0">
      {beam}
      <WireNode
        nodeRef={node}
        align={side === "in" ? "end" : "start"}
        mark={mark}
        title={title}
        hint={hint}
      />
    </div>
  )
}

export function sessionHint(session: SessionInfo, now: number) {
  const network = networkOf(session.ip).label
  if (session.current) return `${network} · this device`
  if (activeNow(session, now)) return `${network} · active now`
  return `${network} · seen ${relativeTime(session.lastSeenAt)}`
}

function keyHint(token: ApiToken) {
  const use = token.lastUsedAt ? `used ${relativeTime(token.lastUsedAt)}` : "never used"
  return `${token.role} · ${use}`
}
