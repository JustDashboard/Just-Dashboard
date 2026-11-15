"use client"

import { useConfirm } from "@/components/confirm-dialog"
import { generatePassword } from "@/components/database/connect/rules"
import {
  CardsSkeleton,
  CouldNotRead,
  NotUpdating,
  staleOf,
} from "@/components/database/home/blocks"
import { read } from "@/components/database/home/read"
import { ProtectedTag, SectionFrame } from "@/components/database/kit"
import {
  accountDsn,
  accountNameProblem,
  roleWord,
  sameRole,
} from "@/components/database/ops/access-model"
import {
  RolePicker,
  UserPassword,
  mongoDropRequest,
  useDatabaseNames,
  useRoles,
  userTags,
} from "@/components/database/ops/access-mongo-parts"
import {
  AccountMark,
  AccountName,
  SecretField,
  SecretShown,
  SignInPicture,
} from "@/components/database/ops/access-parts"
import { QuietWord, TAGS_BESIDE_FROM, TagsUnder } from "@/components/database/ops/access-row"
import type { MongoRoleRef, MongoUser, MongoUsers } from "@/components/database/ops/access-types"
import { accountHref } from "@/components/database/ops/account-address"
import { ServerDown, isDown } from "@/components/database/ops/performance-parts"
import { TaskDialog, databaseSubject } from "@/components/database/ops/settings-dialog"
import { useFocusReturn } from "@/components/database/redis/use-focus-return"
import { useDatabase } from "@/components/database/shell/database-context"
import { useColumnWidth } from "@/components/deploy/settings/use-column-width"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Disclosure, Field, FieldRow, FormFact, FormNote } from "@/components/form"
import { Cross, Key, Trash, UserPlus } from "@/components/icons"
import { Panel, PanelBody } from "@/components/panel"
import { LoadingRows, Notice } from "@/components/state"
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
import { VerbMenu, type Verb } from "@/components/verbs"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { errorMessage, post } from "@/lib/api"
import { plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import Link from "next/link"
import { useId, useMemo, useState } from "react"

/**
 * A document database's accounts: the users, each living in a database and
 * holding roles on databases, and the roles themselves.
 *
 * Two views of one page (`?view=`). Users is the list — a user's initials in
 * its name's hue, the roles it holds, whether it is an administrator — and
 * a user opens on a page of its own (`access-mongo-account.tsx`), where a role
 * is granted or taken away. Roles is what a role is: the ones the server is built with, and any
 * made here, with what each inherits and may do.
 *
 * A server that lists no user at all is one that asks nobody who they are,
 * and the page says that rather than "empty".
 */
export function MongoAccess() {
  const { id, readOnly, status, param, goto } = useDatabase()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  useFocusReturn()
  const down = isDown(status.state)
  const view = param("view") === "roles" ? "roles" : "users"
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
  const beside = width >= TAGS_BESIDE_FROM

  if (down) {
    return (
      <SectionFrame section="access">
        <ServerDown what="Its users" />
      </SectionFrame>
    )
  }

  const data = users.data

  return (
    <SectionFrame section="access">
      {view === "users" && (
        <SignInPicture
          noun="user"
          accounts={list.map((user) => ({
            key: `${user.db}.${user.user}`,
            name: user.user,
            host: user.db,
            signsIn: true,
            own: user.self,
            admin: user.superuser,
          }))}
        />
      )}
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
                            run: () => confirm(mongoDropRequest(id, user, users.refresh)),
                          },
                        ]
                      : []),
                  ]
                  return (
                    <ChoiceRow
                      key={`${user.db}.${user.user}`}
                      href={accountHref(id, { name: user.user, host: user.db })}
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
                      {!beside && (
                        <TagsUnder>
                          {user.self || user.superuser ? (
                            userTags(user)
                          ) : (
                            <QuietWord>
                              {user.mechanisms.length > 0 ? user.mechanisms.join(", ") : "user"}
                            </QuietWord>
                          )}
                        </TagsUnder>
                      )}
                    </ChoiceRow>
                  )
                })}
              </ChoiceList>
            </div>
          )}
        </PanelBody>
      </Panel>

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
        cancelLabel={null}
        discardQuestion="Close? The password is not shown again."
        stayLabel="Go back"
        discardLabel="Close"
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
