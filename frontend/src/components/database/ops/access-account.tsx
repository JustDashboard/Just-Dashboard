"use client"

import { useId, useState } from "react"
import { Key, Trash } from "@/components/icons"
import { del, errorMessage, put } from "@/lib/api"
import { plural, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import {
  Field,
  FieldRow,
  FormFact,
  FormFacts,
  FormNote,
  FormSection,
  OptionList,
  OptionRow,
} from "@/components/form"
import { SidePanel } from "@/components/side-panel"
import { LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { VerbBar, type Verb } from "@/components/verbs"
import { CouldNotRead } from "@/components/database/home/blocks"
import { read } from "@/components/database/home/read"
import { GrantsMatrix } from "@/components/database/ops/access-grants"
import {
  ATTRIBUTES,
  accountKey,
  accountTags,
  attributeValue,
  isOwnAccount,
  lockoutReason,
  type AttributeField,
} from "@/components/database/ops/access-model"
import { ChangePassword } from "@/components/database/ops/access-new"
import { AccountMark, AccountName } from "@/components/database/ops/access-parts"
import type {
  DbPrivileges,
  DbRole,
  DbRoleAlterRequest,
  DbRoleDetail,
} from "@/components/database/ops/access-types"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * The question asked before an account is dropped, with the account named.
 * The row's menu and the panel's own command both ask it.
 */
export function dropAccountRequest(
  id: number,
  role: Pick<DbRole, "name" | "host" | "connections">,
  onDropped: () => void,
): ConfirmRequest {
  const label = accountKey(role)
  return {
    title: "Drop account",
    confirmLabel: "Drop",
    subject: {
      mark: <AccountMark name={role.name} />,
      name: <AccountName name={role.name} host={role.host} className="text-body" />,
      facts: (
        <FormFact label="Sessions now">{role.connections > 0 ? role.connections : "none"}</FormFact>
      ),
    },
    description: (
      <>
        <p>
          Removes the account from the server. Whatever signs in with it stops signing in, and what
          it was granted goes with it.
        </p>
        <p>
          The engine refuses, and changes nothing, while the account still owns something in a
          database.
        </p>
      </>
    ),
    action: async () => {
      await del(`/databases/${id}/server/roles/${encodeURIComponent(role.name)}`, {
        query: role.host !== undefined ? { host: role.host } : undefined,
      })
      notify.success(`Dropped ${label}`)
      onDropped()
      return "reported"
    },
  }
}

/**
 * One account, in a panel beside the list: what it is, what it may do on the
 * server, and what it holds — each of them editable where the engine and the
 * role allow.
 *
 * The attributes are a small form with its own save, which sends only the
 * switches that were thrown: an account's other attributes are never restated
 * and so never reset. The dashboard's own account keeps the switches that
 * would lock the dashboard out, drawn and held, with the reason.
 */
export function AccountPanel({
  name,
  host,
  roles,
  privileges,
  confirm,
  onChanged,
  onClose,
}: {
  name: string
  host?: string
  /** Every account of the server, for memberships. */
  roles: DbRole[]
  privileges: DbPrivileges | undefined
  confirm: (request: ConfirmRequest) => void
  /** Something about the accounts changed: the list is read again. */
  onChanged: () => void
  onClose: () => void
}) {
  const { id, conn, readOnly } = useDatabase()
  const { can } = useAuth()
  const field = useId()
  const [edits, setEdits] = useState<DbRoleAlterRequest>({})
  const [saving, setSaving] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const [password, setPassword] = useState(false)
  const detail = usePoll(
    (signal) =>
      read<DbRoleDetail>(
        `/databases/${id}/server/roles/${encodeURIComponent(name)}`,
        (answer) => typeof answer.name === "string" && Array.isArray(answer.grants),
        host ? { host } : undefined,
        signal,
      ),
    0,
    [id, name, host],
  )
  const data = detail.data
  const own = isOwnAccount({ name }, conn)
  const mayEdit = can("system.admin") && !readOnly
  const mayDrop = mayEdit && can("destructive") && !own && !data?.system
  const editable = data?.editable ?? []
  const count = Object.keys(edits).length

  // Where an engine both locks an account and has a login flag, they are one
  // fact said twice; the lock is the one drawn.
  const switches = ATTRIBUTES.filter(
    (one) =>
      editable.includes(one.field) && !(one.field === "login" && editable.includes("locked")),
  )
  const set = (key: AttributeField, value: boolean) => {
    if (!data) return
    setRefusal(undefined)
    setEdits((held) => {
      const next = { ...held }
      if (value === attributeValue(data, key)) delete next[key]
      else next[key] = value
      return next
    })
  }
  const limit =
    edits.connectionLimit !== undefined
      ? String(edits.connectionLimit)
      : data && data.connectionLimit > 0
        ? String(data.connectionLimit)
        : ""

  const save = async () => {
    setSaving(true)
    setRefusal(undefined)
    try {
      await put(`/databases/${id}/server/roles/${encodeURIComponent(name)}`, {
        ...edits,
        ...(host !== undefined ? { host } : {}),
      })
      notify.success(`Saved ${host ? `${name}@${host}` : name}`)
      setEdits({})
      detail.refresh()
      onChanged()
    } catch (err) {
      setRefusal(errorMessage(err))
    } finally {
      setSaving(false)
    }
  }

  const verbs: Verb[] = [
    ...(mayEdit && editable.includes("password")
      ? [
          {
            key: "password",
            label: "Change password",
            icon: Key,
            inline: true,
            run: () => setPassword(true),
          },
        ]
      : []),
    ...(mayDrop && data
      ? [
          {
            key: "drop",
            label: "Drop account",
            icon: Trash,
            inline: true,
            danger: true,
            run: () =>
              confirm(
                dropAccountRequest(id, data, () => {
                  onChanged()
                  onClose()
                }),
              ),
          },
        ]
      : []),
  ]

  // USAGE at the server's level is the engine's word for "may sign in": it grants nothing.
  const server = (data?.grants ?? [])
    .filter((grant) => grant.level === "server")
    .flatMap((grant) => grant.privileges)
    .filter((privilege) => privilege.toUpperCase() !== "USAGE")
  const others = roles
    .filter((role) => role.name !== name && !role.system)
    .map((role) => role.name)
    .filter((role, index, list) => list.indexOf(role) === index)

  return (
    <SidePanel
      open
      onOpenChange={(open) => !open && onClose()}
      width="lg"
      initialFocus="body"
      title={
        <>
          <AccountMark name={name} size="sm" />
          <AccountName name={name} host={host} className="text-title" />
        </>
      }
      description={`The account ${host ? `${name}@${host}` : name}: its attributes and its grants`}
      actions={verbs.length > 0 ? <VerbBar verbs={verbs} /> : undefined}
    >
      {!data ? (
        detail.error ? (
          <CouldNotRead what="the account" error={detail.error} onRetry={detail.refresh} />
        ) : (
          <LoadingRows rows={8} />
        )
      ) : (
        <div className="animate-rise space-y-8">
          <div className="space-y-2">
            <p className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1">
              {accountTags(data, own).map((tag) => (
                <Tag key={tag.word} tone={tag.tone}>
                  {tag.word}
                </Tag>
              ))}
              <span className="flex">
                <Status
                  tone={data.connections > 0 ? "running" : "unknown"}
                  label={
                    data.connections > 0
                      ? `${plural(data.connections, "session")} now`
                      : "no session now"
                  }
                />
              </span>
            </p>
            <FormFacts>
              {data.authPlugin && (
                <FormFact label="Signs in by" mono>
                  {data.authPlugin}
                </FormFact>
              )}
              {data.validUntil && (
                <FormFact label="Password valid until">{timestamp(data.validUntil)}</FormFact>
              )}
              {data.config.map((setting) => (
                <FormFact key={setting} label="Set for it" mono>
                  {setting}
                </FormFact>
              ))}
              {data.configRedacted?.map((setting) => (
                <FormFact key={setting} label="Set for it" mono>
                  {setting} = hidden
                </FormFact>
              ))}
            </FormFacts>
            {data.notes?.map((note) => (
              <FormNote key={note}>{note}</FormNote>
            ))}
          </div>

          {(switches.length > 0 || editable.includes("connectionLimit")) && (
            <FormSection title="Attributes">
              <form
                aria-label="Account attributes"
                className="space-y-4"
                onSubmit={(event) => {
                  event.preventDefault()
                  if (count > 0 && !saving) void save()
                }}
              >
                <OptionList>
                  {switches.map((one) => {
                    const now = attributeValue(data, one.field)
                    const value = (edits[one.field] as boolean | undefined) ?? now
                    const held = lockoutReason(one.field, !value, own)
                    return (
                      <OptionRow
                        key={one.field}
                        title={one.title}
                        hint={held ?? one.hint}
                        checked={value}
                        disabled={!mayEdit || Boolean(held)}
                        tone={one.field === "superuser" && value ? "warning" : "default"}
                        onCheckedChange={(next) => set(one.field, next)}
                      />
                    )
                  })}
                </OptionList>
                {(editable.includes("connectionLimit") || editable.includes("validUntil")) && (
                  <FieldRow>
                    {editable.includes("connectionLimit") && (
                      <Field
                        label="Sessions at once"
                        htmlFor={`${field}-limit`}
                        hint="Empty for no limit."
                      >
                        <Input
                          id={`${field}-limit`}
                          value={limit === "-1" ? "" : limit}
                          disabled={!mayEdit}
                          inputMode="numeric"
                          autoComplete="off"
                          className="font-mono"
                          onChange={(event) => {
                            const typed = event.target.value.replace(/[^0-9]/g, "")
                            const saved = data.connectionLimit > 0 ? data.connectionLimit : -1
                            const next = typed === "" ? -1 : Number(typed)
                            setEdits((held) => {
                              const rest = { ...held }
                              if (next === saved) delete rest.connectionLimit
                              else rest.connectionLimit = next
                              return rest
                            })
                          }}
                        />
                      </Field>
                    )}
                    {editable.includes("validUntil") && (
                      <Field
                        label="Password valid until"
                        htmlFor={`${field}-until`}
                        hint="Empty for no end."
                      >
                        <Input
                          id={`${field}-until`}
                          type="date"
                          disabled={!mayEdit}
                          className="font-mono"
                          value={edits.validUntil ?? (data.validUntil ?? "").slice(0, 10)}
                          onChange={(event) => {
                            const next = event.target.value
                            const saved = (data.validUntil ?? "").slice(0, 10)
                            setEdits((held) => {
                              const rest = { ...held }
                              if (next === saved) delete rest.validUntil
                              else rest.validUntil = next || "infinity"
                              return rest
                            })
                          }}
                        />
                      </Field>
                    )}
                  </FieldRow>
                )}
                {refusal && (
                  <FormNote role="alert" tone="danger" className="break-words">
                    Nothing was changed. {refusal}
                  </FormNote>
                )}
                {mayEdit && (
                  <div className="flex flex-wrap items-center justify-end gap-2">
                    {count > 0 && (
                      <>
                        <FormNote className="mr-auto">
                          Only {count === 1 ? "this attribute is" : `these ${count} are`} sent; the
                          others stay as they are.
                        </FormNote>
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          disabled={saving}
                          onClick={() => {
                            setEdits({})
                            setRefusal(undefined)
                          }}
                        >
                          Discard
                        </Button>
                      </>
                    )}
                    <Button
                      type="submit"
                      size="sm"
                      variant={count > 0 ? "default" : "outline"}
                      disabled={count === 0}
                      pending={saving}
                    >
                      Save attributes
                    </Button>
                  </div>
                )}
              </form>
            </FormSection>
          )}

          <FormSection title="Grants">
            {server.length > 0 && (
              <div className="space-y-1.5">
                <p className="text-body font-medium">On the whole server</p>
                <p className="flex flex-wrap gap-1.5">
                  {server.map((privilege) => (
                    <Tag key={privilege} mono>
                      {privilege}
                    </Tag>
                  ))}
                </p>
              </div>
            )}
            {data.superuser && (
              <FormNote>
                An administrator may do everything on every database, whatever is granted below.
              </FormNote>
            )}
            {privileges?.supported && privileges.levels.length > 0 ? (
              <GrantsMatrix
                detail={data}
                levels={privileges.levels}
                roles={others}
                editable={mayEdit}
                onApplied={() => {
                  detail.refresh()
                  onChanged()
                }}
              />
            ) : (
              <FormNote>This engine&rsquo;s grants are not listed object by object here.</FormNote>
            )}
          </FormSection>
        </div>
      )}

      {password && data && (
        <ChangePassword
          role={data}
          own={own}
          onChanged={() => {
            detail.refresh()
            onChanged()
          }}
          onClose={() => setPassword(false)}
        />
      )}
    </SidePanel>
  )
}
