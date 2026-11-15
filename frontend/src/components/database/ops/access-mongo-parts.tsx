"use client"

import { useId, useMemo, useState } from "react"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { generatePassword } from "@/components/database/connect/rules"
import { read } from "@/components/database/home/read"
import { accountDsn, roleWord, sameRole } from "@/components/database/ops/access-model"
import {
  AccountMark,
  AccountName,
  SecretField,
  SecretShown,
} from "@/components/database/ops/access-parts"
import type { MongoRoleRef, MongoRoles, MongoUser } from "@/components/database/ops/access-types"
import { TaskDialog } from "@/components/database/ops/settings-dialog"
import { useDatabase } from "@/components/database/shell/database-context"
import { Field, FieldRow, FormFact, FormNote } from "@/components/form"
import { Key, Plus } from "@/components/icons"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { usePoll } from "@/hooks/use-poll"
import { del, errorMessage, put } from "@/lib/api"
import { notify } from "@/lib/toast"

/**
 * What a document database's Access list and a user's own page both draw:
 * the reads of roles and databases, a user's tags, the picker of one role on
 * one database, the password dialog, and the question asked before a user is
 * dropped.
 */

/** Where the accounts and roles routes are asked about one database's roles. */
export function useRoles(database: string, enabled: boolean) {
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
export function useDatabaseNames(enabled: boolean) {
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

export function userTags(user: MongoUser) {
  return (
    <>
      {user.self && <Tag>this connection</Tag>}
      {user.superuser && <Tag>administrator</Tag>}
    </>
  )
}

/** The question asked before a user is dropped, with the user named. */
export function mongoDropRequest(
  id: number,
  user: MongoUser,
  onDropped: () => void,
): ConfirmRequest {
  return {
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
      onDropped()
      return "reported"
    },
  }
}

/** Picks one role on one database: the database first, then the roles that database knows. */
export function RolePicker({
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

/** Give a user another password: the password and nothing else is sent. */
export function UserPassword({
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
        cancelLabel={null}
        discardQuestion="Close? The password is not shown again."
        stayLabel="Go back"
        discardLabel="Close"
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
