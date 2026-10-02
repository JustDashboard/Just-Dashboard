"use client"

import { useId, useMemo, useState } from "react"
import Link from "next/link"
import { Cross, Key, Plus, Trash, UserPlus } from "@/components/icons"
import { del, errorMessage, post, put } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm, type ConfirmRequest } from "@/components/confirm-dialog"
import { useColumnWidth } from "@/components/deploy/settings/use-column-width"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import {
  Disclosure,
  Field,
  FieldRow,
  FormFact,
  FormFacts,
  FormNote,
  FormSection,
} from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Panel, PanelBody } from "@/components/panel"
import { SidePanel } from "@/components/side-panel"
import { LoadingRows, Notice } from "@/components/state"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { ChipStrip, FilterChip, tabClasses } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { VerbBar, VerbMenu, type Verb } from "@/components/verbs"
import { generatePassword } from "@/components/database/connect/rules"
import {
  CardsSkeleton,
  CouldNotRead,
  NotUpdating,
  staleOf,
} from "@/components/database/home/blocks"
import { nameHue } from "@/components/database/home/kinds"
import { read } from "@/components/database/home/read"
import { ProtectedTag, SectionFrame } from "@/components/database/kit"
import {
  accountDsn,
  accountNameProblem,
  roleWord,
  sameRole,
} from "@/components/database/ops/access-model"
import {
  AccountMark,
  AccountName,
  SecretField,
  SecretShown,
} from "@/components/database/ops/access-parts"
import type {
  MongoRoleRef,
  MongoRoles,
  MongoUser,
  MongoUsers,
} from "@/components/database/ops/access-types"
import { ServerDown, isDown } from "@/components/database/ops/performance-parts"
import { TaskDialog, databaseSubject } from "@/components/database/ops/settings-dialog"
import { useDatabase } from "@/components/database/shell/database-context"

const BESIDE_FROM = 640

/** Where the accounts and roles routes are asked about one database's roles. */
function useRoles(database: string, enabled: boolean) {
  const { id } = useDatabase()
  return usePoll(
    (signal) =>
      read<MongoRoles>(
        `/databases/${id}/mongo/roles`,
        (answer) => Array.isArray(answer.roles),
        { database },
        signal,
      ),
    0,
    [id, database],
    { enabled: enabled && Boolean(database) },
  )
}

/** The databases of the server, for where a role applies. `admin` is always one of them. */
function useDatabaseNames(enabled: boolean) {
  const { id, conn } = useDatabase()
  const list = usePoll(
    (signal) =>
      read<{ name: string }[]>(
        `/databases/${id}/schemas`,
        (answer) => Array.isArray(answer),
        undefined,
        signal,
      ),
    0,
    [id],
    { enabled },
  )
  return useMemo(() => {
    const names = new Set(["admin", ...(conn.database ? [conn.database] : [])])
    for (const one of list.data ?? []) {
      if (one.name !== "config" && one.name !== "local") names.add(one.name)
    }
    return [...names]
  }, [list.data, conn.database])
}

function userTags(user: MongoUser) {
  return (
    <>
      {user.self && <Tag>this connection</Tag>}
      {user.superuser && <Tag>administrator</Tag>}
    </>
  )
}

/**
 * A document database's accounts: the users, each living in a database and
 * holding roles on databases, and the roles themselves.
 *
 * Two views of one page (`?view=`). Users is the list — a user's initials in
 * its name's hue, the roles it holds, whether it is an administrator — and
 * opening one puts its roles in a panel, where a role is granted or taken
 * away. Roles is what a role is: the ones the server is built with, and any
 * made here, with what each inherits and may do.
 *
 * A server that lists no user at all is one that asks nobody who they are,
 * and the page says that rather than "empty".
 */
