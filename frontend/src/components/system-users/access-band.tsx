"use client"

import { Key, ShieldCheck } from "@/components/icons"
import { useNow } from "@/components/deploy/vocabulary"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph } from "@/components/product-logo"
import { ShareBar } from "@/components/procs/workloads"
import { Address } from "@/components/security/marks"
import { Status } from "@/components/status-dot"
import { ChipStrip, FilterChip } from "@/components/tabs"
import { NumberTicker } from "@/components/ui/number-ticker"
import {
  WINDOWS,
  accountHue,
  isBare,
  keyShares,
  placeOnAxis,
  rootReach,
  signIns,
  type RootRoute,
  type WindowKey,
} from "@/components/system-users/access"
import { AccountMark } from "@/components/system-users/marks"
import { plural, relativeTime } from "@/lib/format"
import type { SystemUser } from "@/lib/types"
import { useSessionState } from "@/lib/view-state"
import { cn } from "@/lib/utils"

/** How many accounts each block names; the rest are counted under it. */
const SHOWN = 6

/** A sign-in this recent is still happening as far as the page can tell. */
const FRESH = 120_000

/**
 * Who can get into the host and who can become root, as three blocks over the
 * accounts: when each last signed in, on one axis that ends at this second;
 * everyone who holds the power to run anything; and whose keys open the host.
 *
 * It replaced four tiles that each gave one of these as a figure — accounts,
 * administrators, can sign in, last sign-in — and none of which said *who*.
 * Here the people are the picture: each a face in the hue it has on every
 * other page, a dot that moves toward "now" end of its axis as the clock runs,
 * a ring round the sign-in that is under two minutes old. A press on any row
 * opens that account's keys, as its card does.
 */
export function AccessBand({
  users,
  onOpen,
}: {
  users: SystemUser[]
  onOpen: (username: string) => void
}) {
  return (
    <div
      data-slot="access-band"
      className="grid min-w-0 gap-x-10 gap-y-6 lg:grid-cols-2 xl:grid-cols-3"
    >
      <SignInsBlock users={users} onOpen={onOpen} />
      <RootBlock users={users} onOpen={onOpen} />
      <KeysBlock users={users} onOpen={onOpen} />
    </div>
  )
}

function SignInsBlock({
  users,
  onOpen,
}: {
  users: SystemUser[]
  onOpen: (username: string) => void
}) {
  const now = useNow(15_000)
  const [windowKey, setWindowKey] = useSessionState<WindowKey>("system-users.window", "30d")
  const span = WINDOWS.find((w) => w.key === windowKey) ?? WINDOWS[2]
  const { within, older, never } = signIns(users, now, span.ms)
  const shown = within.slice(0, SHOWN)
  const rest = within.length - shown.length

  return (
    <Panel plain aria-label="Last sign-ins" className="lg:col-span-2 xl:col-span-1">
      <PanelHeader title="Last sign-ins">
        <ChipStrip aria-label="Window" className="ml-auto">
          {WINDOWS.map((w) => (
            <FilterChip
              key={w.key}
              selected={windowKey === w.key}
              onClick={() => setWindowKey(w.key)}
            >
              {w.label}
            </FilterChip>
          ))}
        </ChipStrip>
      </PanelHeader>
      <PanelBody className="pt-1">
        {shown.length === 0 ? (
          <p className="py-3 text-body text-muted-foreground">
            Nobody signed in during the last {span.label}.
          </p>
        ) : (
          <ol aria-label="Sign-ins">
            {shown.map(({ user, at }, index) => (
              <SignInRow
                key={user.username}
                user={user}
                at={at}
                now={now}
                windowMs={span.ms}
                first={index === 0}
                onOpen={onOpen}
              />
            ))}
          </ol>
        )}
        <p className="flex flex-wrap items-center gap-x-2 border-t border-hairline pt-3 text-hint text-muted-foreground">
          {[
            rest > 0 && `${rest} more in the window`,
            older > 0 && `${older} earlier`,
            never > 0 && `${never} never signed in`,
          ]
            .filter(Boolean)
            .map((text, i) => (
              <span key={i} className="inline-flex items-center gap-2">
                {i > 0 && <span className="text-muted-foreground/40">·</span>}
                {text}
              </span>
            ))}
          {rest + older + never === 0 && <span>Every account that can sign in has.</span>}
        </p>
      </PanelBody>
    </Panel>
  )
}

