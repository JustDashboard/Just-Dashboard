"use client"

import { useId, useState } from "react"
import { Key } from "@/components/icons"
import { errorMessage, put } from "@/lib/api"
import { FormFact, FormNote } from "@/components/form"
import { accountDsn, accountKey } from "@/components/database/ops/access-model"
import {
  AccountMark,
  AccountName,
  SecretField,
  SecretShown,
} from "@/components/database/ops/access-parts"
import type { DbRole, DbRoleAltered } from "@/components/database/ops/access-types"
import { generatePassword } from "@/components/database/connect/rules"
import { TaskDialog } from "@/components/database/ops/settings-dialog"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * Give an account another password. Only the password is sent: every other
 * attribute of the account stays exactly as it is.
 *
 * When the account is the one this connection signs in with, the server
 * replaces the connection's saved password too, once it has seen the new one
 * work — and says when it could not, so the connection is not left holding a
 * password that no longer opens anything.
 */
export function ChangePassword({
  role,
  own,
  onChanged,
  onClose,
}: {
  role: Pick<DbRole, "name" | "host">
  /** This connection signs in with it. */
  own: boolean
  onChanged: () => void
  onClose: () => void
}) {
  const { id, conn, href } = useDatabase()
  const field = useId()
  const [password, setPassword] = useState(() => generatePassword())
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const [done, setDone] = useState<{ notes: string[] }>()
  const label = accountKey(role)

  const change = async () => {
    setBusy(true)
    setRefusal(undefined)
    try {
      const answer = await put<DbRoleAltered>(
        `/databases/${id}/server/roles/${encodeURIComponent(role.name)}`,
        { password, ...(role.host !== undefined ? { host: role.host } : {}) },
      )
      onChanged()
      setDone({
        notes: own
          ? [
              answer.connectionUpdated
                ? `${conn.name} signs in with this account: its saved password was replaced with the new one.`
                : `${conn.name} signs in with this account, and its saved password could not be replaced. Edit the address under Settings, or this connection stops working.`,
            ]
          : [],
      })
    } catch (err) {
      setRefusal(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  if (done) {
    return (
      <TaskDialog
        title="Password changed"
        description={`${label} has a new password; it is shown once`}
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
          title={`${label} has a new password`}
          password={password}
          dsn={accountDsn(conn, { user: role.name, password })}
          user={role.name}
          database={conn.database}
          notes={done.notes}
        />
        {own && (
          <FormNote>
            The connection&rsquo;s address is under{" "}
            <a href={`${href("settings")}#connection`} className="rounded-sm underline focus-ring">
              Settings
            </a>
            .
          </FormNote>
        )}
      </TaskDialog>
    )
  }

  return (
    <TaskDialog
      title="Change password"
      description={`Give ${label} another password`}
      subject={{
        mark: <AccountMark name={role.name} />,
        name: <AccountName name={role.name} host={role.host} className="text-body" />,
        facts: own ? <FormFact label="Used by">{conn.name}, this connection</FormFact> : undefined,
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
        error={password ? undefined : "An account needs a password."}
      />
    </TaskDialog>
  )
}