export function MongoAccess() {
  const { id, conn, readOnly, status, param, select, goto } = useDatabase()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const down = isDown(status.state)
  const view = param("view") === "roles" ? "roles" : "users"
  const opened = param("account")
  const openedDb = param("host")
  const [creating, setCreating] = useState(false)
  const [password, setPassword] = useState<MongoUser>()
  const [frame, width] = useColumnWidth<HTMLDivElement>()
  const users = usePoll(
    (signal) =>
      read<MongoUsers>(
        `/databases/${id}/mongo/users`,
        (answer) => Array.isArray(answer.users),
        undefined,
        signal,
      ),
    30_000,
    [id],
    { enabled: !down },
  )
  const list = useMemo(() => users.data?.users ?? [], [users.data])
  const mayEdit = can("system.admin") && !readOnly
  const mayDrop = mayEdit && can("destructive")
  const beside = width >= BESIDE_FROM

  if (down) {
    return (
      <SectionFrame section="access">
        <ServerDown what="Its users" />
      </SectionFrame>
    )
  }

  const dropRequest = (user: MongoUser, after?: () => void): ConfirmRequest => ({
    title: "Drop user",
    confirmLabel: "Drop",
    subject: {
      mark: <AccountMark name={user.user} />,
      name: <AccountName name={user.user} host={user.db} className="text-body" />,
      facts: (
        <FormFact label="Holds">
          {user.roles.length > 0 ? user.roles.map(roleWord).join(", ") : "no role"}
        </FormFact>
      ),
    },
    description: (
      <p>
        Removes the user from <span className="font-mono">{user.db}</span>. Whatever signs in with
        it stops signing in.
      </p>
    ),
    action: async () => {
      await del(`/databases/${id}/mongo/users`, { body: { database: user.db, user: user.user } })
      notify.success(`Dropped ${user.user}`)
      users.refresh()
      after?.()
      return "reported"
    },
  })

  const data = users.data
  const admins = list.filter((user) => user.superuser).length
  const homes = new Set(list.map((user) => user.db)).size
  const figure = (value: number) =>
    data ? (
      <span key="value" className="animate-rise">
        {value}
      </span>
    ) : users.error ? (
      <span className="text-muted-foreground">—</span>
    ) : (
      <Skeleton className="my-1 h-6 w-12" />
    )
  const unread = !data && users.error ? "could not be read" : undefined
  const current = list.find((user) => user.user === opened && user.db === openedDb)

  return (
    <SectionFrame section="access">
      <StatGrid columns={3} dense role="group" aria-label="Accounts at a glance">
        <StatTile
          label="Users"
          value={figure(list.length)}
          tone={data && list.length === 0 ? "warning" : "default"}
          hint={
            data
              ? list.length === 0
                ? "the server asks nobody"
                : conn.user
                  ? `connected as ${conn.user}`
                  : "connected as nobody"
              : unread
          }
        />
        <StatTile
          label="Administrators"
          value={figure(admins)}
          hint={data ? "hold root on the server" : unread}
        />
        <StatTile
          label="Account databases"
          value={figure(homes)}
          hint={data ? "where users are kept" : unread}
        />
      </StatGrid>

      <Panel plain aria-label="Accounts" ref={frame}>
        {/* The views of the page as a strip on the panel's own hairline, its commands at the end. */}
        <div className="flex min-w-0 flex-wrap items-end justify-between gap-x-4 gap-y-2 border-b border-hairline">
          <div role="group" aria-label="Accounts view" className="flex gap-4">
            {(["users", "roles"] as const).map((one) => (
              <button
                key={one}
                type="button"
                aria-pressed={view === one}
                onClick={() => goto("access", { view: one === "users" ? null : one })}
                className={tabClasses(view === one, "h-10")}
              >
                {one === "users" ? "Users" : "Roles"}
              </button>
            ))}
          </div>
          <div className="flex shrink-0 flex-wrap items-center gap-1.5 pb-2">
            {staleOf(users) && <NotUpdating error={staleOf(users)!} />}
            {readOnly && <ProtectedTag />}
            {mayEdit && view === "users" && (
              <Button size="sm" onClick={() => setCreating(true)}>
                <UserPlus />
                New user
              </Button>
            )}
          </div>
        </div>
        <PanelBody>
          {view === "roles" ? (
            <RolesView />
          ) : !data ? (
            users.error ? (
              <CouldNotRead what="the users" error={users.error} onRetry={users.refresh} />
            ) : (
              <CardsSkeleton count={3} />
            )
          ) : list.length === 0 ? (
            <Notice tone="warning" title="No user exists on this server">
              A server with no user accepts every connection that reaches it, with every right. Make
              one that holds <span className="font-mono">root@admin</span> first, then start the
              server with access control on.
              {mayEdit && (
                <span className="mt-2 block">
                  <Button size="xs" variant="outline" onClick={() => setCreating(true)}>
                    <UserPlus />
                    New user
                  </Button>
                </span>
              )}
            </Notice>
          ) : (
            <div className="animate-rise space-y-3">
              {readOnly && (
                <FormNote>
                  This connection is protected: users and their roles are read here, and changed
                  from a connection that is not.
                </FormNote>
              )}
              <ChoiceList>
                {list.map((user) => {
                  const verbs: Verb[] = [
                    ...(mayEdit
                      ? [
                          {
                            key: "password",
                            label: "Change password",
                            icon: Key,
                            run: () => setPassword(user),
                          },
                        ]
                      : []),
                    ...(mayDrop && !user.self
                      ? [
                          {
                            key: "drop",
                            label: "Drop user",
                            icon: Trash,
                            danger: true,
                            run: () => confirm(dropRequest(user)),
                          },
                        ]
                      : []),
                  ]
                  return (
                    <ChoiceRow
                      key={`${user.db}.${user.user}`}
                      onSelect={() => select({ account: user.user, host: user.db })}
                      verb={`Open ${user.user} of ${user.db}`}
                      leading={<AccountMark name={user.user} />}
                      title={<AccountName name={user.user} host={user.db} />}
                      description={
                        user.roles.length > 0 ? (
                          <span className="font-mono">{user.roles.map(roleWord).join(", ")}</span>
                        ) : (
                          "Holds no role"
                        )
                      }
                      trailing={
                        beside ? (
                          <span className="flex items-center gap-2.5">{userTags(user)}</span>
                        ) : undefined
                      }
                      actions={
                        verbs.length > 0 ? (
                          <VerbMenu verbs={verbs} label={`Actions for ${user.user}`} />
                        ) : undefined
                      }
                    >
                      {!beside && (user.self || user.superuser) && (
                        <span className="flex flex-wrap items-center gap-x-2.5 gap-y-1 pl-12">
                          {userTags(user)}
                        </span>
                      )}
                    </ChoiceRow>
                  )
                })}
              </ChoiceList>
            </div>
          )}
        </PanelBody>
      </Panel>

      {opened && data && (
        <UserPanel
          key={`${openedDb}.${opened}`}
          user={current}
          name={opened}
          mayEdit={mayEdit}
          mayDrop={mayDrop}
          onChanged={users.refresh}
          onPassword={setPassword}
          onDrop={(user) => confirm(dropRequest(user, () => select({ account: null, host: null })))}
          onClose={() => select({ account: null, host: null })}
        />
      )}
      {creating && (
        <NewUser taken={list} onCreated={users.refresh} onClose={() => setCreating(false)} />
      )}
      {password && (
        <UserPassword
          user={password}
          onChanged={users.refresh}
          onClose={() => setPassword(undefined)}
        />
      )}
      {dialog}
    </SectionFrame>
  )
}