function SignInRow({
  user,
  at,
  now,
  windowMs,
  first,
  onOpen,
}: {
  user: SystemUser
  at: number
  now: number
  windowMs: number
  first: boolean
  onOpen: (username: string) => void
}) {
  const fresh = now - at < FRESH
  const left = placeOnAxis(at, now, windowMs)
  const color = accountHue(user.username)
  return (
    <li
      className={cn(
        "group -mx-3 px-3 py-2.5 transition-colors hover:bg-row-hover",
        !first && "border-t border-hairline",
      )}
    >
      <div className="flex min-w-0 items-center gap-2.5">
        <AccountMark user={user} size="sm" />
        <button
          type="button"
          onClick={() => onOpen(user.username)}
          className="min-w-0 truncate rounded-sm text-body font-medium focus-ring"
        >
          {user.username}
        </button>
        <span className="numeric ml-auto shrink-0 text-hint text-muted-foreground">
          {fresh ? <span className="text-success">just now</span> : relativeTime(user.lastLogin)}
        </span>
      </div>
      <div className="mt-2 flex min-w-0 items-center gap-3">
        <div
          role="img"
          aria-label={`${user.username} signed in ${relativeTime(user.lastLogin)}`}
          className="relative h-3 min-w-0 flex-1"
        >
          {[25, 50, 75].map((tick) => (
            <span
              key={tick}
              aria-hidden
              className="absolute inset-y-0 w-px bg-hairline"
              style={{ left: `${tick}%` }}
            />
          ))}
          <span aria-hidden className="absolute inset-x-0 top-1/2 h-px bg-hairline" />
          <span
            aria-hidden
            className="absolute top-1/2 h-px -translate-y-1/2 transition-[width] duration-700 ease-out"
            style={{
              left: `${left}%`,
              right: 0,
              background: `color-mix(in oklab, ${color} 55%, transparent)`,
            }}
          />
          <span
            aria-hidden
            className="absolute top-1/2 size-2.5 -translate-x-1/2 -translate-y-1/2 transition-[left] duration-700 ease-out"
            style={{ left: `${left}%` }}
          >
            {fresh && (
              <span
                className="absolute -inset-1 animate-breathe rounded-full"
                style={{ background: color }}
              />
            )}
            <span className="absolute inset-0 rounded-full" style={{ background: color }} />
          </span>
        </div>
        {user.lastLoginFrom && (
          <Address
            ip={user.lastLoginFrom}
            className="max-w-[45%] shrink-0 text-hint text-muted-foreground"
          />
        )}
      </div>
    </li>
  )
}

