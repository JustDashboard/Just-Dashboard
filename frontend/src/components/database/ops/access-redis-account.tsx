"use client"

import { useEffect, useMemo, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { useConfirm } from "@/components/confirm-dialog"
import { SaveBarProvider } from "@/components/deploy/settings/save-bar"
import { SettingForm, SettingSection } from "@/components/deploy/settings/setting-card"
import { FormSections } from "@/components/form"
import { Trash } from "@/components/icons"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Skeleton } from "@/components/ui/skeleton"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu, type Verb } from "@/components/verbs"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { errorMessage, put } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { CouldNotRead } from "@/components/database/home/blocks"
import { read } from "@/components/database/home/read"
import { ProtectedTag } from "@/components/database/kit"
import { useAccessFilter } from "@/components/database/ops/access-filter"
import {
  aclChanged,
  aclDraftOf,
  aclRequest,
  aclSummary,
  filterAclUsers,
  type AclDraft,
  type AclShow,
} from "@/components/database/ops/access-model"
import { AccountMark, AccountName } from "@/components/database/ops/access-parts"
import { AclFields, redisDropRequest } from "@/components/database/ops/access-redis-parts"
import type { RedisACL, RedisACLChange, RedisACLUser } from "@/components/database/ops/access-types"
import { accessHref } from "@/components/database/ops/account-address"
import {
  AccountFacts,
  AccountFrame,
  AccountPane,
  NoSuchAccount,
  type AccountList,
  type AccountView,
} from "@/components/database/ops/account-frame"
import { ServerDown, isDown } from "@/components/database/ops/performance-parts"
import { useFocusReturn } from "@/components/database/redis/use-focus-return"
import { useDatabase } from "@/components/database/shell/database-context"

const SHOW_WORDS: Record<AclShow, string> = {
  "": "",
  enabled: "switched on",
  unrestricted: "unrestricted",
  open: "any password",
}

/** How a user's password stands, as its line says it. The server never sends one back. */
function passwordWords(user: RedisACLUser): string {
  if (user.noPassword) return "accepts any password"
  if (user.passwords === 0) return "no password set, so nothing signs in as it"
  return user.passwords === 1 ? "1 password set" : `${user.passwords} passwords set`
}

/**
 * One ACL user of a key–value server, as a page: whether it may connect, and
 * the rule it is held to.
 *
 * Two views. Rule is the reading — the rule as the server holds it, and its
 * commands, keys and channels each on a line of their own. Settings is the
 * form that rewrites it, on the page's one save bar; only the part of the
 * rule that was edited is sent. A reader who may not change a user has the
 * reading alone, with no strip.
 *
 * The user the dashboard connects as is marked, and what would cut the
 * dashboard off — switching it off, deleting it, taking commands or keys away
 * from it — is drawn held, with the reason.
 */
