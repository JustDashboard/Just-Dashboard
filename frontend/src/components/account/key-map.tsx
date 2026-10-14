"use client"

import { useRef, type RefObject } from "react"
import { relativeTime } from "@/lib/format"
import type { ApiToken, Role } from "@/lib/types"
import { useMediaQuery } from "@/hooks/use-mobile"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { WireHost, WireMark, WireNode } from "@/components/deploy/wire"
import { useNow } from "@/components/deploy/vocabulary"
import { EndpointText } from "@/components/config/request-path"
import { ROLE_HUE, ROLE_MARK, ROLE_NAME } from "@/components/account/capabilities"
import {
  ROLES,
  expiringSoon,
  keyLine,
  usableKeys,
  usedToday,
  type Line,
} from "@/components/account/access"
import { KeyWireMark, WireMore } from "@/components/account/marks"

/** How many keys the picture draws before it counts the rest. */
const SHOWN = 6

/**
 * Where the keys reach, drawn the way the deployment pages draw where a
 * request goes: every key that still opens a door as what holds it — GitHub's
 * mark for `github-actions`, Grafana's for `grafana` — wired through the role
 * it carries into this server's API.
 *
 * It replaced four tiles: in use, used this week, never used, expiring soon.
 * Each is a wire now. A key called today pulses, so "used this week" is the
 * set of lines with something on them; one that has never been called is
 * dashed, the key nobody would notice being used; one about to stop is amber.
 * The roles in the middle say what each key may do without a column of
 * badges, and the line out of a role is as busy as the busiest key in it.
 * The counts went to the chips over the list below, which narrow it as well.
 */
export function KeyMap({ tokens, origin }: { tokens: ApiToken[]; origin: string }) {
  const wide = useMediaQuery("(min-width: 1024px)")
  const now = useNow(15_000)
  const container = useRef<HTMLDivElement>(null)
  const server = useRef<HTMLDivElement>(null)
  const admin = useRef<HTMLDivElement>(null)
  const limited = useRef<HTMLDivElement>(null)
  const readonly = useRef<HTMLDivElement>(null)
  const roleRef: Record<Role, RefObject<HTMLDivElement | null>> = { admin, limited, readonly }

  const usable = usableKeys(tokens, now).sort(
    (a, b) =>
      ROLES.indexOf(a.role) - ROLES.indexOf(b.role) ||
      (b.lastUsedAt ?? "").localeCompare(a.lastUsedAt ?? ""),
  )
  const shown = usable.slice(0, SHOWN)
  const size = wide ? "md" : "sm"

  const keys = (
    <>
      {shown.map((token) => (
        <KeyNode
          key={token.id}
          containerRef={container}
          roleRef={roleRef[token.role]}
          compact={!wide}
          token={token}
          line={keyLine(token, now)}
        />
      ))}
      {usable.length > SHOWN && (
        <WireMore
          count={usable.length - SHOWN}
          href="#keys"
          label={`${usable.length - SHOWN} more keys`}
          size={size}
        />
      )}
    </>
  )

  const roles = ROLES.map((role) => {
    const held = usable.filter((k) => k.role === role)
    const Mark = ROLE_MARK[role]
    const line: Line = {
      still: !held.some((k) => usedToday(k, now)),
      dashed: held.length === 0,
      tone: held.some((k) => expiringSoon(k, now)) ? "warning" : "default",
    }
    return (
      <div key={role} className="min-w-0">
        <AnimatedBeam
          containerRef={container}
          fromRef={roleRef[role]}
          toRef={server}
          shape={wide ? "s" : "arc"}
          still={line.still}
          dashed={line.dashed}
          tone={line.tone}
          duration={2.4}
          delay={0.6}
        />
        {wide ? (
          <WireNode
            nodeRef={roleRef[role]}
            align="center"
            mark={
              <WireMark tone="logo" size="md" className={held.length === 0 ? "opacity-50" : ""}>
                <Mark aria-hidden style={{ color: ROLE_HUE[role] }} />
              </WireMark>
            }
            title={ROLE_NAME[role]}
            hint={
              held.length === 0 ? "no key" : `${held.length} ${held.length === 1 ? "key" : "keys"}`
            }
          />
        ) : (
          <div ref={roleRef[role]} className="relative z-10 flex">
            <WireMark tone="logo" size="sm" className={held.length === 0 ? "opacity-50" : ""}>
              <Mark aria-hidden style={{ color: ROLE_HUE[role] }} />
            </WireMark>
            <span className="sr-only">{`${ROLE_NAME[role]}: ${held.length} keys`}</span>
          </div>
        )}
      </div>
    )
  })

  const host = new URL(origin).host

  return (
    <div ref={container} className="relative mx-auto w-full max-w-5xl py-2">
      <div aria-hidden className="wire-grid pointer-events-none absolute -inset-x-4 inset-y-0" />
      {wide ? (
        <div className="relative grid grid-cols-[minmax(0,1.1fr)_minmax(2rem,0.35fr)_auto_minmax(2rem,0.35fr)_minmax(0,0.9fr)] items-center">
          <div role="group" aria-label="Keys" className="flex flex-col items-end gap-5">
            {keys}
          </div>
          <div aria-hidden />
          <div role="group" aria-label="Roles" className="flex flex-col gap-16 pb-12">
            {roles}
          </div>
          <div aria-hidden />
          <WireNode
            nodeRef={server}
            mark={<WireHost />}
            eyebrow="This server"
            title={<EndpointText url={`${origin}/api/v1`} className="block" />}
            hint="a bearer token on every request"
          />
        </div>
      ) : (
        <div className="relative flex flex-col items-center text-center">
          <div role="group" aria-label="Keys" className="flex flex-wrap justify-center gap-4">
            {keys}
          </div>
          <div role="group" aria-label="Roles" className="mt-10 flex justify-center gap-8">
            {roles}
          </div>
          <div ref={server} className="relative z-10 mt-10 flex">
            <WireHost size="md" />
          </div>
          <p className="mt-2 font-mono text-hint text-muted-foreground">{host}/api/v1</p>
        </div>
      )}
    </div>
  )
}

function KeyNode({
  token,
  containerRef,
  roleRef,
  compact,
  line,
}: {
  token: ApiToken
  containerRef: RefObject<HTMLDivElement | null>
  roleRef: RefObject<HTMLDivElement | null>
  compact: boolean
  line: Line
}) {
  const node = useRef<HTMLDivElement>(null)
  const hint = [
    token.lastUsedAt ? `used ${relativeTime(token.lastUsedAt)}` : "never used",
    token.expiresAt && `expires ${relativeTime(token.expiresAt)}`,
  ]
    .filter(Boolean)
    .join(" · ")
  const beam = (
    <AnimatedBeam
      containerRef={containerRef}
      fromRef={node}
      toRef={roleRef}
      shape={compact ? "arc" : "s"}
      still={line.still}
      dashed={line.dashed}
      tone={line.tone}
      duration={2.4}
      delay={(token.id % 4) * 0.3}
    />
  )
  if (compact)
    return (
      <div className="min-w-0">
        {beam}
        <div ref={node} className="relative z-10 flex">
          <KeyWireMark name={token.name} size="sm" />
        </div>
        <span className="sr-only">{`${token.name}: ${hint}`}</span>
      </div>
    )
  return (
    <div className="min-w-0">
      {beam}
      <WireNode
        nodeRef={node}
        align="end"
        mark={<KeyWireMark name={token.name} />}
        title={<span className="font-mono">{token.name}</span>}
        hint={<span className={line.tone === "warning" ? "text-warning" : undefined}>{hint}</span>}
      />
    </div>
  )
}