function RootBlock({ users, onOpen }: { users: SystemUser[]; onOpen: (username: string) => void }) {
  const reach = rootReach(users)
  const shown = reach.slice(0, SHOWN)
  const bare = reach.filter((entry) => isBare(entry.user)).length

  return (
    <Panel plain aria-label="Root access">
      <PanelHeader
        title="Can become root"
        actions={
          <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
            <span className={cn("font-medium", bare > 0 ? "text-warning" : "text-foreground")}>
              <NumberTicker value={reach.length} />
            </span>
            {reach.length === 1 ? "account" : "accounts"}
          </span>
        }
      />
      <PanelBody className="pt-1">
        {shown.length === 0 ? (
          <p className="py-3 text-body text-muted-foreground">
            No account is in sudo, wheel, admin or docker.
          </p>
        ) : (
          <ul>
            {shown.map(({ user, routes }, index) => (
              <li
                key={user.username}
                className={cn("-mx-3 px-3", index > 0 && "border-t border-hairline")}
              >
                <button
                  type="button"
                  onClick={() => onOpen(user.username)}
                  aria-label={`SSH keys for ${user.username}`}
                  className="group flex w-full min-w-0 items-center gap-2.5 rounded-sm py-2.5 text-left focus-ring transition-colors hover:bg-row-hover"
                >
                  <AccountMark user={user} size="sm" />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate font-mono text-body font-medium">
                      {user.username}
                    </span>
                    <span className="flex min-w-0 items-center gap-3 text-hint text-muted-foreground">
                      {routes.map((route) => (
                        <RouteMark key={route.label} route={route} />
                      ))}
                    </span>
                  </span>
                  <span className="shrink-0 text-hint">
                    {isBare(user) ? (
                      <Status tone="warning" label="No password" />
                    ) : user.locked ? (
                      <Status tone="stopped" label="Locked" />
                    ) : (
                      <span className="inline-flex items-center gap-1 text-muted-foreground">
                        <Key aria-hidden className="size-3.5" />
                        <span className="numeric">{user.sshKeyCount}</span>
                      </span>
                    )}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}
        {reach.length > shown.length && (
          <p className="border-t border-hairline pt-3 text-hint text-muted-foreground">
            {reach.length - shown.length} more
          </p>
        )}
      </PanelBody>
    </Panel>
  )
}

function RouteMark({ route }: { route: RootRoute }) {
  return (
    <span className="inline-flex shrink-0 items-center gap-1 text-foreground">
      {route.kind === "docker" ? (
        <ProductGlyph id="docker" />
      ) : (
        <ShieldCheck aria-hidden className="size-3.5" />
      )}
      {route.label}
    </span>
  )
}

function KeysBlock({ users, onOpen }: { users: SystemUser[]; onOpen: (username: string) => void }) {
  const { holders, total } = keyShares(users)
  const shown = holders.slice(0, SHOWN)

  return (
    <Panel plain aria-label="SSH keys by account" className="lg:max-xl:col-span-2">
      <PanelHeader
        title="SSH keys"
        actions={
          <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
            <span className="font-medium text-foreground">
              <NumberTicker value={total} />
            </span>
            authorised
          </span>
        }
      />
      <PanelBody className="space-y-3 pt-4">
        {total === 0 ? (
          <p className="py-2 text-body text-muted-foreground">
            No account has an authorised key, so none can be reached by one.
          </p>
        ) : (
          <>
            <ShareBar
              label="SSH keys"
              capacity={total}
              rest={0}
              parts={holders.map((u) => ({
                key: u.username,
                value: u.sshKeyCount,
                color: accountHue(u.username),
                label: `${u.username} ${plural(u.sshKeyCount, "key")}`,
              }))}
              format={(value) => plural(value, "key")}
            />
            <ul className="-mx-3">
              {shown.map((user) => (
                <li key={user.username}>
                  <button
                    type="button"
                    onClick={() => onOpen(user.username)}
                    aria-label={`SSH keys for ${user.username}`}
                    className="flex w-full min-w-0 items-center gap-2.5 rounded-sm px-3 py-2 text-left focus-ring transition-colors hover:bg-row-hover"
                  >
                    <span
                      aria-hidden
                      className="h-2.5 w-0.5 shrink-0 rounded-full"
                      style={{ background: accountHue(user.username) }}
                    />
                    <span className="min-w-0 flex-1 truncate font-mono text-body">
                      {user.username}
                    </span>
                    <span className="numeric shrink-0 text-body">{user.sshKeyCount}</span>
                    <span className="numeric w-10 shrink-0 text-right text-hint text-muted-foreground">
                      {Math.round((user.sshKeyCount / total) * 100)}%
                    </span>
                  </button>
                </li>
              ))}
            </ul>
            {holders.length > shown.length && (
              <p className="text-hint text-muted-foreground">
                {holders.length - shown.length} more hold keys
              </p>
            )}
          </>
        )}
      </PanelBody>
    </Panel>
  )
}
