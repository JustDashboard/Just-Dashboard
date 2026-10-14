"use client"

import { plural, relativeTime } from "@/lib/format"
import { LANES, hueFor } from "@/lib/hue"
import type { DashboardUser, Role } from "@/lib/types"
import { useSessionState } from "@/lib/view-state"
import { cn } from "@/lib/utils"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ShareBar } from "@/components/procs/workloads"
import { Status } from "@/components/status-dot"
import { ChipStrip, FilterChip } from "@/components/tabs"
import { NumberTicker } from "@/components/ui/number-ticker"
import { useNow } from "@/components/deploy/vocabulary"
import { ROLE_HUE, ROLE_MARK, ROLE_NAME } from "@/components/account/capabilities"
import {
  ROLES,
  WINDOWS,
  lastSignIns,
  onPasswordAlone,
  placeOn,
  type UserScope,
  type WindowKey,
} from "@/components/account/access"
import { AvatarRun, UserAvatar, displayNameOf } from "@/components/account/user-avatar"

/** How many accounts each block names; the rest are counted under it. */
const SHOWN = 5

/** A sign-in this recent is still happening as far as the page can tell. */
const FRESH = 120_000

/** An account's colour everywhere on the page: its initials' hue. */
function hueOf(user: Pick<DashboardUser, "username">) {
  return hueFor(user.username.toLowerCase(), LANES)
}

/**
 * Who can sign in to this dashboard, as three blocks over the accounts —
 * System users' band, for the dashboard's own people: when each last signed
 * in, on an axis that ends at this second; what guards each one, as one bar
 * of the accounts by whether a code is asked for; and what each may do, as
 * one bar of the accounts by role.
 *
 * It replaced four tiles — accounts, administrators, two-factor, signed in
 * this week — which gave each as a figure and none of which said *who*. Here
 * the people are the picture: each in the hue they have in the rail and on
 * their card, a dot that moves toward the axis's "now" as the clock runs, a
 * ring round a sign-in under two minutes old. A press on a block's row
 * narrows the cards below to the accounts it names.
 */
export function PeopleBand({
  users,
  onScope,
}: {
  users: DashboardUser[]
  onScope: (scope: UserScope) => void
}) {
  return (
    <div
      data-slot="people-band"
      className="grid min-w-0 gap-x-10 gap-y-6 lg:grid-cols-2 xl:grid-cols-3"
    >
      <SignInsBlock users={users} />
      <GuardBlock users={users} onScope={onScope} />
      <RolesBlock users={users} onScope={onScope} />
    </div>
  )
}

function SignInsBlock({ users }: { users: DashboardUser[] }) {
  const now = useNow(15_000)
  const [windowKey, setWindowKey] = useSessionState<WindowKey>("account-users.window", "7d")
  const span = WINDOWS.find((w) => w.key === windowKey) ?? WINDOWS[1]
  const { within, older, never } = lastSignIns(users, now, span.ms)
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
            {shown.map(({ user, at }, index) => {
              const fresh = now - at < FRESH
              const left = placeOn(at, { from: now - span.ms, to: now })
              const color = hueOf(user)
              return (
                <li
                  key={user.id}
                  className={cn("-mx-3 px-3 py-2.5", index > 0 && "border-t border-hairline")}
                >
                  <div className="flex min-w-0 items-center gap-2.5">
                    <UserAvatar user={user} scope="admin" size="xs" />
                    <span className="min-w-0 truncate text-body font-medium">
                      {displayNameOf(user)}
                    </span>
                    <span className="numeric ml-auto shrink-0 text-hint text-muted-foreground">
                      {fresh ? (
                        <span className="text-success">just now</span>
                      ) : (
                        relativeTime(user.lastLoginAt)
                      )}
                    </span>
                  </div>
                  <div
                    role="img"
                    aria-label={`${displayNameOf(user)} signed in ${relativeTime(user.lastLoginAt)}`}
                    className="relative mt-2 h-3"
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
                      className="absolute top-1/2 h-px -translate-y-1/2 transition-[left] duration-700 ease-out"
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
                      <span
                        className="absolute inset-0 rounded-full"
                        style={{ background: color }}
                      />
                    </span>
                  </div>
                </li>
              )
            })}
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
          {rest + older + never === 0 && <span>Every account has signed in.</span>}
        </p>
      </PanelBody>
    </Panel>
  )
}

/**
 * What stands between a stolen password and each account: the accounts that
 * can sign in as one bar, the share asking for a code in green and the share
 * a password alone opens in amber, then the second named, administrators
 * first — those are root on this server for whoever holds the password.
 */
