"use client"

import { useState } from "react"
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
import { StatGrid, StatTile } from "@/components/stat-tile"
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
import { ROLE_MARK } from "@/components/account/capabilities"
import { RoleChoice } from "@/components/account/role-choice"
import { UserAvatar, displayNameOf } from "@/components/account/user-avatar"

export function useDashboardUsers(enabled = true) {
  return usePoll(
    (signal) => get<DashboardUser[]>("/dashboard-users/", undefined, signal),
    30000,
    [],
    { enabled },
  )
}

const ROLES: Role[] = ["admin", "limited", "readonly"]

function neverSignedIn(user: DashboardUser) {
  return user.lastLoginAt.startsWith("0001")
}

function signedInThisWeek(user: DashboardUser) {
  return !neverSignedIn(user) && Date.now() - new Date(user.lastLoginAt).getTime() < 7 * 86_400_000
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
        <Mark aria-hidden className="size-3.5 text-muted-foreground" />
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
 * The accounts in four readings: how many there are and how many of them can
 * get in, who holds everything, how many of them a stolen password alone
 * would open — amber while an administrator is one of those — and who has
 * been here this week.
 */
function UserReadings({ users }: { users: DashboardUser[] }) {
  const active = users.filter((u) => !u.disabled)
  const admins = active.filter((u) => u.role === "admin")
  const enrolled = active.filter((u) => u.totpEnabled)
  const bareAdmins = admins.filter((u) => !u.totpEnabled)
  const week = users
    .filter(signedInThisWeek)
    .sort((a, b) => b.lastLoginAt.localeCompare(a.lastLoginAt))

  return (
    <StatGrid columns={4}>
      <StatTile
        label="Accounts"
        value={users.length}
        hint={
          users.length === active.length
            ? "every one can sign in"
            : `${active.length} can sign in · ${users.length - active.length} disabled`
        }
      />
      <StatTile
        label="Administrators"
        value={admins.length}
        hint={
          <span className="inline-flex max-w-full min-w-0 items-center gap-2">
            <AvatarRun users={admins} />
            <span className="truncate">
              {admins.length === 1 ? displayNameOf(admins[0]) : "hold every capability"}
            </span>
          </span>
        }
      />
      <StatTile
        label="Two-factor"
        value={`${enrolled.length} of ${active.length}`}
        tone={
          bareAdmins.length > 0
            ? "warning"
            : enrolled.length === active.length
              ? "success"
              : "default"
        }
        hint={
          bareAdmins.length > 0
            ? `${plural(bareAdmins.length, "administrator")} on a password alone`
            : enrolled.length === active.length
              ? "every account asks for a code"
              : `${active.length - enrolled.length} on a password alone`
        }
      />
      <StatTile
        label="Signed in this week"
        value={week.length}
        hint={
          week.length > 0
            ? `${displayNameOf(week[0])} ${relativeTime(week[0].lastLoginAt)}`
            : "nobody yet"
        }
      />
    </StatGrid>
  )
}

/** Who a reading counts, drawn as them: a few faces in a row, then how many more. */
export function AvatarRun({ users, max = 4 }: { users: DashboardUser[]; max?: number }) {
  if (users.length === 0) return null
  return (
    <span aria-hidden className="flex shrink-0 items-center gap-0.5">
      {users.slice(0, max).map((user) => (
        <UserAvatar key={user.id} user={user} scope="admin" size="xs" />
      ))}
      {users.length > max && (
        <span className="numeric ml-0.5 text-micro text-muted-foreground">
          +{users.length - max}
        </span>
      )}
    </span>
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
  onEdit,
  onUpdate,
  verbs,
}: {
  user: DashboardUser
  me: boolean
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
      <span className="flex w-28">
        <Status
          tone={user.totpEnabled ? "running" : user.role === "admin" ? "warning" : "notice"}
          label={user.totpEnabled ? "two-factor on" : "password only"}
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

/**
 * Every dashboard account, with the two things an admin changes daily — the
 * role and whether it can sign in — on each card, and everything rarer
 * behind the card's menu: a rename or password reset in the editor, a 2FA
 * reset, deletion with the name typed.
 */
export function DashboardUsersView({ users }: { users: ReturnType<typeof useDashboardUsers> }) {
  const { status, refresh: refreshAuth } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [editing, setEditing] = useState<DashboardUser | null>(null)
  const { data, error, loading, refresh } = users

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
        detail: "Rename the account or give it a new password.",
        icon: Pencil,
        run: () => setEditing(user),
      },
      ...(user.totpEnabled
        ? [
            {
              key: "reset-totp",
              label: "Reset two-factor",
              detail:
                "For a lost authenticator: they sign in with a password until they enrol again.",
              icon: Fingerprint,
              run: () =>
                confirm({
                  title: "Reset two-factor",
                  confirmLabel: "Reset",
                  description: (
                    <p>
                      <b>{name}</b> signs in with a password alone until they enrol an authenticator
                      again. Do this when they have lost theirs and their recovery codes.
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
              detail: "They lose access at once, with every session and API key they hold.",
              icon: Trash,
              danger: true,
              run: () =>
                confirm({
                  title: "Delete dashboard user",
                  phrase: user.username,
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
    return <EmptyState icon={Users} title="No dashboard users" />
  }

  // Administrators first, then the rest by name: the accounts that can do
  // the most are the ones an administrator reads this list for.
  const ordered = [...data].sort(
    (a, b) =>
      ROLES.indexOf(a.role) - ROLES.indexOf(b.role) ||
      displayNameOf(a).localeCompare(displayNameOf(b)),
  )

  return (
    <>
      <UserReadings users={data} />
      <Panel plain>
        <PanelHeader
          title="Accounts"
          actions={
            <span className="numeric text-hint text-muted-foreground">
              {plural(data.length, "account")}
            </span>
          }
        />
        <PanelBody flush className="pt-3">
          <ChoiceList className="animate-rise">
            {ordered.map((user) => (
              <UserCard
                key={user.id}
                user={user}
                me={user.id === status?.user?.id}
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