/** Picks one role on one database: the database first, then the roles that database knows. */
function RolePicker({
  onAdd,
  holding,
  pending,
}: {
  onAdd: (role: MongoRoleRef) => void
  /** The roles already held or already picked: not offered again. */
  holding: MongoRoleRef[]
  pending?: boolean
}) {
  const { conn } = useDatabase()
  const field = useId()
  const databases = useDatabaseNames(true)
  const [database, setDatabase] = useState(conn.database || "admin")
  const [role, setRole] = useState("")
  const roles = useRoles(database, true)
  const offered = (roles.data?.roles ?? []).filter(
    (one) => !holding.some((held) => sameRole(held, { role: one.role, db: database })),
  )
  return (
    <div className="space-y-1.5">
      <FieldRow columns={3}>
        <Field label="On the database" htmlFor={`${field}-db`}>
          <Select
            value={database}
            onValueChange={(next) => {
              setDatabase(next)
              setRole("")
            }}
          >
            <SelectTrigger id={`${field}-db`} className="w-full font-mono">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {databases.map((one) => (
                <SelectItem key={one} value={one} className="font-mono">
                  {one}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Field label="The role" htmlFor={`${field}-role`}>
          <Select value={role} onValueChange={setRole} disabled={!roles.data}>
            <SelectTrigger id={`${field}-role`} className="w-full font-mono">
              <SelectValue placeholder={roles.data ? "Choose a role" : "Reading the roles…"} />
            </SelectTrigger>
            <SelectContent>
              {offered.map((one) => (
                <SelectItem key={one.role} value={one.role} className="font-mono">
                  {one.role}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <div className="flex items-end">
          <Button
            type="button"
            variant="outline"
            className="h-11 w-full sm:h-9"
            disabled={!role}
            pending={pending}
            onClick={() => {
              onAdd({ role, db: database })
              setRole("")
            }}
          >
            <Plus />
            Add role
          </Button>
        </div>
      </FieldRow>
      {roles.error && (
        <FormNote tone="danger">
          The roles of {database} could not be read: {errorMessage(roles.error)}
        </FormNote>
      )}
    </div>
  )
}

function UserPanel({
  user,
  name,
  mayEdit,
  mayDrop,
  onChanged,
  onPassword,
  onDrop,
  onClose,
}: {
  user: MongoUser | undefined
  name: string
  mayEdit: boolean
  mayDrop: boolean
  onChanged: () => void
  onPassword: (user: MongoUser) => void
  onDrop: (user: MongoUser) => void
  onClose: () => void
}) {
  const { id } = useDatabase()
  const [busy, setBusy] = useState<string>()
  const [refusal, setRefusal] = useState<string>()

  const change = async (action: "grant" | "revoke", role: MongoRoleRef) => {
    if (!user) return
    setBusy(`${action}:${roleWord(role)}`)
    setRefusal(undefined)
    try {
      await post(`/databases/${id}/mongo/users/${action}`, {
        database: user.db,
        user: user.user,
        roles: [role],
      })
      notify.success(
        action === "grant"
          ? `Granted ${roleWord(role)} to ${user.user}`
          : `Took ${roleWord(role)} from ${user.user}`,
      )
      onChanged()
    } catch (err) {
      setRefusal(errorMessage(err))
    } finally {
      setBusy(undefined)
    }
  }

  const verbs: Verb[] = user
    ? [
        ...(mayEdit
          ? [
              {
                key: "password",
                label: "Change password",
                icon: Key,
                inline: true,
                run: () => onPassword(user),
              },
            ]
          : []),
        ...(mayDrop && !user.self
          ? [
              {
                key: "drop",
                label: "Drop user",
                icon: Trash,
                inline: true,
                danger: true,
                run: () => onDrop(user),
              },
            ]
          : []),
      ]
    : []

  return (
    <SidePanel
      open
      onOpenChange={(open) => !open && onClose()}
      width="md"
      initialFocus="body"
      title={
        <>
          <AccountMark name={name} size="sm" />
          <AccountName name={name} host={user?.db} className="text-title" />
        </>
      }
      description={`The user ${name}: the roles it holds`}
      actions={verbs.length > 0 ? <VerbBar verbs={verbs} /> : undefined}
    >
      {!user ? (
        <Notice title={`There is no user called ${name}`}>It may have been dropped.</Notice>
      ) : (
        <div className="animate-rise space-y-6">
          <div className="space-y-2">
            {(user.self || user.superuser) && (
              <p className="flex flex-wrap items-center gap-x-2.5 gap-y-1">{userTags(user)}</p>
            )}
            <FormFacts>
              <FormFact label="Kept in" mono>
                {user.db}
              </FormFact>
              {user.mechanisms.length > 0 && (
                <FormFact label="Signs in by" mono>
                  {user.mechanisms.join(", ")}
                </FormFact>
              )}
            </FormFacts>
          </div>

          <FormSection title="Roles">
            {user.roles.length === 0 ? (
              <FormNote>It holds no role, so it can sign in and do nothing.</FormNote>
            ) : (
              <ul className="divide-y divide-hairline" aria-label="Roles it holds">
                {user.roles.map((role) => (
                  <li key={roleWord(role)} className="flex min-w-0 items-center gap-3 py-1.5">
                    <span className="min-w-0 flex-1 truncate font-mono text-xs">
                      {role.role}
                      <span className="text-muted-foreground"> on </span>
                      <span style={{ color: nameHue(role.db) }}>{role.db}</span>
                    </span>
                    {mayEdit && !(user.self && role.role === "root") && (
                      <IconAction
                        label={`Take ${roleWord(role)} away`}
                        disabled={busy !== undefined}
                        onClick={() => void change("revoke", role)}
                      >
                        <Cross />
                      </IconAction>
                    )}
                  </li>
                ))}
              </ul>
            )}
            {user.self && (
              <FormNote>
                The dashboard connects as this user. A role taken from it is a page here that stops
                working.
              </FormNote>
            )}
            {mayEdit && (
              <RolePicker
                holding={user.roles}
                pending={busy?.startsWith("grant")}
                onAdd={(role) => void change("grant", role)}
              />
            )}
            {refusal && (
              <FormNote role="alert" tone="danger" className="break-words">
                Nothing was changed. {refusal}
              </FormNote>
            )}
          </FormSection>
        </div>
      )}
    </SidePanel>
  )
}

/** Make a user: where it is kept, its password, the roles it holds. The dialog ends on its connection string. */
function NewUser({
  taken,
  onCreated,
  onClose,
}: {
  taken: MongoUser[]
  onCreated: () => void
  onClose: () => void
}) {
  const { id, conn, engine, summary } = useDatabase()
  const field = useId()
  const databases = useDatabaseNames(true)
  const [name, setName] = useState("")
  const [home, setHome] = useState(conn.database || "admin")
  const [password, setPassword] = useState(() => generatePassword())
  const [roles, setRoles] = useState<MongoRoleRef[]>(
    conn.database ? [{ role: "readWrite", db: conn.database }] : [],
  )
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const [done, setDone] = useState(false)
  const typed = name.trim()
  const problem = accountNameProblem(
    name,
    taken.filter((user) => user.db === home).map((user) => user.user),
  )

  const create = async () => {
    setBusy(true)
    setRefusal(undefined)
    try {
      await post(`/databases/${id}/mongo/users`, { database: home, user: typed, password, roles })
      onCreated()
      setDone(true)
    } catch (err) {
      setRefusal(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  if (done) {
    const database = roles.find((role) => role.db !== "admin")?.db ?? conn.database
    return (
      <TaskDialog
        title="User created"
        description={`${typed} was created; its password is shown once`}
        size="lg"
        dirty
        busy={false}
        cancelLabel="Close"
        discardQuestion="Close? The password is not shown again."
        command="I have saved it"
        onRun={onClose}
        onClose={onClose}
      >
        <SecretShown
          title={`${typed} was created in ${home}`}
          password={password}
          dsn={accountDsn(conn, {
            user: typed,
            password,
            database,
            // Where the account lives, when that is not the database it opens.
            authSource: home === database ? undefined : home,
          })}
          user={typed}
          database={database}
        />
      </TaskDialog>
    )
  }

  return (
    <TaskDialog
      title="New user"
      description={`Create a user on the server ${conn.name} connects to`}
      size="lg"
      subject={databaseSubject(
        {
          name: summary?.versionNumber ? `${engine.label} ${summary.versionNumber}` : engine.label,
        },
        engine,
        <FormFact label="Server" mono>
          {conn.host}
          {conn.port ? `:${conn.port}` : ""}
        </FormFact>,
      )}
      dirty={typed !== ""}
      busy={busy}
      refusal={refusal}
      note="Its password is shown once, after it is made."
      command="Create user"
      commandIcon={UserPlus}
      disabled={Boolean(problem) || !password}
      onRun={() => void create()}
      onClose={onClose}
    >
      <FieldRow>
        <Field label="Name" htmlFor={`${field}-name`} error={typed ? problem : undefined}>
          <Input
            id={`${field}-name`}
            value={name}
            onChange={(event) => setName(event.target.value)}
            autoComplete="off"
            spellCheck={false}
            className="font-mono"
          />
        </Field>
        <Field label="Kept in" htmlFor={`${field}-home`} hint="The database it signs in against.">
          <Select value={home} onValueChange={setHome}>
            <SelectTrigger id={`${field}-home`} className="w-full font-mono">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {databases.map((one) => (
                <SelectItem key={one} value={one} className="font-mono">
                  {one}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      </FieldRow>
      <SecretField
        id={`${field}-password`}
        label="Password"
        hint="Made here, thirty characters. Type another if one is wanted."
        value={password}
        onChange={setPassword}
        error={password ? undefined : "A user needs a password."}
      />
      <div className="space-y-2">
        <p className="text-body font-medium">Roles it holds</p>
        {roles.length === 0 ? (
          <FormNote>None yet: it could sign in and do nothing.</FormNote>
        ) : (
          <ChipStrip role="group" aria-label="Roles it will hold">
            {roles.map((role) => (
              <FilterChip
                key={roleWord(role)}
                selected
                className="font-mono"
                aria-label={`Remove ${roleWord(role)}`}
                onClick={() => setRoles((held) => held.filter((one) => !sameRole(one, role)))}
              >
                {roleWord(role)}
                <Cross className="size-3 opacity-60" />
              </FilterChip>
            ))}
          </ChipStrip>
        )}
        <RolePicker holding={roles} onAdd={(role) => setRoles((held) => [...held, role])} />
      </div>
    </TaskDialog>
  )
}

/** Give a user another password: the password and nothing else is sent. */
function UserPassword({
  user,
  onChanged,
  onClose,
}: {
  user: MongoUser
  onChanged: () => void
  onClose: () => void
}) {
  const { id, conn, href } = useDatabase()
  const field = useId()
  const [password, setPassword] = useState(() => generatePassword())
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const [done, setDone] = useState(false)

  const change = async () => {
    setBusy(true)
    setRefusal(undefined)
    try {
      await put(`/databases/${id}/mongo/users`, { database: user.db, user: user.user, password })
      onChanged()
      setDone(true)
    } catch (err) {
      setRefusal(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  if (done) {
    const database = user.roles.find((role) => role.db !== "admin")?.db ?? conn.database
    return (
      <TaskDialog
        title="Password changed"
        description={`${user.user} has a new password; it is shown once`}
        size="lg"
        dirty
        busy={false}
        cancelLabel="Close"
        discardQuestion="Close? The password is not shown again."
        command="I have saved it"
        onRun={onClose}
        onClose={onClose}
      >
        <SecretShown
          title={`${user.user} has a new password`}
          password={password}
          dsn={accountDsn(conn, {
            user: user.user,
            password,
            database,
            authSource: user.db === database ? undefined : user.db,
          })}
          user={user.user}
          database={database}
          notes={
            user.self
              ? [
                  `${conn.name} signs in with this user, and still holds the old password: edit its address under Settings, or it stops working.`,
                ]
              : undefined
          }
        />
        {user.self && (
          <FormNote>
            <a href={`${href("settings")}#connection`} className="rounded-sm underline focus-ring">
              Open Settings
            </a>
          </FormNote>
        )}
      </TaskDialog>
    )
  }

  return (
    <TaskDialog
      title="Change password"
      description={`Give ${user.user} another password`}
      subject={{
        mark: <AccountMark name={user.user} />,
        name: <AccountName name={user.user} host={user.db} className="text-body" />,
      }}
      dirty={false}
      busy={busy}
      refusal={refusal}
      note="Only the password changes. Whatever uses the old one stops signing in."
      command="Change password"
      commandIcon={Key}
      disabled={!password}
      onRun={() => void change()}
      onClose={onClose}
    >
      <SecretField
        id={`${field}-password`}
        label="New password"
        hint="Made here, thirty characters. Type another if one is wanted."
        value={password}
        onChange={setPassword}
        error={password ? undefined : "A user needs a password."}
      />
    </TaskDialog>
  )
}

/**
 * What a role is, database by database: the ones the server is built with and
 * the ones made on it, each with the roles it inherits and — for one made
 * here — what it may do. It is a reading: a role is made in the console.
 */
function RolesView() {
  const { conn, engine, href } = useDatabase()
  const databases = useDatabaseNames(true)
  const [database, setDatabase] = useState(conn.database || "admin")
  const roles = useRoles(database, true)
  const data = roles.data
  const custom = (data?.roles ?? []).filter((role) => !role.builtin)
  const builtin = (data?.roles ?? []).filter((role) => role.builtin)
  return (
    <div className="space-y-4">
      <ChipStrip role="group" aria-label="Which database's roles">
        {databases.map((one) => (
          <FilterChip
            key={one}
            selected={one === database}
            className="font-mono"
            onClick={() => setDatabase(one)}
          >
            {one}
          </FilterChip>
        ))}
      </ChipStrip>
      {!data ? (
        roles.error ? (
          <CouldNotRead what="the roles" error={roles.error} onRetry={roles.refresh} />
        ) : (
          <LoadingRows rows={5} />
        )
      ) : (
        <div className="animate-rise space-y-4">
          <div className="space-y-1">
            <p className="text-body font-medium">
              Made on this server{" "}
              <span className="numeric font-normal text-muted-foreground">{custom.length}</span>
            </p>
            {custom.length === 0 ? (
              <FormNote>
                No role has been made in {database}. One is made in the console with{" "}
                <span className="font-mono">createRole</span>.{" "}
                {engine.has("query") && (
                  <Link href={href("query")} className="rounded-sm underline focus-ring">
                    Open {engine.section("query")?.title ?? "the console"}
                  </Link>
                )}
              </FormNote>
            ) : (
              <ul className="divide-y divide-hairline">
                {custom.map((role) => (
                  <li key={role.role} className="py-2">
                    <Disclosure
                      quiet
                      summary={<span className="font-mono text-xs">{role.role}</span>}
                      facts={`${plural(role.privileges.length, "privilege")} · inherits ${role.roles.length}`}
                    >
                      <div className="space-y-2 pl-6">
                        {role.roles.length > 0 && (
                          <FormNote>
                            Inherits{" "}
                            <span className="font-mono">{role.roles.map(roleWord).join(", ")}</span>
                          </FormNote>
                        )}
                        {role.privileges.map((privilege, index) => (
                          <div key={index} className="space-y-1">
                            <p className="font-mono text-hint break-all text-muted-foreground">
                              {privilege.resource}
                            </p>
                            <p className="flex flex-wrap gap-1">
                              {privilege.actions.map((action) => (
                                <Tag key={action} mono>
                                  {action}
                                </Tag>
                              ))}
                            </p>
                          </div>
                        ))}
                      </div>
                    </Disclosure>
                  </li>
                ))}
              </ul>
            )}
          </div>
          <div className="space-y-1.5">
            <p className="text-body font-medium">
              Built in{" "}
              <span className="numeric font-normal text-muted-foreground">{builtin.length}</span>
            </p>
            <p className={cn("flex flex-wrap gap-1.5")}>
              {builtin.map((role) => (
                <Tag key={role.role} mono>
                  {role.role}
                </Tag>
              ))}
            </p>
          </div>
        </div>
      )}
    </div>
  )
}
