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
import { useAccessFilterFrom } from "@/components/database/ops/access-filter"
import {
  accountDsn,
  accountNameProblem,
  aclDraftOf,
  aclRequest,
  aclSummary,
  filterAclUsers,
  type AclDraft,
  type AclShow,
} from "@/components/database/ops/access-model"
import {
  AccountMark,
  AccountName,
  SecretShown,
  SignInPicture,
} from "@/components/database/ops/access-parts"
import { AclFields, redisDropRequest, tagsOf } from "@/components/database/ops/access-redis-parts"
import { QuietWord, TAGS_BESIDE_FROM, TagsUnder } from "@/components/database/ops/access-row"
import type { RedisACL, RedisACLChange } from "@/components/database/ops/access-types"
import { accountHref } from "@/components/database/ops/account-address"
import { ServerDown, isDown } from "@/components/database/ops/performance-parts"
import { TaskDialog, databaseSubject } from "@/components/database/ops/settings-dialog"
import { useFocusReturn } from "@/components/database/redis/use-focus-return"
import { useDatabase } from "@/components/database/shell/database-context"
import { useColumnWidth } from "@/components/deploy/settings/use-column-width"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Field, FormFact, FormNote } from "@/components/form"
import { Trash, UserPlus } from "@/components/icons"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Notice } from "@/components/state"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { VerbMenu, type Verb } from "@/components/verbs"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { errorMessage, put } from "@/lib/api"
import { useId, useMemo, useState } from "react"

/**
 * A key–value server's ACL users: who may connect, and the rule each is held
 * to — which commands, on which keys, on which channels.
 *
 * List filters select every user, the ones switched on, the ones
 * held to nothing, the ones that accept any password. A user opens on a page
 * of its own (`access-redis-account.tsx`) with its rule as the server holds it
 * and the form that changes it; only the part of the rule that was edited is
 * sent.
 *
 * The user the dashboard connects as is marked, and what would cut the
 * dashboard off — switching it off, deleting it, taking commands or keys away
 * from it — is drawn held, with the reason.
 */
export function RedisAccess() {
  const { id, engine, readOnly, status, param, select } = useDatabase()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  useFocusReturn()
  const down = isDown(status.state)
  const [creating, setCreating] = useState(false)
  const [frame, width] = useColumnWidth<HTMLDivElement>()
  const show = (param("show") || "") as AclShow
  // The rail beside a user's page holds the same users this chip does.
  useAccessFilterFrom(id, show)
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
  const shown = filterAclUsers(users, show)

  if (down) {
    return (
      <SectionFrame section="access">
        <ServerDown what="Its users" />
      </SectionFrame>
    )
  }

  const beside = width >= TAGS_BESIDE_FROM
  const counted = {
    "": users.length,
    enabled: users.filter((user) => user.enabled).length,
    unrestricted: users.filter((user) => user.unrestricted).length,
    open: users.filter((user) => user.noPassword && user.enabled).length,
  }
  return (
    <SectionFrame section="access">
      {data?.supported && (
        <SignInPicture
          noun="user"
          accounts={users.map((user) => ({
            key: user.name,
            name: user.name,
            signsIn: user.enabled,
            own: user.self,
            admin: user.unrestricted,
          }))}
        />
      )}
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
        <ChipStrip role="group" aria-label="Filter accounts" className="px-1 py-2">
          {(
            [
              ["", "All users"],
              ["enabled", "Switched on"],
              ["unrestricted", "Unrestricted"],
              ["open", "Any password"],
            ] as const
          ).map(([key, label]) => (
            <FilterChip
              key={key}
              selected={show === key}
              onClick={() => select({ show: key || null })}
            >
              {label}
              {data?.supported && <ChipCount>{counted[key]}</ChipCount>}
            </FilterChip>
          ))}
        </ChipStrip>
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
                            run: () => confirm(redisDropRequest(id, user, acl.refresh)),
                          },
                        ]
                      : []
                  return (
                    <ChoiceRow
                      key={user.name}
                      href={accountHref(id, { name: user.name })}
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
                        <TagsUnder>
                          {user.self || user.system || !user.enabled || user.noPassword ? (
                            tagsOf(user)
                          ) : (
                            <QuietWord>switched on</QuietWord>
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
        cancelLabel={null}
        discardQuestion="Close? The password is not shown again."
        stayLabel="Go back"
        discardLabel="Close"
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
