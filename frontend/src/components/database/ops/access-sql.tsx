"use client"

import { useConfirm } from "@/components/confirm-dialog"
import {
  CardsSkeleton,
  CouldNotRead,
  NotUpdating,
  staleOf,
} from "@/components/database/home/blocks"
import { read } from "@/components/database/home/read"
import { ProtectedTag, SectionFrame } from "@/components/database/kit"
import { AccountPanel, dropAccountRequest } from "@/components/database/ops/access-account"
import {
  accountKey,
  accountTags,
  filterAccounts,
  grantSummary,
  grantsByAccount,
  isOwnAccount,
  type AccountShow,
} from "@/components/database/ops/access-model"
import { ChangePassword, NewAccount } from "@/components/database/ops/access-new"
import { useOpenAccount } from "@/components/database/ops/access-panel"
import { AccountMark, AccountName } from "@/components/database/ops/access-parts"
import type {
  DbGrants,
  DbPrivileges,
  DbRole,
  DbRoles,
} from "@/components/database/ops/access-types"
import { ServerDown, isDown } from "@/components/database/ops/performance-parts"
import { useFocusReturn } from "@/components/database/redis/use-focus-return"
import { useDatabase } from "@/components/database/shell/database-context"
import { useColumnWidth } from "@/components/deploy/settings/use-column-width"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { FormNote } from "@/components/form"
import { Key, Trash, UserPlus } from "@/components/icons"
import { SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyState, Notice } from "@/components/state"
import { ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { VerbMenu, type Verb } from "@/components/verbs"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { plural } from "@/lib/format"
import { useMemo, useState } from "react"

/** The width of the list from which an account's tags stand beside its name rather than under it. */
const BESIDE_FROM = 640

/**
 * A SQL server's accounts: who can sign in, what each is, and what each was
 * given.
 *
 * List filters select every account, the administrators, the ones with a session open now, the ones
 * that cannot sign in. An account is a card — its initials in its name's own
 * hue, what its grants add up to, what it is as tags — and opening one puts
 * its attributes and its grants in a panel beside the list (`?account=`).
 *
 * The account this connection signs in with is marked wherever it is drawn,
 * and the controls that would lock the dashboard out of the server are not
 * offered on it. Nothing that writes is drawn for a role without
 * `system.admin`, or on a protected connection.
 */
export function SqlAccess() {
  const { id, conn, engine, readOnly, status, param, select } = useDatabase()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const account = useOpenAccount()
  // The panel and every dialog here are opened by state: this hands the
  // keyboard back to the row or the button that opened one.
  useFocusReturn()
  const down = isDown(status.state)
  const [search, setSearch] = useState("")
  const [creating, setCreating] = useState(false)
  const [password, setPassword] = useState<DbRole>()
  const [frame, width] = useColumnWidth<HTMLDivElement>()
  const show = (param("show") || "") as AccountShow
  const opened = param("account")
  const openedHost = param("host")

  const roles = usePoll(
    (signal) =>
      read<DbRoles>(
        `/databases/${id}/server/roles`,
        (answer) => Array.isArray(answer.roles),
        undefined,
        signal,
      ),
    30_000,
    [id],
    { enabled: !down },
  )
  const matrix = engine.can("privileges")
  const privileges = usePoll(
    (signal) =>
      read<DbPrivileges>(
        `/databases/${id}/server/privileges`,
        (answer) => Array.isArray(answer.levels),
        undefined,
        signal,
      ),
    0,
    [id],
    { enabled: !down },
  )
  const grants = usePoll(
    (signal) =>
      read<DbGrants>(
        `/databases/${id}/server/grants`,
        (answer) => Array.isArray(answer.grants),
        undefined,
        signal,
      ),
    60_000,
    [id],
    { enabled: !down && matrix },
  )
  const databases = usePoll(
    (signal) =>
      read<{ name: string }[]>(
        `/databases/${id}/schemas`,
        (answer) => Array.isArray(answer),
        undefined,
        signal,
      ),
    0,
    [id],
    { enabled: creating },
  )

  const list = useMemo(() => roles.data?.roles ?? [], [roles.data])
  const held = useMemo(() => grantsByAccount(grants.data?.grants ?? []), [grants.data])
  const shown = filterAccounts(list, show, search)
  const mayEdit = can("system.admin") && !readOnly
  const mayDrop = mayEdit && can("destructive")
  const editable = privileges.data?.editable ?? []
  const refresh = () => {
    roles.refresh()
    grants.refresh()
  }
  const verbsFor = (role: DbRole): Verb[] => {
    const own = isOwnAccount(role, conn)
    return [
      ...(mayEdit && editable.includes("password")
        ? [
            {
              key: "password",
              label: "Change password",
              icon: Key,
              run: () => setPassword(role),
            },
          ]
        : []),
      ...(mayDrop && !own && !role.system
        ? [
            {
              key: "drop",
              label: "Drop account",
              icon: Trash,
              danger: true,
              run: () => confirm(dropAccountRequest(id, role, refresh)),
            },
          ]
        : []),
    ]
  }

  if (down) {
    return (
      <SectionFrame section="access">
        <ServerDown what="Its accounts" />
      </SectionFrame>
    )
  }

  const data = roles.data
  const beside = width >= BESIDE_FROM

  return (
    <SectionFrame section="access">
      <Panel plain aria-label="Accounts" ref={frame}>
        <PanelHeader
          title="Accounts"
          actions={
            <>
              {staleOf(roles) && <NotUpdating error={staleOf(roles)!} />}
              {readOnly && <ProtectedTag />}
              <SearchInput
                aria-label="Filter the accounts"
                placeholder="Filter by name"
                value={search}
                onChange={(event) => setSearch(event.target.value)}
                containerClassName="w-40 sm:w-52"
              />
              {mayEdit && data?.supported && (
                <Button size="sm" onClick={() => setCreating(true)}>
                  <UserPlus />
                  New account
                </Button>
              )}
            </>
          }
        />
        <ChipStrip role="group" aria-label="Filter accounts" className="px-1 py-2">
          {(
            [
              ["", "All accounts"],
              ["admins", "Administrators"],
              ["sessions", "Signed in now"],
              ["blocked", "Cannot sign in"],
            ] as const
          ).map(([key, label]) => (
            <FilterChip
              key={key}
              selected={show === key}
              onClick={() => select({ show: key || null })}
            >
              {label}
            </FilterChip>
          ))}
        </ChipStrip>
        <PanelBody>
          {!data ? (
            roles.error ? (
              <CouldNotRead what="the accounts" error={roles.error} onRetry={roles.refresh} />
            ) : (
              <CardsSkeleton count={4} />
            )
          ) : !data.supported ? (
            <Notice title="The accounts cannot be listed">
              {data.reason ?? "This server does not list its accounts to this connection."}
            </Notice>
          ) : list.length === 0 ? (
            <EmptyState
              title="The server lists no account"
              description="An account is what an application signs in with. Make one, and give it the database it needs."
              action={
                mayEdit && (
                  <Button size="sm" variant="outline" onClick={() => setCreating(true)}>
                    <UserPlus />
                    New account
                  </Button>
                )
              }
            />
          ) : shown.length === 0 ? (
            <p className="py-6 text-center text-body text-muted-foreground">
              No account matches.{" "}
              <button
                type="button"
                className="rounded-sm underline focus-ring"
                onClick={() => {
                  setSearch("")
                  select({ show: null })
                }}
              >
                Show every account
              </button>
            </p>
          ) : (
            <div className="animate-rise space-y-3">
              {readOnly && (
                <FormNote>
                  This connection is protected: accounts and grants are read here, and changed from
                  a connection that is not.
                </FormNote>
              )}
              <ChoiceList>
                {shown.map((role) => {
                  const own = isOwnAccount(role, conn)
                  const key = accountKey(role)
                  const tags = (
                    <>
                      {accountTags(role, own).map((tag) => (
                        <Tag key={tag.word} tone={tag.tone}>
                          {tag.word}
                        </Tag>
                      ))}
                      {role.connections > 0 && (
                        <span className="numeric text-hint text-muted-foreground">
                          {plural(role.connections, "session")}
                        </span>
                      )}
                    </>
                  )
                  const verbs = verbsFor(role)
                  return (
                    <ChoiceRow
                      key={key}
                      onSelect={() => account.open(role.name, role.host)}
                      verb={`Open ${key}`}
                      leading={<AccountMark name={role.name} />}
                      title={<AccountName name={role.name} host={role.host} />}
                      description={
                        matrix && !grants.data && !grants.error
                          ? undefined
                          : grantSummary(role, held.get(key) ?? held.get(role.name) ?? [])
                      }
                      trailing={
                        beside ? (
                          <span className="flex items-center gap-2.5">{tags}</span>
                        ) : undefined
                      }
                      actions={
                        verbs.length > 0 ? (
                          <VerbMenu verbs={verbs} label={`Actions for ${key}`} />
                        ) : undefined
                      }
                    >
                      {!beside && (
                        <span className="flex flex-wrap items-center gap-x-2.5 gap-y-1 pl-12">
                          {tags}
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
        <AccountPanel
          key={`${opened}@${openedHost}`}
          name={opened}
          host={openedHost || undefined}
          roles={list}
          privileges={privileges.data}
          confirm={confirm}
          onChanged={refresh}
          onClose={account.close}
        />
      )}
      {creating && (
        <NewAccount
          roles={list}
          editable={editable}
          databases={(databases.data ?? []).map((one) => one.name)}
          onCreated={refresh}
          onClose={() => setCreating(false)}
        />
      )}
      {password && (
        <ChangePassword
          role={password}
          own={isOwnAccount(password, conn)}
          onChanged={refresh}
          onClose={() => setPassword(undefined)}
        />
      )}
      {dialog}
    </SectionFrame>
  )
}
