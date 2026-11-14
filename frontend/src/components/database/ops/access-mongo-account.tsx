"use client"

import { useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import { useConfirm } from "@/components/confirm-dialog"
import { FormNote } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Cross, Key, Trash } from "@/components/icons"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu, type Verb } from "@/components/verbs"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { errorMessage, post } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { CouldNotRead } from "@/components/database/home/blocks"
import { nameHue } from "@/components/database/home/kinds"
import { read } from "@/components/database/home/read"
import { ProtectedTag } from "@/components/database/kit"
import { roleWord } from "@/components/database/ops/access-model"
import {
  RolePicker,
  UserPassword,
  mongoDropRequest,
  userTags,
} from "@/components/database/ops/access-mongo-parts"
import { AccountMark, AccountName } from "@/components/database/ops/access-parts"
import type { MongoRoleRef, MongoUser, MongoUsers } from "@/components/database/ops/access-types"
import { accessHref } from "@/components/database/ops/account-address"
import {
  AccountFacts,
  AccountFrame,
  AccountPane,
  NoSuchAccount,
  type AccountList,
} from "@/components/database/ops/account-frame"
import { ServerDown, isDown } from "@/components/database/ops/performance-parts"
import { useFocusReturn } from "@/components/database/redis/use-focus-return"
import { useDatabase } from "@/components/database/shell/database-context"

const userKey = (user: Pick<MongoUser, "user" | "db">) => `${user.db}.${user.user}`

/**
 * One user of a document database, as a page: where it is kept, how it signs
 * in, and the roles it holds on databases.
 *
 * A role is granted or taken at the press — nothing is staged — so the page
 * has one view and no strip: the roles as a list, each taken away by its own
 * control, and under them the picker that adds one. The one command is a new
 * password; dropping the user is in the menu, and is not offered on the user
 * the dashboard connects as, which also keeps its `root`.
 *
 * `db` is the database the user is kept in (`?host=`). An address without
 * one names the first user of that name.
 */
export function MongoAccount({ name, db }: { name: string; db?: string }) {
  const { id, readOnly, status } = useDatabase()
  const { can } = useAuth()
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  useFocusReturn()
  const down = isDown(status.state)
  const [password, setPassword] = useState(false)
  const [busy, setBusy] = useState<string>()
  const [refusal, setRefusal] = useState<string>()
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
  const all = useMemo(() => users.data?.users ?? [], [users.data])
  const user = all.find((one) => one.user === name && (db === undefined || one.db === db))
  const home = db ?? user?.db
  const mayEdit = can("system.admin") && !readOnly
  const mayDrop = mayEdit && can("destructive")

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
      users.refresh()
    } catch (err) {
      setRefusal(errorMessage(err))
    } finally {
      setBusy(undefined)
    }
  }

  const list: AccountList = {
    label: "Users",
    accounts: all.map((one) => ({
      key: userKey(one),
      name: one.user,
      host: one.db,
      line:
        [one.self ? "this connection" : "", one.superuser ? "administrator" : ""]
          .filter(Boolean)
          .join(" · ") || (one.roles.length > 0 ? plural(one.roles.length, "role") : "no role"),
    })),
    loading: users.loading && !down,
    caption: down
      ? "The server is not running"
      : users.data
        ? plural(all.length, "user")
        : "Reading the users…",
    filtered: false,
    onClearFilters: () => {},
  }

  const verbs: Verb[] =
    user && mayDrop && !user.self
      ? [
          {
            key: "drop",
            label: "Drop user",
            icon: Trash,
            danger: true,
            run: () =>
              confirm(
                mongoDropRequest(id, user, () => {
                  users.refresh()
                  router.replace(accessHref(id))
                }),
              ),
          },
        ]
      : []

  const mark = <AccountMark name={name} />
  const title = <AccountName name={name} host={home} className="text-title" />
  const identity = user ? (
    <HostIdentity
      label={`About ${user.user}`}
      slot="account-identity"
      className="animate-rise border-b-0 pb-0"
      logo={mark}
      title={title}
      state={
        <>
          <Status tone="running" label="can sign in" />
          {userTags(user)}
          {readOnly && <ProtectedTag />}
        </>
      }
      facts={
        <AccountFacts>
          <span className="shrink-0">
            kept in <span className="font-mono text-foreground">{user.db}</span>
          </span>
          {user.mechanisms.length > 0 && (
            <>
              <FactDot />
              <span className="min-w-0 truncate">
                signs in by <span className="font-mono">{user.mechanisms.join(", ")}</span>
              </span>
            </>
          )}
          <FactDot />
          <span className="numeric shrink-0">
            {user.roles.length > 0 ? `holds ${plural(user.roles.length, "role")}` : "holds no role"}
          </span>
        </AccountFacts>
      }
      command={
        mayEdit ? (
          <Button size="sm" onClick={() => setPassword(true)}>
            <Key className="size-3.5" />
            Change password
          </Button>
        ) : undefined
      }
      menu={
        verbs.length > 0 ? <VerbMenu verbs={verbs} label={`Actions for ${user.user}`} /> : false
      }
    />
  ) : (
    <HostIdentity
      slot="account-identity"
      className="border-b-0 pb-0"
      logo={mark}
      title={title}
      state={
        down || users.data || users.error ? undefined : (
          <span className="flex text-xs">
            <TextShimmer>Reading the user…</TextShimmer>
          </span>
        )
      }
      facts={<span>&nbsp;</span>}
      menu={false}
    />
  )

  return (
    <AccountFrame
      name={name}
      host={home}
      current={user ? userKey(user) : `${home ?? ""}.${name}`}
      list={list}
      identity={identity}
      views={[{ key: "grants", label: "Roles" }]}
    >
      {() => (
        <>
          <AccountPane view="grants">
            {down ? (
              <ServerDown what="Its users" />
            ) : !users.data ? (
              users.error ? (
                <CouldNotRead what="the user" error={users.error} onRetry={users.refresh} />
              ) : (
                <Panel plain aria-hidden>
                  <PanelHeader title="Roles" />
                  <PanelBody className="space-y-2.5 pt-3">
                    {[38, 30].map((width) => (
                      <Skeleton key={width} className="h-6" style={{ width: `${width}%` }} />
                    ))}
                  </PanelBody>
                </Panel>
              )
            ) : !user ? (
              <NoSuchAccount name={name} host={db} noun="user">
                It may have been dropped, or the name in the link is misspelt.
              </NoSuchAccount>
            ) : (
              <Panel plain aria-label="Roles" className="animate-rise">
                <PanelHeader
                  title="Roles"
                  actions={
                    user.roles.length > 0 && (
                      <span className="numeric text-hint text-muted-foreground">
                        {user.roles.length}
                      </span>
                    )
                  }
                />
                <PanelBody className="space-y-4">
                  {user.roles.length === 0 ? (
                    <FormNote className="pt-3">
                      It holds no role, so it can sign in and do nothing.
                    </FormNote>
                  ) : (
                    <ul className="divide-y divide-hairline" aria-label="Roles it holds">
                      {user.roles.map((role) => (
                        <li key={roleWord(role)} className="flex h-11 min-w-0 items-center gap-3">
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
                      The dashboard connects as this user. A role taken from it is a page here that
                      stops working.
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
                </PanelBody>
              </Panel>
            )}
          </AccountPane>
          {password && user && (
            <UserPassword
              user={user}
              onChanged={users.refresh}
              onClose={() => setPassword(false)}
            />
          )}
          {dialog}
        </>
      )}
    </AccountFrame>
  )
}
