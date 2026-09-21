"use client"

import { useState } from "react"
import { Pencil, Trash, UserPlus } from "@/components/icons"
import { del, get, patch, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { relativeTime } from "@/lib/format"
import type { DashboardUser, Role } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { Field, FieldRow, FormNote } from "@/components/form"
import { RowActions } from "@/components/icon-action"
import { Modal } from "@/components/modal"
import { Panel, PanelBody } from "@/components/panel"
import { ErrorState, LoadingPanel } from "@/components/state"
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { ROLE_SUMMARY } from "@/components/account/capabilities"
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

function RolePicker({
  value,
  onChange,
  size,
}: {
  value: Role
  onChange: (role: Role) => void
  size?: "sm"
}) {
  return (
    <Select value={value} onValueChange={(v) => onChange(v as Role)}>
      <SelectTrigger size={size} className={size ? "w-28 text-xs" : "w-full"}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {ROLES.map((r) => (
          <SelectItem key={r} value={r}>
            {size ? r : `${r} — ${ROLE_SUMMARY[r]}`}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

/**
 * Every dashboard account, with the two things an admin changes daily — the
 * role and whether it can sign in — inline, and everything rarer behind the
 * row's actions: a rename or password reset in a dialog, a 2FA reset, deletion
 * with the name typed.
 */
export function DashboardUsersTable({
  users,
}: {
  users: ReturnType<typeof useDashboardUsers>
}) {
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

  if (loading && !data) return <LoadingPanel rows={3} />
  if (error) return <ErrorState error={error} />

  return (
    <>
      <Panel>
        <PanelBody flush>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-full">User</TableHead>
                <TableHead>Role</TableHead>
                <TableHead>Two-factor</TableHead>
                <TableHead>Last sign-in</TableHead>
                <TableHead className="w-24">Can sign in</TableHead>
                <TableHead className="w-px" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {data?.map((user) => {
                const me = user.id === status?.user?.id
                return (
                  <TableRow key={user.id} className="group">
                    <TableCell>
                      <span className="flex min-w-0 items-center gap-3">
                        <UserAvatar user={user} scope="admin" size="md" />
                        <span className="min-w-0">
                          <span className="flex min-w-0 items-center gap-2">
                            <span className="truncate text-body font-medium">
                              {displayNameOf(user)}
                            </span>
                            {me && <Tag>you</Tag>}
                          </span>
                          <span className="block truncate text-hint text-muted-foreground">
                            @{user.username}
                            {user.mustChangePassword && " · must change password at next sign-in"}
                          </span>
                        </span>
                      </span>
                    </TableCell>
                    <TableCell>
                      <RolePicker
                        size="sm"
                        value={user.role}
                        onChange={(role) => update(user, { role })}
                      />
                    </TableCell>
                    <TableCell>
                      <Status
                        tone={user.totpEnabled ? "running" : "notice"}
                        label={user.totpEnabled ? "on" : "off"}
                      />
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {user.lastLoginAt.startsWith("0001") ? "never" : relativeTime(user.lastLoginAt)}
                    </TableCell>
                    <TableCell>
                      <Switch
                        checked={!user.disabled}
                        disabled={me}
                        aria-label={`${displayNameOf(user)} can sign in`}
                        onCheckedChange={(v) => update(user, { disabled: !v })}
                      />
                    </TableCell>
                    <TableCell>
                      <RowActions className="gap-1">
                        <Button
                          size="icon-xs"
                          variant="ghost"
                          aria-label={`Edit ${displayNameOf(user)}`}
                          onClick={() => setEditing(user)}
                        >
                          <Pencil />
                        </Button>
                        {user.totpEnabled && (
                          <Button
                            size="xs"
                            variant="ghost"
                            onClick={() =>
                              confirm({
                                title: "Reset two-factor",
                                confirmLabel: "Reset",
                                description: (
                                  <p>
                                    <b>{displayNameOf(user)}</b> signs in with a password alone until
                                    they enrol an authenticator again. Do this when they have lost
                                    theirs and their recovery codes.
                                  </p>
                                ),
                                action: async () => {
                                  await post(`/dashboard-users/${user.id}/reset-totp`)
                                  notify.success(`Two-factor reset for ${displayNameOf(user)}`)
                                  refresh()
                                },
                              })
                            }
                          >
                            Reset 2FA
                          </Button>
                        )}
                        {!me && (
                          <Button
                            size="icon-xs"
                            variant="ghost"
                            aria-label={`Delete ${displayNameOf(user)}`}
                            className="text-destructive"
                            onClick={() =>
                              confirm({
                                title: "Delete dashboard user",
                                phrase: user.username,
                                confirmLabel: "Delete",
                                description: (
                                  <p>
                                    <b>{displayNameOf(user)}</b> loses access immediately, along with
                                    every session and API key they hold.
                                  </p>
                                ),
                                action: async (c) => {
                                  await del(`/dashboard-users/${user.id}`, { confirm: c })
                                  refresh()
                                },
                              })
                            }
                          >
                            <Trash />
                          </Button>
                        )}
                      </RowActions>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
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
        <Button onClick={save} disabled={busy || !displayName.trim() || !username.trim()} pending={busy}>
          Save
        </Button>
      }
    >
      <div className="space-y-4">
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
        size="sm"
        title="New dashboard user"
        description="An account for somebody else to sign in to this dashboard."
        footer={
          <Button onClick={create} disabled={!username.trim() || !password || busy} pending={busy}>
            Create user
          </Button>
        }
      >
        <div className="space-y-4">
          <FieldRow>
            <Field label="Name" htmlFor="du-display" hint="How they appear. Defaults to the username.">
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
          <Field label="Role" hint={ROLE_SUMMARY[role]}>
            <RolePicker value={role} onChange={setRole} />
          </Field>
          <FormNote>
            Whether they must also enrol an authenticator is decided by this install&apos;s
            two-factor policy, under Settings → Configuration.
          </FormNote>
        </div>
      </Modal>
    </>
  )
}