export function RedisAccount({ name }: { name: string }) {
  const { id, readOnly, status } = useDatabase()
  const { can } = useAuth()
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  useFocusReturn()
  const down = isDown(status.state)
  const filter = useAccessFilter(id)
  const show = filter.show as AclShow
  const [unsaved, setUnsaved] = useState(0)
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
  const all = useMemo(() => acl.data?.users ?? [], [acl.data])
  const user = all.find((one) => one.name === name)
  const mayEdit = can("system.admin") && !readOnly
  const mayDelete = mayEdit && can("destructive")
  const shown = filterAclUsers(all, show)

  const list: AccountList = {
    label: "Users",
    accounts: shown.map((one) => ({
      key: one.name,
      name: one.name,
      line: !one.enabled
        ? "switched off"
        : one.noPassword
          ? "any password"
          : one.unrestricted
            ? "unrestricted"
            : aclSummary(one),
      tone: one.enabled && one.noPassword ? ("warning" as const) : undefined,
    })),
    loading: acl.loading && !down,
    caption: down
      ? "The server is not running"
      : acl.data
        ? show
          ? `${shown.length} of ${all.length} · ${SHOW_WORDS[show]}`
          : plural(all.length, "user")
        : "Reading the users…",
    filtered: Boolean(show),
    onClearFilters: () => filter.setShow(""),
    show,
  }

  const views: AccountView[] = [
    { key: "grants", label: "Rule" },
    ...(mayEdit ? [{ key: "settings" as const, label: "Settings", unsaved: unsaved > 0 }] : []),
  ]

  const verbs: Verb[] =
    user && mayDelete && !user.self && !user.system
      ? [
          {
            key: "delete",
            label: "Delete user",
            icon: Trash,
            danger: true,
            run: () =>
              confirm(
                redisDropRequest(id, user, () => {
                  acl.refresh()
                  router.replace(accessHref(id, show))
                }),
              ),
          },
        ]
      : []

  const mark = <AccountMark name={name} />
  const title = <AccountName name={name} className="text-title" />
  const identity = user ? (
    <HostIdentity
      label={`About ${user.name}`}
      slot="account-identity"
      className="animate-rise border-b-0 pb-0"
      logo={mark}
      title={title}
      state={
        <>
          <Status
            tone={user.enabled ? "running" : "stopped"}
            label={user.enabled ? "switched on" : "switched off"}
          />
          {user.self && <Tag>this connection</Tag>}
          {user.system && <Tag>default</Tag>}
          {user.noPassword && <Tag tone="warning">any password</Tag>}
          {readOnly && <ProtectedTag />}
        </>
      }
      facts={
        <AccountFacts>
          <span className="min-w-0 truncate font-mono text-foreground" title={aclSummary(user)}>
            {aclSummary(user)}
          </span>
          <FactDot />
          <span className="numeric shrink-0">{passwordWords(user)}</span>
        </AccountFacts>
      }
      menu={
        verbs.length > 0 ? <VerbMenu verbs={verbs} label={`Actions for ${user.name}`} /> : false
      }
    />
  ) : (
    <HostIdentity
      slot="account-identity"
      className="border-b-0 pb-0"
      logo={mark}
      title={title}
      state={
        down || acl.data || acl.error ? undefined : (
          <span className="flex text-xs">
            <TextShimmer>Reading the user…</TextShimmer>
          </span>
        )
      }
      facts={<span>&nbsp;</span>}
      menu={false}
    />
  )

  const gone = (
    <NoSuchAccount name={name} noun="user">
      It may have been deleted, or the server restarted without an ACL file to keep it.
    </NoSuchAccount>
  )

  return (
    <AccountFrame
      name={name}
      current={name}
      list={list}
      identity={identity}
      views={views}
      unsaved={unsaved}
    >
      {(tab) => (
        <>
          <AccountPane view="grants">
            {down ? (
              <ServerDown what="Its users" />
            ) : !acl.data ? (
              acl.error ? (
                <CouldNotRead what="the user" error={acl.error} onRetry={acl.refresh} />
              ) : (
                <Panel plain aria-hidden>
                  <PanelHeader title="Rule" />
                  <PanelBody className="space-y-2.5 pt-3">
                    {[72, 34, 28, 22].map((width) => (
                      <Skeleton key={width} className="h-6" style={{ width: `${width}%` }} />
                    ))}
                  </PanelBody>
                </Panel>
              )
            ) : !user ? (
              gone
            ) : (
              <RuleReading user={user} />
            )}
          </AccountPane>
          {mayEdit && (
            <AccountPane view="settings" keep>
              {down ? (
                <ServerDown what="Its users" />
              ) : user ? (
                <RuleSettings
                  key={user.name}
                  user={user}
                  shown={tab === "settings"}
                  onPending={setUnsaved}
                  onSaved={acl.refresh}
                />
              ) : acl.data ? (
                gone
              ) : acl.error ? (
                <CouldNotRead what="the user" error={acl.error} onRetry={acl.refresh} />
              ) : null}
            </AccountPane>
          )}
          {dialog}
        </>
      )}
    </AccountFrame>
  )
}

