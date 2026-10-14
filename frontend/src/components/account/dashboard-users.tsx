"use client"

import { useRef, useState } from "react"
import { Fingerprint, Pencil, Trash, UserPlus, Users } from "@/components/icons"
import { del, get, patch, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { plural, relativeTime } from "@/lib/format"
import type { DashboardUser, Role } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Field, FieldRow, FormNote } from "@/components/form"
import { DimActions } from "@/components/icon-action"
import { Modal } from "@/components/modal"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { VerbMenu, type Verb } from "@/components/verbs"
import { ROLE_HUE, ROLE_MARK } from "@/components/account/capabilities"
import {
  ROLES,
  inUserScope,
  neverSignedIn,
  peopleVerdict,
  type UserScope,
} from "@/components/account/access"
import { PeopleBand } from "@/components/account/people-band"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { cn } from "@/lib/utils"
import { RoleChoice } from "@/components/account/role-choice"
import { AvatarRun, UserAvatar, displayNameOf } from "@/components/account/user-avatar"

export function useDashboardUsers(enabled = true) {
  return usePoll(
    (signal) => get<DashboardUser[]>("/dashboard-users/", undefined, signal),
    30000,
    [],
    { enabled },
  )
}

/** The role, changed where it is read: each option drawn with its mark. */
function RolePicker({
  label,
  value,
  onChange,
}: {
  label: string
  value: Role
  onChange: (role: Role) => void
}) {
  const Mark = ROLE_MARK[value]
  return (
    <Select value={value} onValueChange={(v) => onChange(v as Role)}>
      <SelectTrigger size="sm" className="w-32 text-xs" aria-label={label}>
        <Mark aria-hidden className="size-3.5" style={{ color: ROLE_HUE[value] }} />
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {ROLES.map((r) => (
          <SelectItem key={r} value={r}>
            {r}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

/**
 * One account, as the person it is: their picture (or their initials in
 * their own hue), their name, and the three readings an administrator
 * compares down the list — what they may do, whether a code guards them, and
 * when they were last here. The role and whether they can sign in are
 * changed where they are read; the card opens the account's editor, and the
 * rarer verbs are in its menu.
 *
 * A card rather than a table row because each one is a destination — the
 * editor — and the Docker lists and the backup jobs draw theirs the same way
 * (§12, §16). From `lg` the readings sit beside the name; below it they go
 * beneath it at the card's full width, chosen once rather than drawn twice.
 */
function UserCard({
  user,
  me,
  require2fa,
  onEdit,
  onUpdate,
  verbs,
}: {
  user: DashboardUser
  me: boolean
  require2fa: boolean
  onEdit: () => void
  onUpdate: (body: Record<string, unknown>) => void
  verbs: Verb[]
}) {
  const wide = useMediaQuery("(min-width: 1024px)")
  const name = displayNameOf(user)
  const readings = (
    <span className="flex min-w-0 flex-wrap items-center gap-x-5 gap-y-2">
      {/* The options open in a portal, and React carries a press on one back
          up through the card, which would open the editor behind the menu. */}
      <span onClick={(event) => event.stopPropagation()}>
        <RolePicker
          label={`Role of ${name}`}
          value={user.role}
          onChange={(role) => onUpdate({ role })}
        />
      </span>
      <span className="flex w-36">
        <Status
          tone={user.totpEnabled ? "running" : user.role === "admin" ? "warning" : "notice"}
          label={
            user.totpEnabled ? "two-factor on" : require2fa ? "enrolment required" : "password only"
          }
        />
      </span>
      <span className="w-36 truncate text-xs text-muted-foreground">
        {neverSignedIn(user) ? "never signed in" : `signed in ${relativeTime(user.lastLoginAt)}`}
      </span>
    </span>
  )

  return (
    <ChoiceRow
      verb={`Edit ${name}`}
      onSelect={onEdit}
      className={user.disabled ? "opacity-60" : undefined}
      leading={<UserAvatar user={user} scope="admin" size="md" />}
      title={
        <span className="inline-flex max-w-full min-w-0 items-center gap-2">
          <span className="truncate">{name}</span>
          {me && <Tag>you</Tag>}
        </span>
      }
      description={
        <>
          @{user.username}
          {user.disabled && " · cannot sign in"}
          {user.mustChangePassword && " · chooses a new password at next sign-in"}
        </>
      }
      trailing={wide ? readings : undefined}
      actions={
        <DimActions className="gap-2">
          <Switch
            checked={!user.disabled}
            disabled={me}
            aria-label={`${name} can sign in`}
            onCheckedChange={(v) => onUpdate({ disabled: !v })}
          />
          <VerbMenu verbs={verbs} label={`More actions for ${name}`} />
        </DimActions>
      }
    >
      {!wide && <div className="pl-12">{readings}</div>}
    </ChoiceRow>
  )
}

/** The chips over the cards, each a narrowing and the tone its count takes while it is not zero. */
const SCOPES: { key: UserScope; label: string; warn?: boolean }[] = [
  { key: "all", label: "All" },
  { key: "admin", label: "Administrators" },
  { key: "limited", label: "Limited" },
  { key: "readonly", label: "Read-only" },
  { key: "bare", label: "Password only", warn: true },
  { key: "never", label: "Never signed in" },
  { key: "disabled", label: "Disabled" },
]

/**
 * The accounts as one identity line, the shape the Overview gives the machine
 * and System users gives the host's people: their faces where the tile would
 * be, how many, who can sign in and who administers, and the last sign-in
 * among the facts; at the right end the verdict — how many accounts a
 * password alone opens and whether an administrator is one of them, a press
 * of which narrows the cards to exactly those — beside New user.
 */
function PeopleIdentity({
  users,
  require2fa,
  onScope,
  action,
}: {
  users: DashboardUser[]
  require2fa: boolean
  onScope: (scope: UserScope) => void
  action: React.ReactNode
}) {
  const active = users.filter((u) => !u.disabled)
  const admins = active.filter((u) => u.role === "admin")
  const latest = users
    .filter((u) => !neverSignedIn(u))
    .sort((a, b) => b.lastLoginAt.localeCompare(a.lastLoginAt))[0]
  const verdict = peopleVerdict(users, require2fa)
  return (
    <HostIdentity
      logo={<FaceStack users={[...admins, ...active.filter((u) => u.role !== "admin")]} />}
      title={plural(users.length, "account")}
      facts={
        <>
          <span>
            {require2fa
              ? plural(active.length, "enabled account")
              : active.length === users.length
                ? "every one can sign in"
                : `${active.length} can sign in`}
          </span>
          <FactDot />
          <span className="inline-flex items-center gap-1.5">
            <AvatarRun users={admins} />
            {plural(admins.length, "administrator")}
          </span>
          {latest && (
            <>
              <FactDot />
              <span>
                last sign-in {displayNameOf(latest)} {relativeTime(latest.lastLoginAt)}
              </span>
            </>
          )}
        </>
      }
      aside={
        <span className="flex flex-wrap items-center gap-x-4 gap-y-2">
          {verdict.bare > 0 ? (
            <button
              type="button"
              onClick={() => onScope("bare")}
              className="rounded-sm focus-ring"
              aria-label={
                require2fa
                  ? "Only accounts requiring enrollment"
                  : "Only the accounts a password alone opens"
              }
            >
              <Status
                className="text-body"
                tone={verdict.tone}
                label={`${plural(verdict.admins > 0 ? verdict.admins : verdict.bare, verdict.admins > 0 ? "administrator" : "account")} ${require2fa ? "must enrol before signing in" : "on a password alone"}`}
              />
            </button>
          ) : (
            <Status className="text-body" verdict="ok" label="every account asks for a code" />
          )}
          {action}
        </span>
      }
    />
  )
}

/**
 * The people a line is about, as their faces overlapping on the identity
 * line's tile — the first whole, each after it tucked under — so the users
 * page opens on who rather than on a glyph for "users".
 */
function FaceStack({ users }: { users: DashboardUser[] }) {
  const shown = users.slice(0, 3)
  return (
    <span aria-hidden className="flex h-12 shrink-0 items-center">
      {shown.map((user, index) => (
        <span
          key={user.id}
          className={cn("relative flex rounded-full ring-2 ring-background", index > 0 && "-ml-3")}
          style={{ zIndex: shown.length - index }}
        >
          <UserAvatar user={user} scope="admin" size="md" />
        </span>
      ))}
    </span>
  )
}

/**
 * Every dashboard account, with the two things an admin changes daily — the
 * role and whether it can sign in — on each card, and everything rarer
 * behind the card's menu: a rename or password reset in the editor, a 2FA
 * reset, and deletion with ordinary confirmation.
 */
export function DashboardUsersView({ users }: { users: ReturnType<typeof useDashboardUsers> }) {
  const { status, refresh: refreshAuth } = useAuth()
  const require2fa = Boolean(status?.require2fa)
  const { confirm, dialog } = useConfirm()
  const [editing, setEditing] = useState<DashboardUser | null>(null)
  const [scope, setScope] = useState<UserScope>("all")
  const list = useRef<HTMLDivElement>(null)
  const { data, error, loading, refresh } = users

  // A press in the band narrows the cards and brings them into view, as a
  // verdict's press does on the System users page.
  const narrow = (next: UserScope) => {
    setScope(next)
    list.current?.scrollIntoView({ behavior: "smooth", block: "start" })
  }

  const update = async (user: DashboardUser, body: Record<string, unknown>) => {
    try {
      await patch(`/dashboard-users/${user.id}`, body)
      notify.success(`${displayNameOf(user)} updated`)
      refresh()
      if (user.id === status?.user?.id) refreshAuth()
    } catch (err) {
      notify.error("Could not update", err)
    }
  }

  const verbsFor = (user: DashboardUser): Verb[] => {
    const me = user.id === status?.user?.id
    const name = displayNameOf(user)
    return [
      {
        key: "edit",
        label: "Edit",
        icon: Pencil,
        run: () => setEditing(user),
      },
      ...(user.totpEnabled
        ? [
            {
              key: "reset-totp",
              label: "Reset two-factor",
              icon: Fingerprint,
              run: () =>
                confirm({
                  title: "Reset two-factor",
                  confirmLabel: "Reset",
                  description: (
                    <p>
                      <b>{name}</b>{" "}
                      {require2fa
                        ? "must enrol an authenticator again before completing sign-in."
                        : "signs in with a password alone until they enrol an authenticator again."}{" "}
                      Do this when they have lost theirs and their recovery codes.
                    </p>
                  ),
                  action: async () => {
                    await post(`/dashboard-users/${user.id}/reset-totp`)
                    notify.success(`Two-factor reset for ${name}`)
                    refresh()
                  },
                }),
            },
          ]
        : []),
      ...(me
        ? []
        : [
            {
              key: "delete",
              label: "Delete",
              icon: Trash,
              danger: true,
              run: () =>
                confirm({
                  title: "Delete dashboard user",
                  confirmLabel: "Delete",
                  description: (
                    <p>
                      <b>{name}</b> loses access immediately, along with every session and API key
                      they hold.
                    </p>
                  ),
                  action: async (c) => {
                    await del(`/dashboard-users/${user.id}`, { confirm: c })
                    refresh()
                  },
                }),
            },
          ]),
    ]
  }

  if (loading && !data) return <LoadingRows rows={3} />
  if (error && !data) return <ErrorState error={error} onRetry={refresh} />
  if (!data) return null
  if (data.length === 0) {
    return (
      <EmptyState
        icon={Users}
        title="No dashboard users"
        action={<CreateDashboardUserDialog onDone={refresh} />}
      />
    )
  }

  // Administrators first, then the rest by name: the accounts that can do
  // the most are the ones an administrator reads this list for.
  const ordered = [...data].sort(
    (a, b) =>
      ROLES.indexOf(a.role) - ROLES.indexOf(b.role) ||
      displayNameOf(a).localeCompare(displayNameOf(b)),
  )

  const shown = ordered.filter((u) => inUserScope(u, scope))
  const chips = SCOPES.map((chip) => ({
    ...chip,
    label: chip.key === "bare" && require2fa ? "Enrollment required" : chip.label,
    count: data.filter((u) => inUserScope(u, chip.key)).length,
  })).filter((chip) => chip.key === "all" || chip.count > 0)

  return (
    <>
      <PeopleIdentity
        users={data}
        require2fa={require2fa}
        onScope={narrow}
        action={<CreateDashboardUserDialog onDone={refresh} />}
      />
      <PeopleBand users={data} require2fa={require2fa} onScope={narrow} />
      <Panel plain ref={list} className="scroll-mt-6">
        <PanelHeader
          title="Accounts"
          actions={
            <span className="numeric text-hint text-muted-foreground">
              {shown.length === data.length
                ? plural(data.length, "account")
                : `${shown.length} of ${data.length}`}
            </span>
          }
        />
        <PanelBody flush className="space-y-3 pt-3">
          <ChipStrip role="group" aria-label="Show">
            {chips.map(({ key, label, warn, count }) => (
              <FilterChip
                key={key}
                selected={scope === key}
                onClick={() => setScope(scope === key && key !== "all" ? "all" : key)}
              >
                {label}
                <ChipCount className={cn(warn && "text-warning opacity-100")}>{count}</ChipCount>
              </FilterChip>
            ))}
          </ChipStrip>
          <ChoiceList className="animate-rise">
            {shown.map((user) => (
              <UserCard
                key={user.id}
                user={user}
                me={user.id === status?.user?.id}
                require2fa={require2fa}
                onEdit={() => setEditing(user)}
                onUpdate={(body) => update(user, body)}
                verbs={verbsFor(user)}
              />
            ))}
          </ChoiceList>
        </PanelBody>
      </Panel>
      {editing && (
        <EditUserDialog
          user={editing}
          onClose={() => setEditing(null)}
          onDone={() => {
            refresh()
            if (editing.id === status?.user?.id) refreshAuth()
          }}
        />
      )}
      {dialog}
    </>
  )
}

function EditUserDialog({
  user,
  onClose,
  onDone,
}: {
  user: DashboardUser
  onClose: () => void
  onDone: () => void
}) {
  const [displayName, setDisplayName] = useState(displayNameOf(user))
  const [username, setUsername] = useState(user.username)
  const [password, setPassword] = useState("")
  const [busy, setBusy] = useState(false)

  const save = async () => {
    setBusy(true)
    try {
      const body: Record<string, string> = {}
      if (displayName.trim() !== displayNameOf(user)) body.displayName = displayName.trim()
      if (username.trim() !== user.username) body.username = username.trim()
      if (password) body.password = password
      if (Object.keys(body).length > 0) {
        await patch(`/dashboard-users/${user.id}`, body)
        notify.success(`${displayName.trim() || displayNameOf(user)} updated`, {
          description: password ? "Their sessions were signed out." : undefined,
        })
        onDone()
      }
      onClose()
    } catch (err) {
      notify.error("Could not update", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      size="sm"
      title={`Edit ${displayNameOf(user)}`}
      description="Rename the account or give it a new password."
      footer={
        <Button
          onClick={save}
          disabled={busy || !displayName.trim() || !username.trim()}
          pending={busy}
        >
          Save
        </Button>
      }
    >
      <div className="space-y-4">
        <div className="flex min-w-0 items-center gap-3">
          <UserAvatar user={user} scope="admin" size="lg" />
          <div className="min-w-0">
            <p className="truncate text-body font-medium">{displayNameOf(user)}</p>
            <p className="truncate text-hint text-muted-foreground">
              @{user.username} · {user.role}
            </p>
          </div>
        </div>
        <Field label="Name" htmlFor="edit-name">
          <Input
            id="edit-name"
            value={displayName}
            maxLength={64}
            onChange={(e) => setDisplayName(e.target.value)}
          />
        </Field>
        <Field
          label="Username"
          htmlFor="edit-username"
          hint="What they sign in with. One word; earlier audit entries keep the old name."
        >
          <Input
            id="edit-username"
            value={username}
            maxLength={64}
            autoCapitalize="none"
            autoCorrect="off"
            spellCheck={false}
            onChange={(e) => setUsername(e.target.value)}
          />
        </Field>
        <Field
          label="New password"
          htmlFor="edit-password"
          hint="Leave blank to keep the current one. Setting it signs them out everywhere."
        >
          <Input
            id="edit-password"
            type="password"
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </Field>
      </div>
    </Modal>
  )
}

export function CreateDashboardUserDialog({ onDone }: { onDone: () => void }) {
  const [open, setOpen] = useState(false)
  const [displayName, setDisplayName] = useState("")
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [role, setRole] = useState<Role>("readonly")
  const [busy, setBusy] = useState(false)

  const reset = () => {
    setDisplayName("")
    setUsername("")
    setPassword("")
    setRole("readonly")
  }

  const create = async () => {
    setBusy(true)
    try {
      await post("/dashboard-users/", {
        username: username.trim(),
        displayName: displayName.trim(),
        password,
        role,
      })
      notify.success(`Created ${displayName.trim() || username.trim()}`, {
        description: "They must choose their own password at first sign-in.",
      })
      setOpen(false)
      reset()
      onDone()
    } catch (err) {
      notify.error("Could not create user", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <Button size="sm" onClick={() => setOpen(true)}>
        <UserPlus className="size-4" />
        New user
      </Button>
      <Modal
        open={open}
        onOpenChange={(o) => {
          setOpen(o)
          if (!o) reset()
        }}
        title="New dashboard user"
        description="An account for somebody else to sign in to this dashboard."
        footer={
          <Button onClick={create} disabled={!username.trim() || !password || busy} pending={busy}>
            Create user
          </Button>
        }
      >
        <div className="space-y-5">
          <FieldRow>
            <Field
              label="Name"
              htmlFor="du-display"
              hint="How they appear. Defaults to the username."
            >
              <Input
                id="du-display"
                value={displayName}
                maxLength={64}
                onChange={(e) => setDisplayName(e.target.value)}
              />
            </Field>
            <Field label="Username" htmlFor="du-name" hint="What they sign in with. One word.">
              <Input
                id="du-name"
                value={username}
                maxLength={64}
                autoCapitalize="none"
                autoCorrect="off"
                spellCheck={false}
                onChange={(e) => setUsername(e.target.value)}
              />
            </Field>
          </FieldRow>
          <Field
            label="Temporary password"
            htmlFor="du-pw"
            hint="At least 12 characters, mixing three character classes. They replace it at first sign-in."
          >
            <Input
              id="du-pw"
              type="password"
              autoComplete="new-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </Field>
          <RoleChoice label="Role" value={role} onChange={setRole} />
          <FormNote>
            Whether they must also enrol an authenticator is decided by this install&apos;s
            two-factor policy, under Settings → Configuration.
          </FormNote>
        </div>
      </Modal>
    </>
  )
}
