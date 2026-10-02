"use client"

import { useId, useMemo, useState } from "react"
import { Trash, UserPlus } from "@/components/icons"
import { del, errorMessage, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm, type ConfirmRequest } from "@/components/confirm-dialog"
import { useColumnWidth } from "@/components/deploy/settings/use-column-width"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Field, FormFact, FormNote, FormSection, OptionList, OptionRow } from "@/components/form"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { SidePanel } from "@/components/side-panel"
import { Notice } from "@/components/state"
import { StatButton, StatGrid, StatTile } from "@/components/stat-tile"
import { ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Textarea } from "@/components/ui/textarea"
import { VerbBar, VerbMenu, type Verb } from "@/components/verbs"
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
  aclChanged,
  aclDraftOf,
  aclRequest,
  aclSummary,
  ruleList,
  type AclDraft,
} from "@/components/database/ops/access-model"
import {
  AccountMark,
  AccountName,
  SecretField,
  SecretShown,
} from "@/components/database/ops/access-parts"
import type { RedisACL, RedisACLChange, RedisACLUser } from "@/components/database/ops/access-types"
import { ServerDown, isDown } from "@/components/database/ops/performance-parts"
import { TaskDialog, databaseSubject } from "@/components/database/ops/settings-dialog"
import { useDatabase } from "@/components/database/shell/database-context"

type Show = "" | "enabled" | "unrestricted" | "open"

const BESIDE_FROM = 640

/** The command categories an ACL rule is most often written with. */
const CATEGORIES = ["+@all", "+@read", "+@write", "+@keyspace", "-@dangerous", "-@admin"]

function tagsOf(user: RedisACLUser) {
  return (
    <>
      {user.self && <Tag>this connection</Tag>}
      {user.system && <Tag>default</Tag>}
      {!user.enabled && <Tag>off</Tag>}
      {user.noPassword && <Tag tone="warning">any password</Tag>}
    </>
  )
}

/**
 * A key–value server's ACL users: who may connect, and the rule each is held
 * to — which commands, on which keys, on which channels.
 *
 * The readings narrow the list: every user, the ones switched on, the ones
 * held to nothing, the ones that accept any password. A user opens in a panel
 * with its rule as the server holds it and the form that changes it; only the
 * part of the rule that was edited is sent.
 *
 * The user the dashboard connects as is marked, and what would cut the
 * dashboard off — switching it off, deleting it, taking commands or keys away
 * from it — is drawn held, with the reason.
 */