/** One part of a rule on its line: what it is, then each rule as the server writes it. */
function RulePart({ label, rules, none }: { label: string; rules: string[]; none: string }) {
  return (
    <div className="flex min-h-11 min-w-0 items-center gap-4 py-2">
      <dt className="w-24 shrink-0 text-body font-medium">{label}</dt>
      <dd className="flex min-w-0 flex-1 flex-wrap gap-x-3 gap-y-1">
        {rules.length === 0 ? (
          <span className="text-body text-muted-foreground">{none}</span>
        ) : (
          rules.map((rule, index) => (
            <code key={`${rule}:${index}`} className="font-mono text-xs">
              {rule}
            </code>
          ))
        )}
      </dd>
    </div>
  )
}

/** What the user is held to, read: the server's own line, then its three parts. */
function RuleReading({ user }: { user: RedisACLUser }) {
  return (
    <Panel plain aria-label="Rule" className="animate-rise">
      <PanelHeader title="Rule" />
      <PanelBody className="space-y-3 pt-3">
        <Well
          aria-label="The rule as the server holds it"
          className="text-hint leading-relaxed break-words whitespace-pre-wrap"
        >
          {user.rule || "no rule"}
        </Well>
        <dl className="divide-y divide-hairline">
          <RulePart label="Commands" rules={user.commands ?? []} none="none" />
          <RulePart label="Keys" rules={user.keys ?? []} none="none" />
          <RulePart label="Channels" rules={user.channels ?? []} none="none" />
        </dl>
      </PanelBody>
    </Panel>
  )
}

/**
 * The form that rewrites a user's rule. Only what was edited is sent; the
 * route leaves an absent part as it is, so a change of the key patterns
 * cannot touch the password.
 */
function RuleSettings({
  user,
  shown,
  onPending,
  onSaved,
}: {
  user: RedisACLUser
  /** The view is on screen: its edits are on the save bar only then. */
  shown: boolean
  onPending: (count: number) => void
  onSaved: () => void
}) {
  const { id, href } = useDatabase()
  const [edits, setEdits] = useState<Partial<AclDraft>>({})
  const [saving, setSaving] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const [notice, setNotice] = useState<string>()
  const draft = { ...aclDraftOf(user), ...edits }
  const request = aclRequest(user, draft)
  const changed = aclChanged(request)
  const changes = Object.keys(request).length
  useEffect(() => {
    onPending(changes)
  }, [changes, onPending])
  const held = user.self
    ? "The dashboard connects as this user. It stays switched on, and its keys and commands stay as they are: changing them would cut the dashboard off from the server."
    : undefined

  const save = async () => {
    setSaving(true)
    setRefusal(undefined)
    try {
      const answer = await put<RedisACLChange>(
        `/databases/${id}/redis/acl/${encodeURIComponent(user.name)}`,
        request,
      )
      notify.success(`Saved ${user.name}`, {
        description: answer.persisted ? undefined : "Kept until the server restarts.",
      })
      setNotice(answer.notice)
      setEdits({})
      onSaved()
      return true
    } catch (err) {
      setRefusal(errorMessage(err))
      return false
    } finally {
      setSaving(false)
    }
  }

  return (
    // The room at the foot is the save bar's, so it never lies over the last field.
    <div className="mx-auto w-full max-w-3xl min-w-0 animate-rise space-y-6 pb-20">
      <SaveBarProvider inline>
        {notice && (
          <Notice tone="warning" title="The server said">
            {notice}{" "}
            {user.self && (
              <Link
                href={`${href("settings")}#connection`}
                className="rounded-sm underline focus-ring"
              >
                Open Settings
              </Link>
            )}
          </Notice>
        )}
        <FormSections>
          <SettingForm
            name="The user's rule"
            canEdit={shown}
            dirty={changed}
            changes={changes}
            saving={saving}
            applies="immediately"
            onSave={save}
            onDiscard={() => {
              setEdits({})
              setRefusal(undefined)
            }}
            error={refusal && <>Nothing was changed. {refusal}</>}
          >
            <SettingSection title="Rule">
              <AclFields
                draft={draft}
                exists
                tips
                held={held}
                onChange={(patch) => {
                  setEdits((now) => ({ ...now, ...patch }))
                  setRefusal(undefined)
                }}
              />
            </SettingSection>
          </SettingForm>
        </FormSections>
      </SaveBarProvider>
    </div>
  )
}