function GuardBlock({
  users,
  onScope,
}: {
  users: DashboardUser[]
  onScope: (scope: UserScope) => void
}) {
  const active = users.filter((u) => !u.disabled)
  const coded = active.filter((u) => u.totpEnabled)
  const bare = active
    .filter(onPasswordAlone)
    .sort(
      (a, b) =>
        ROLES.indexOf(a.role) - ROLES.indexOf(b.role) ||
        displayNameOf(a).localeCompare(displayNameOf(b)),
    )
  const shown = bare.slice(0, SHOWN)

  return (
    <Panel plain aria-label="Two-factor">
      <PanelHeader
        title="Two-factor"
        actions={
          <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
            <span
              className={cn(
                "font-medium",
                bare.length === 0 && active.length > 0 ? "text-success" : "text-foreground",
              )}
            >
              <NumberTicker value={coded.length} />
            </span>
            of {active.length} ask for a code
          </span>
        }
      />
      <PanelBody className="space-y-3 pt-4">
        <ShareBar
          label="Two-factor"
          capacity={active.length}
          rest={0}
          parts={[
            {
              key: "code",
              value: coded.length,
              color: "var(--success)",
              label: `${coded.length} ask for a code`,
            },
            {
              key: "bare",
              value: bare.length,
              color: "var(--warning)",
              label: `${bare.length} on a password alone`,
            },
          ]}
          format={(value) => plural(value, "account")}
        />
        {shown.length === 0 ? (
          <p className="py-1 text-body text-muted-foreground">
            Every account that can sign in asks for a code.
          </p>
        ) : (
          <ul className="-mx-3">
            {shown.map((user) => (
              <li key={user.id}>
                <button
                  type="button"
                  onClick={() => onScope("bare")}
                  aria-label={`Only accounts on a password alone, like ${displayNameOf(user)}`}
                  className="flex w-full min-w-0 items-center gap-2.5 rounded-sm px-3 py-2 text-left focus-ring transition-colors hover:bg-row-hover"
                >
                  <UserAvatar user={user} scope="admin" size="xs" />
                  <span className="min-w-0 flex-1 truncate text-body">{displayNameOf(user)}</span>
                  <Status
                    tone={user.role === "admin" ? "warning" : "notice"}
                    label={user.role === "admin" ? "administrator" : user.role}
                  />
                </button>
              </li>
            ))}
          </ul>
        )}
        {bare.length > shown.length && (
          <p className="text-hint text-muted-foreground">
            {bare.length - shown.length} more on a password alone
          </p>
        )}
      </PanelBody>
    </Panel>
  )
}

/** What each account may do, as one bar of the accounts by role in the ladder's hues. */
function RolesBlock({
  users,
  onScope,
}: {
  users: DashboardUser[]
  onScope: (scope: UserScope) => void
}) {
  const byRole = ROLES.map((role) => ({
    role,
    users: users.filter((u) => u.role === role),
  }))

  return (
    <Panel plain aria-label="Roles" className="lg:max-xl:col-span-2">
      <PanelHeader
        title="Roles"
        actions={
          <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
            <span className="font-medium text-foreground">
              <NumberTicker value={users.length} />
            </span>
            {users.length === 1 ? "account" : "accounts"}
          </span>
        }
      />
      <PanelBody className="space-y-3 pt-4">
        <ShareBar
          label="Roles"
          capacity={users.length}
          rest={0}
          parts={byRole.map(({ role, users: held }) => ({
            key: role,
            value: held.length,
            color: ROLE_HUE[role],
            label: `${plural(held.length, ROLE_NAME[role].toLowerCase())}`,
          }))}
          format={(value) => plural(value, "account")}
        />
        <ul className="-mx-3">
          {byRole.map(({ role, users: held }) => (
            <li key={role}>
              <RoleRow role={role} users={held} onPress={() => onScope(role)} />
            </li>
          ))}
        </ul>
      </PanelBody>
    </Panel>
  )
}

function RoleRow({
  role,
  users,
  onPress,
}: {
  role: Role
  users: DashboardUser[]
  onPress: () => void
}) {
  const Mark = ROLE_MARK[role]
  return (
    <button
      type="button"
      onClick={onPress}
      disabled={users.length === 0}
      aria-label={`Only ${ROLE_NAME[role].toLowerCase()} accounts`}
      className="flex w-full min-w-0 items-center gap-2.5 rounded-sm px-3 py-2 text-left focus-ring transition-colors enabled:hover:bg-row-hover disabled:opacity-50"
    >
      <span
        aria-hidden
        className="h-2.5 w-0.5 shrink-0 rounded-full"
        style={{ background: ROLE_HUE[role] }}
      />
      <Mark aria-hidden className="size-3.5 shrink-0" style={{ color: ROLE_HUE[role] }} />
      <span className="min-w-0 flex-1 truncate text-body">{ROLE_NAME[role]}</span>
      <AvatarRun users={users} />
      <span className="numeric w-6 shrink-0 text-right text-body">{users.length}</span>
    </button>
  )
}