export function RedisAccess() {
  const { id, engine, readOnly, status, param, select } = useDatabase()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const down = isDown(status.state)
  const [creating, setCreating] = useState(false)
  const [frame, width] = useColumnWidth<HTMLDivElement>()
  const show = (param("show") || "") as Show
  const opened = param("account")
  const acl = usePoll(
    (signal) =>
      read<RedisACL>(
        `/databases/${id}/redis/acl`,
        (answer) => Array.isArray(answer.users),
        undefined,
        signal,
      ),
    30_000,
    [id],
    { enabled: !down },
  )
  const data = acl.data
  const users = useMemo(() => data?.users ?? [], [data])
  const mayEdit = can("system.admin") && !readOnly
  const mayDelete = mayEdit && can("destructive")
  const shown = users.filter(
    (user) =>
      show === "" ||
      (show === "enabled" && user.enabled) ||
      (show === "unrestricted" && user.unrestricted) ||
      (show === "open" && user.noPassword && user.enabled),
  )
  const open = users.filter((user) => user.noPassword && user.enabled).length

  if (down) {
    return (
      <SectionFrame section="access">
        <ServerDown what="Its users" />
      </SectionFrame>
    )
  }

  const tile = (
    key: Show,
    label: string,
    value: number,
    hint: string,
    name: string,
    warn = false,
  ) => (
    <StatButton
      label={name}
      pressed={key !== "" && show === key}
      onClick={() => select({ show: show === key || key === "" ? null : key })}
    >
      <StatTile
        className="h-full transition-colors group-hover:bg-row-hover"
        label={label}
        tone={data && warn && value > 0 ? "warning" : "default"}
        value={
          data ? (
            <span key="value" className="animate-rise">
              {value}
            </span>
          ) : acl.error ? (
            <span className="text-muted-foreground">—</span>
          ) : (
            <Skeleton className="my-1 h-6 w-12" />
          )
        }
        hint={data ? hint : acl.error ? "could not be read" : undefined}
      />
    </StatButton>
  )
  const beside = width >= BESIDE_FROM
  const dropRequest = (user: RedisACLUser, after?: () => void): ConfirmRequest => ({
    title: "Delete user",
    confirmLabel: "Delete",
    subject: {
      mark: <AccountMark name={user.name} />,
      name: <AccountName name={user.name} className="text-body" />,
      facts: <FormFact label="Rule">{aclSummary(user)}</FormFact>,
    },
    description: (
      <p>
        Removes the user from the server and closes the connections it holds. Whatever signs in with
        it stops signing in.
      </p>
    ),
    action: async () => {
      const answer = await del<RedisACLChange>(
        `/databases/${id}/redis/acl/${encodeURIComponent(user.name)}`,
      )
      notify.success(`Deleted ${user.name}`, { description: answer.notice })
      acl.refresh()
      after?.()
      return "reported"
    },
  })

  return (
    <SectionFrame section="access">
      <StatGrid columns={4} dense role="group" aria-label="Users at a glance">
        {tile("", "Users", users.length, "in the server's ACL", "Every user")}
        {tile(
          "enabled",
          "Switched on",
          users.filter((user) => user.enabled).length,
          "may connect",
          "Only users that are switched on",
        )}
        {tile(
          "unrestricted",
          "Unrestricted",
          users.filter((user) => user.unrestricted).length,
          "every command on every key",
          "Only users held to nothing",
        )}
        {tile(
          "open",
          "Any password",
          open,
          open > 0 ? "need no password" : "none goes without one",
          "Only users that accept any password",
          true,
        )}
      </StatGrid>

      <Panel plain aria-label="Users" ref={frame}>
        <PanelHeader
          title="Users"
          actions={
            <>
              {staleOf(acl) && <NotUpdating error={staleOf(acl)!} />}
              {readOnly && <ProtectedTag />}
              {mayEdit && data?.supported && (
                <Button size="sm" onClick={() => setCreating(true)}>
                  <UserPlus />
                  New user
                </Button>
              )}
            </>
          }
        />
        <PanelBody>
          {!data ? (
            acl.error ? (
              <CouldNotRead what="the users" error={acl.error} onRetry={acl.refresh} />
            ) : (
              <CardsSkeleton count={3} />
            )
          ) : !data.supported ? (
            <Notice title={`${engine.label} keeps no users here`}>
              {data.reason ?? "This server has no access control list to read."}
            </Notice>
          ) : shown.length === 0 ? (
            <p className="py-6 text-center text-body text-muted-foreground">
              No user matches.{" "}
              <button
                type="button"
                className="rounded-sm underline focus-ring"
                onClick={() => select({ show: null })}
              >
                Show every user
              </button>
            </p>
          ) : (
            <div className="animate-rise space-y-3">
              {readOnly && (
                <FormNote>
                  This connection is protected: users and their rules are read here, and changed
                  from a connection that is not.
                </FormNote>
              )}
              <ChoiceList>
                {shown.map((user) => {
                  const verbs: Verb[] =
                    mayDelete && !user.self && !user.system
                      ? [
                          {
                            key: "delete",
                            label: "Delete user",
                            icon: Trash,
                            danger: true,
                            run: () => confirm(dropRequest(user)),
                          },
                        ]
                      : []
                  return (
                    <ChoiceRow
                      key={user.name}
                      onSelect={() => select({ account: user.name })}
                      verb={`Open ${user.name}`}
                      leading={<AccountMark name={user.name} />}
                      title={<AccountName name={user.name} />}
                      description={<span className="font-mono">{aclSummary(user)}</span>}
                      trailing={
                        beside ? (
                          <span className="flex items-center gap-2.5">{tagsOf(user)}</span>
                        ) : undefined
                      }
                      actions={
                        verbs.length > 0 ? (
                          <VerbMenu verbs={verbs} label={`Actions for ${user.name}`} />
                        ) : undefined
                      }
                    >
                      {!beside && (
                        <span className="flex flex-wrap items-center gap-x-2.5 gap-y-1 pl-12">
                          {tagsOf(user)}
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

      {opened && data?.supported && (
        <UserPanel
          key={opened}
          user={users.find((user) => user.name === opened)}
          name={opened}
          mayEdit={mayEdit}
          mayDelete={mayDelete}
          onChanged={acl.refresh}
          onDelete={(user) => confirm(dropRequest(user, () => select({ account: null })))}
          onClose={() => select({ account: null })}
        />
      )}
      {creating && (
        <NewUser
          taken={users.map((user) => user.name)}
          onCreated={acl.refresh}
          onClose={() => setCreating(false)}
        />
      )}
      {dialog}
    </SectionFrame>
  )
}

/**
 * The parts of a rule as a form: whether the user may connect, its password,
 * and the three lists — key patterns, channel patterns, command rules in the
 * order they apply. `held` is why the dashboard's own user keeps the parts
 * that would cut it off.
 */
function AclFields({
  draft,
  onChange,
  held,
  exists,
  disabled,
}: {
  draft: AclDraft
  onChange: (patch: Partial<AclDraft>) => void
  /** Why switching it off, or narrowing its commands and keys, is not offered. */
  held?: string
  /** The user exists: an empty password keeps the ones it has. */
  exists: boolean
  disabled?: boolean
}) {
  const field = useId()
  const commands = ruleList(draft.commands)
  return (
    <div className="space-y-4">
      {held && <FormNote data-slot="acl-held">{held}</FormNote>}
      <OptionList>
        <OptionRow
          title="Switched on: it may connect"
          checked={draft.enabled}
          disabled={disabled || Boolean(held)}
          onCheckedChange={(enabled) => onChange({ enabled })}
        />
        <OptionRow
          title="Accept any password"
          hint="Anything that reaches the port signs in as this user."
          tone={draft.noPassword ? "warning" : "default"}
          checked={draft.noPassword}
          disabled={disabled}
          onCheckedChange={(noPassword) => onChange({ noPassword, password: "" })}
        />
      </OptionList>
      {!draft.noPassword && !disabled && (
        <SecretField
          id={`${field}-password`}
          label={exists ? "New password" : "Password"}
          hint={
            exists
              ? "Empty keeps the passwords it has. A new one replaces them all."
              : "Made here. Type another if one is wanted."
          }
          value={draft.password}
          onChange={(password) => onChange({ password })}
        />
      )}
      <Field
        label="Keys"
        htmlFor={`${field}-keys`}
        hint="Patterns of the keys it may touch, one to a line: app:*, %R~cache:* for read only. * is every key."
      >
        <Textarea
          id={`${field}-keys`}
          value={draft.keys}
          disabled={disabled || Boolean(held)}
          onChange={(event) => onChange({ keys: event.target.value })}
          rows={2}
          spellCheck={false}
          className="font-mono text-xs"
        />
      </Field>
      <Field
        label="Commands"
        htmlFor={`${field}-commands`}
        hint="Rules in the order they apply, one to a line, starting from nothing: +@read, -@dangerous, +get, -config|set."
      >
        <Textarea
          id={`${field}-commands`}
          value={draft.commands}
          disabled={disabled || Boolean(held)}
          onChange={(event) => onChange({ commands: event.target.value })}
          rows={3}
          spellCheck={false}
          className="font-mono text-xs"
        />
        {!disabled && !held && (
          <ChipStrip role="group" aria-label="Add a command category">
            {CATEGORIES.filter((one) => !commands.includes(one)).map((one) => (
              <FilterChip
                key={one}
                className="font-mono"
                onClick={() => onChange({ commands: [...commands, one].join("\n") })}
              >
                {one}
              </FilterChip>
            ))}
          </ChipStrip>
        )}
      </Field>
      <Field
        label="Channels"
        htmlFor={`${field}-channels`}
        hint="Patterns of the Pub/Sub channels it may use, one to a line. Empty for none."
      >
        <Textarea
          id={`${field}-channels`}
          value={draft.channels}
          disabled={disabled}
          onChange={(event) => onChange({ channels: event.target.value })}
          rows={2}
          spellCheck={false}
          className="font-mono text-xs"
        />
      </Field>
    </div>
  )
}

function UserPanel({
  user,
  name,
  mayEdit,
  mayDelete,
  onChanged,
  onDelete,
  onClose,
}: {
  user: RedisACLUser | undefined
  name: string
  mayEdit: boolean
  mayDelete: boolean
  onChanged: () => void
  onDelete: (user: RedisACLUser) => void
  onClose: () => void
}) {
  const { id, href } = useDatabase()
  const [edits, setEdits] = useState<Partial<AclDraft>>({})
  const [saving, setSaving] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const [notice, setNotice] = useState<string>()
  const draft = { ...aclDraftOf(user), ...edits }
  const request = aclRequest(user, draft)
  const changed = user !== undefined && aclChanged(request)
  const held = user?.self
    ? "The dashboard connects as this user. It stays switched on, and its keys and commands stay as they are: changing them would cut the dashboard off from the server."
    : undefined

  const save = async () => {
    setSaving(true)
    setRefusal(undefined)
    try {
      const answer = await put<RedisACLChange>(
        `/databases/${id}/redis/acl/${encodeURIComponent(name)}`,
        request,
      )
      notify.success(`Saved ${name}`, {
        description: answer.persisted ? undefined : "Kept until the server restarts.",
      })
      setNotice(answer.notice)
      setEdits({})
      onChanged()
    } catch (err) {
      setRefusal(errorMessage(err))
    } finally {
      setSaving(false)
    }
  }

  const verbs: Verb[] =
    user && mayDelete && !user.self && !user.system
      ? [
          {
            key: "delete",
            label: "Delete user",
            icon: Trash,
            inline: true,
            danger: true,
            run: () => onDelete(user),
          },
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
          <AccountName name={name} className="text-title" />
        </>
      }
      description={`The user ${name}: its rule and the form that changes it`}
      actions={verbs.length > 0 ? <VerbBar verbs={verbs} /> : undefined}
    >
      {!user ? (
        <Notice title={`There is no user called ${name}`}>
          It may have been deleted, or the server restarted without an ACL file to keep it.
        </Notice>
      ) : (
        <div className="animate-rise space-y-6">
          <div className="space-y-2">
            <p className="flex flex-wrap items-center gap-x-2.5 gap-y-1">{tagsOf(user)}</p>
            <div className="space-y-1.5">
              <p className="eyebrow">The rule as the server holds it</p>
              <Well className="text-hint leading-relaxed break-words whitespace-pre-wrap">
                {user.rule || "no rule"}
              </Well>
            </div>
            <FormNote>
              {user.passwords > 0
                ? `${user.passwords === 1 ? "One password is" : `${user.passwords} passwords are`} set. The server never sends them back.`
                : user.noPassword
                  ? "It accepts any password."
                  : "No password is set, so nothing signs in as it."}
            </FormNote>
          </div>

          {notice && (
            <Notice tone="warning" title="The server said">
              {notice}{" "}
              {user.self && (
                <a
                  href={`${href("settings")}#connection`}
                  className="rounded-sm underline focus-ring"
                >
                  Open Settings
                </a>
              )}
            </Notice>
          )}

          <FormSection title="Rule">
            <form
              aria-label="The user's rule"
              className="space-y-4"
              onSubmit={(event) => {
                event.preventDefault()
                if (changed && !saving) void save()
              }}
            >
              <AclFields
                draft={draft}
                exists
                held={held}
                disabled={!mayEdit}
                onChange={(patch) => {
                  setEdits((now) => ({ ...now, ...patch }))
                  setRefusal(undefined)
                }}
              />
              {refusal && (
                <FormNote role="alert" tone="danger" className="break-words">
                  Nothing was changed. {refusal}
                </FormNote>
              )}
              {mayEdit && (
                <div className="flex flex-wrap items-center justify-end gap-2">
                  {changed && (
                    <>
                      <FormNote className="mr-auto">
                        Only what was edited is sent; the rest of the rule stays.
                      </FormNote>
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        disabled={saving}
                        onClick={() => setEdits({})}
                      >
                        Discard
                      </Button>
                    </>
                  )}
                  <Button
                    type="submit"
                    size="sm"
                    variant={changed ? "default" : "outline"}
                    disabled={!changed}
                    pending={saving}
                  >
                    Save rule
                  </Button>
                </div>
              )}
            </form>
          </FormSection>
        </div>
      )}
    </SidePanel>
  )
}

/** Make a user: its name, its password and its rule; the dialog ends on the string it connects with. */
function NewUser({
  taken,
  onCreated,
  onClose,
}: {
  taken: string[]
  onCreated: () => void
  onClose: () => void
}) {
  const { id, conn, engine, summary } = useDatabase()
  const field = useId()
  const [name, setName] = useState("")
  const [draft, setDraft] = useState<AclDraft>(() => ({
    ...aclDraftOf(undefined),
    password: generatePassword(),
    keys: "*",
    commands: "+@all\n-@dangerous",
  }))
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const [done, setDone] = useState<{ notes: string[] }>()
  const typed = name.trim()
  const problem = accountNameProblem(name, taken)

  const create = async () => {
    setBusy(true)
    setRefusal(undefined)
    try {
      const answer = await put<RedisACLChange>(
        `/databases/${id}/redis/acl/${encodeURIComponent(typed)}`,
        aclRequest(undefined, draft),
      )
      onCreated()
      setDone({ notes: answer.notice ? [answer.notice] : [] })
    } catch (err) {
      setRefusal(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  if (done) {
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
        {draft.noPassword ? (
          <Notice tone="warning" title={`${typed} was created, and accepts any password`}>
            Anything that reaches the port signs in as it.
          </Notice>
        ) : (
          <SecretShown
            title={`${typed} was created`}
            password={draft.password}
            dsn={accountDsn(conn, { user: typed, password: draft.password })}
            user={typed}
            database={conn.database}
            notes={done.notes}
          />
        )}
      </TaskDialog>
    )
  }

  return (
    <TaskDialog
      title="New user"
      description={`Create an ACL user on the server ${conn.name} connects to`}
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
      disabled={Boolean(problem) || (!draft.noPassword && !draft.password)}
      onRun={() => void create()}
      onClose={onClose}
    >
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
      <AclFields
        draft={draft}
        exists={false}
        onChange={(patch) => setDraft((now) => ({ ...now, ...patch }))}
      />
    </TaskDialog>
  )
}
