"use client"

import { useCallback, useEffect, useId, useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import { Key, Trash } from "@/components/icons"
import { ApiError, errorMessage, put } from "@/lib/api"
import { calendarDate, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { SaveBarProvider } from "@/components/deploy/settings/save-bar"
import { SettingForm, SettingSection } from "@/components/deploy/settings/setting-card"
import { Field, FieldRow, FormNote, FormSections, OptionList, OptionRow } from "@/components/form"
import { HostIdentity } from "@/components/metrics/host-identity"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu, type Verb } from "@/components/verbs"
import { CouldNotRead } from "@/components/database/home/blocks"
import { read } from "@/components/database/home/read"
import { ProtectedTag } from "@/components/database/kit"
import { dropAccountRequest } from "@/components/database/ops/access-drop"
import { useAccessFilter } from "@/components/database/ops/access-filter"
import { GrantsMatrix, ServerGrants } from "@/components/database/ops/access-grants"
import {
  ATTRIBUTES,
  accountCounts,
  accountKey,
  accountTags,
  attributeValue,
  filterAccounts,
  grantSummary,
  isOwnAccount,
  lockoutReason,
  sessionWords,
  signsIn,
  type AccountShow,
  type AttributeField,
} from "@/components/database/ops/access-model"
import { ChangePassword } from "@/components/database/ops/access-password"
import { AccountMark, AccountName } from "@/components/database/ops/access-parts"
import { AccountSessions } from "@/components/database/ops/access-sessions"
import type {
  DbPrivileges,
  DbRole,
  DbRoleAlterRequest,
  DbRoleDetail,
  DbRoles,
} from "@/components/database/ops/access-types"
import { accessHref, accountHref } from "@/components/database/ops/account-address"
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

const SHOW_WORDS: Record<AccountShow, string> = {
  "": "",
  admins: "administrators",
  sessions: "signed in now",
  blocked: "cannot sign in",
}

/** What an account is, in the rail's few words. */
function railLine(role: DbRole, own: boolean): { line: string; tone?: "warning" } {
  if (role.locked) return { line: "locked", tone: "warning" }
  const words = [
    own ? "this connection" : "",
    role.system ? "system" : "",
    role.superuser ? "administrator" : "",
    !role.login ? "no login" : "",
  ].filter(Boolean)
  return { line: words.join(" · ") || (role.connections > 0 ? "signed in" : "account") }
}

/**
 * One account of a SQL server, as a page: what it is, what it holds, who is
 * signed in as it, and what it may do on the server — each editable where the
 * engine and the role allow.
 *
 * It opens on the account's line: its mark and name, whether a session is
 * open as it now, what it is as tags, and what its grants add up to. The one
 * command is a new password; dropping it is in the menu, and neither is drawn
 * for a role that may not, on a protected connection, or — the drop — on the
 * account this connection signs in with.
 *
 * Grants are staged on the matrix and reviewed as the server's statements
 * before any runs. Settings are a form on the page's one save bar, which
 * sends only the switches that were thrown: an account's other attributes are
 * never restated and so never reset. Both views stay mounted, so work staged
 * in one is still there after a look at another, and the page asks before it
 * is left over any of it.
 */
export function SqlAccount({ name, host }: { name: string; host?: string }) {
  const { id, conn, engine, readOnly, status } = useDatabase()
  const { can } = useAuth()
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  // Every dialog here is opened by state: this hands the keyboard back to
  // the control that opened one.
  useFocusReturn()
  const down = isDown(status.state)
  const filter = useAccessFilter(id)
  const show = filter.show as AccountShow
  const [password, setPassword] = useState(false)
  const [staged, setStaged] = useState(0)
  const [edited, setEdited] = useState(0)
  const [signedIn, setSignedIn] = useState<number>()

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
  const detail = usePoll(
    (signal) =>
      read<DbRoleDetail>(
        `/databases/${id}/server/roles/${encodeURIComponent(name)}`,
        (answer) => typeof answer.name === "string" && Array.isArray(answer.grants),
        host !== undefined ? { host } : undefined,
        signal,
      ),
    0,
    [id, name, host],
    { enabled: !down },
  )

  const all = useMemo(() => roles.data?.roles ?? [], [roles.data])
  const data = detail.data
  const key = accountKey({ name, host })
  const listed = all.find((role) => accountKey(role) === key)
  const missing =
    !data && detail.error instanceof ApiError && [400, 404].includes(detail.error.status)
  // An engine whose accounts are `name@host` needs the host to tell them
  // apart, and an address typed without one names the first of that name.
  const namesake =
    host === undefined ? all.find((role) => role.name === name && role.host !== undefined) : null
  useEffect(() => {
    if (missing && namesake) router.replace(accountHref(id, namesake))
  }, [missing, namesake, router, id])

  const own = isOwnAccount({ name }, conn)
  const mayEdit = can("system.admin") && !readOnly
  const mayDrop = mayEdit && can("destructive") && !own && !data?.system
  const editable = data?.editable ?? []
  const sessions = engine.can("sessions") && !engine.can("clickhouseViews")
  const connections = listed?.connections ?? data?.connections ?? 0

  const refreshRoles = roles.refresh
  const refreshDetail = detail.refresh
  const changed = useCallback(() => {
    refreshDetail()
    refreshRoles()
  }, [refreshDetail, refreshRoles])

  const shown = useMemo(() => filterAccounts(all, show, filter.search), [all, show, filter.search])
  const list: AccountList = {
    label: "Accounts",
    accounts: shown.map((role) => ({
      key: accountKey(role),
      name: role.name,
      host: role.host,
      ...railLine(role, isOwnAccount(role, conn)),
      live: role.connections > 0,
      sessions: role.connections,
    })),
    loading: roles.loading,
    search: {
      value: filter.search,
      onChange: filter.setSearch,
      placeholder: "Filter by name",
    },
    caption: roles.data
      ? show || filter.search
        ? `${shown.length} of ${accountCounts(all).all}${show ? ` · ${SHOW_WORDS[show]}` : ""}`
        : `${accountCounts(all).all} ${accountCounts(all).all === 1 ? "account" : "accounts"}`
      : "Reading the accounts…",
    filtered: Boolean(show),
    onClearFilters: () => filter.setShow(""),
    show,
  }

  // An account that is not there, or could not be read, has nothing to show in three views.
  const unread = !data && (down || Boolean(detail.error))
  const views: AccountView[] = unread
    ? [{ key: "grants", label: "Grants" }]
    : [
        { key: "grants", label: "Grants", unsaved: staged > 0 },
        ...(sessions
          ? [{ key: "sessions" as const, label: "Sessions", count: signedIn ?? connections }]
          : []),
        { key: "settings", label: "Settings", unsaved: edited > 0 },
      ]

  const verbs: Verb[] =
    mayDrop && data
      ? [
          {
            key: "drop",
            label: "Drop account",
            icon: Trash,
            danger: true,
            run: () =>
              confirm(
                dropAccountRequest(id, data, () => {
                  refreshRoles()
                  router.replace(accessHref(id, show))
                }),
              ),
          },
        ]
      : []

  const mark = <AccountMark name={name} />
  const title = <AccountName name={name} host={host} className="text-title" />
  const summary = data ? grantSummary(data, data.grants) : ""
  const identity = data ? (
    <HostIdentity
      label={`About ${key}`}
      slot="account-identity"
      className="animate-rise border-b-0 pb-0"
      logo={mark}
      title={title}
      state={
        <>
          <Status
            tone={connections > 0 ? "running" : signsIn(data) ? "unknown" : "stopped"}
            live={connections > 0}
            label={signsIn(data) || connections > 0 ? sessionWords(connections) : "cannot sign in"}
          />
          {accountTags(data, own).map((tag) => (
            <Tag key={tag.word} tone={tag.tone}>
              {tag.word}
            </Tag>
          ))}
          {readOnly && <ProtectedTag />}
        </>
      }
      facts={
        <AccountFacts>
          <span className="min-w-0 truncate text-foreground" title={summary}>
            {summary}
          </span>
        </AccountFacts>
      }
      command={
        mayEdit && editable.includes("password") ? (
          <Button size="sm" onClick={() => setPassword(true)}>
            <Key className="size-3.5" />
            Change password
          </Button>
        ) : undefined
      }
      menu={verbs.length > 0 ? <VerbMenu verbs={verbs} label={`Actions for ${key}`} /> : false}
    />
  ) : (
    <HostIdentity
      slot="account-identity"
      className="border-b-0 pb-0"
      logo={mark}
      title={title}
      state={
        down || missing || detail.error ? undefined : (
          <span className="flex text-xs">
            <TextShimmer>Reading the account…</TextShimmer>
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
      host={host}
      current={key}
      list={list}
      identity={identity}
      views={views}
      unsaved={staged + edited}
    >
      {(tab) => (
        <>
          <AccountPane view="grants" keep>
            {down ? (
              <ServerDown what="Its accounts" />
            ) : !data ? (
              missing ? (
                <NoSuchAccount name={name} host={host} noun="account">
                  It may have been dropped, or the name in the link is misspelt.
                </NoSuchAccount>
              ) : detail.error ? (
                <CouldNotRead what="the account" error={detail.error} onRetry={detail.refresh} />
              ) : (
                <GrantsSkeleton />
              )
            ) : privileges.data?.supported && privileges.data.levels.length > 0 ? (
              <div className="animate-rise">
                <GrantsMatrix
                  detail={data}
                  levels={privileges.data.levels}
                  roles={all
                    .filter((role) => role.name !== name && !role.system)
                    .map((role) => role.name)
                    .filter((role, index, names) => names.indexOf(role) === index)}
                  editable={mayEdit}
                  onPending={setStaged}
                  onApplied={changed}
                />
              </div>
            ) : privileges.data || privileges.error ? (
              <Panel plain aria-label="Grants" className="animate-rise">
                <PanelHeader title="Grants" />
                <PanelBody className="space-y-3 pt-3">
                  <ServerGrants
                    privileges={data.grants
                      .filter((grant) => grant.level === "server")
                      .flatMap((grant) => grant.privileges)
                      .filter((privilege) => privilege.toUpperCase() !== "USAGE")}
                    superuser={data.superuser}
                  />
                  <FormNote>
                    This engine&rsquo;s grants are not listed object by object here.
                  </FormNote>
                </PanelBody>
              </Panel>
            ) : (
              <GrantsSkeleton />
            )}
          </AccountPane>
          {sessions && !unread && (
            <AccountPane view="sessions">
              {tab === "sessions" &&
                (down ? (
                  <ServerDown what="Its sessions" />
                ) : (
                  <AccountSessions name={name} onCount={setSignedIn} />
                ))}
            </AccountPane>
          )}
          {data && (
            <AccountPane view="settings" keep>
              <AccountSettings
                key={key}
                detail={data}
                own={own}
                mayEdit={mayEdit}
                shown={tab === "settings"}
                onPending={setEdited}
                onSaved={changed}
              />
            </AccountPane>
          )}
          {password && data && (
            <ChangePassword
              role={data}
              own={own}
              onChanged={changed}
              onClose={() => setPassword(false)}
            />
          )}
          {dialog}
        </>
      )}
    </AccountFrame>
  )
}

/** The grants view before the account has been read: its two heads and the rows to come. */
function GrantsSkeleton() {
  return (
    <div className="space-y-8" aria-hidden>
      {["Member of", "Grants"].map((title) => (
        <Panel plain key={title}>
          <PanelHeader title={title} />
          <PanelBody className="space-y-2.5 pt-3">
            {(title === "Grants" ? [52, 40, 64] : [24]).map((width) => (
              <Skeleton key={width} className="h-6" style={{ width: `${width}%` }} />
            ))}
          </PanelBody>
        </Panel>
      ))}
    </div>
  )
}

/** The attributes that say whether and how an account signs in; the rest are what it may do. */
const SIGN_IN: AttributeField[] = ["login", "locked"]

/**
 * What an account is on the server, as the page's settings: whether it signs
 * in and within what limits, and what it may do beyond its grants.
 *
 * One form on the page's one save bar. It sends only what was changed — a
 * switch thrown, a limit typed — so nothing else about the account is
 * restated. The dashboard's own account keeps the switches that would lock
 * the dashboard out, drawn and held, with the reason. A reader who may not
 * change an account is shown the same facts as words, not as controls.
 */
function AccountSettings({
  detail: data,
  own,
  mayEdit,
  shown,
  onPending,
  onSaved,
}: {
  detail: DbRoleDetail
  own: boolean
  mayEdit: boolean
  /** The view is on screen: its edits are on the save bar only then. */
  shown: boolean
  onPending: (count: number) => void
  onSaved: () => void
}) {
  const { id } = useDatabase()
  const field = useId()
  const [edits, setEdits] = useState<DbRoleAlterRequest>({})
  const [saving, setSaving] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const editable = data.editable
  const count = Object.keys(edits).length
  useEffect(() => {
    onPending(count)
  }, [count, onPending])

  // Where an engine both locks an account and has a login flag, they are one
  // fact said twice; the lock is the one drawn.
  const switches = ATTRIBUTES.filter(
    (one) =>
      editable.includes(one.field) && !(one.field === "login" && editable.includes("locked")),
  )
  const signIn = switches.filter((one) => SIGN_IN.includes(one.field))
  const powers = switches.filter((one) => !SIGN_IN.includes(one.field))
  const limits = editable.includes("connectionLimit") || editable.includes("validUntil")
  const set = (key: AttributeField, value: boolean) => {
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
      : data.connectionLimit > 0
        ? String(data.connectionLimit)
        : ""

  const save = async () => {
    setSaving(true)
    setRefusal(undefined)
    try {
      await put(`/databases/${id}/server/roles/${encodeURIComponent(data.name)}`, {
        ...edits,
        ...(data.host !== undefined ? { host: data.host } : {}),
      })
      notify.success(`Saved ${accountKey(data)}`)
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

  const switchRows = (list: typeof switches) => (
    <OptionList>
      {list.map((one) => {
        const now = attributeValue(data, one.field)
        const value = (edits[one.field] as boolean | undefined) ?? now
        const held = lockoutReason(one.field, !value, own)
        return (
          <OptionRow
            key={one.field}
            title={one.title}
            hint={held ?? one.hint}
            checked={value}
            disabled={Boolean(held)}
            tone={one.field === "superuser" && value ? "warning" : "default"}
            onCheckedChange={(next) => set(one.field, next)}
          />
        )
      })}
    </OptionList>
  )
  // The same facts as words, for a reader who may not change them.
  const said = (list: typeof switches): [string, string][] =>
    list.map((one) => [one.title, attributeValue(data, one.field) ? "Yes" : "No"])
  const settings = [...data.config, ...(data.configRedacted ?? []).map((one) => `${one} = hidden`)]
  // How it signs in, as the section's own line of data.
  const signInState = [
    data.authPlugin ? (
      <span key="plugin">
        by <span className="font-mono">{data.authPlugin}</span>
      </span>
    ) : null,
    data.validUntil ? (
      <span key="until" title={timestamp(data.validUntil)}>
        password until {calendarDate(data.validUntil)}
      </span>
    ) : null,
  ].filter(Boolean)

  return (
    // The room at the foot is the save bar's, so it never lies over the last field.
    <div className="mx-auto w-full max-w-3xl min-w-0 animate-rise space-y-6 pb-20">
      <SaveBarProvider inline>
        {data.notes?.map((note) => (
          <FormNote key={note}>{note}</FormNote>
        ))}
        <FormSections>
          <SettingForm
            name="Account attributes"
            canEdit={mayEdit && shown}
            dirty={count > 0}
            changes={count}
            saving={saving}
            applies="immediately"
            onSave={save}
            onDiscard={() => {
              setEdits({})
              setRefusal(undefined)
            }}
            error={refusal && <>Nothing was changed. {refusal}</>}
          >
            {(signIn.length > 0 || limits || signInState.length > 0) && (
              <SettingSection
                title="Signing in"
                state={
                  signInState.length > 0 && (
                    <span className="flex flex-wrap items-center gap-x-2">{signInState}</span>
                  )
                }
              >
                {mayEdit ? (
                  <>
                    {signIn.length > 0 && switchRows(signIn)}
                    {limits && (
                      <FieldRow>
                        {editable.includes("connectionLimit") && (
                          <Field
                            label="Sessions at once"
                            htmlFor={`${field}-limit`}
                            info="Empty for no limit."
                          >
                            <Input
                              id={`${field}-limit`}
                              value={limit === "-1" ? "" : limit}
                              placeholder="No limit"
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
                            info="Empty for no end."
                          >
                            <Input
                              id={`${field}-until`}
                              type="date"
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
                  </>
                ) : (
                  <Said
                    rows={[
                      ...said(signIn),
                      ...(editable.includes("connectionLimit")
                        ? ([
                            [
                              "Sessions at once",
                              data.connectionLimit > 0 ? String(data.connectionLimit) : "No limit",
                            ],
                          ] as [string, string][])
                        : []),
                      ...(editable.includes("validUntil")
                        ? ([
                            [
                              "Password valid until",
                              data.validUntil ? calendarDate(data.validUntil) : "No end",
                            ],
                          ] as [string, string][])
                        : []),
                    ]}
                  />
                )}
              </SettingSection>
            )}
            {powers.length > 0 && (
              <SettingSection title="On the server">
                {mayEdit ? switchRows(powers) : <Said rows={said(powers)} />}
              </SettingSection>
            )}
          </SettingForm>
          {settings.length > 0 && (
            <SettingSection title="Set for it">
              <ul className="divide-y divide-hairline" aria-label="Settings of the account">
                {settings.map((setting) => (
                  <li key={setting} className="py-2 font-mono text-xs wrap-anywhere">
                    {setting}
                  </li>
                ))}
              </ul>
            </SettingSection>
          )}
          {switches.length === 0 &&
            !limits &&
            settings.length === 0 &&
            signInState.length === 0 && (
              <FormNote>
                This engine keeps no attribute of an account to read or change here.
              </FormNote>
            )}
        </FormSections>
      </SaveBarProvider>
    </div>
  )
}

/** Facts a reader may not change, one to a line: what it is, and its word. */
function Said({ rows }: { rows: [string, string][] }) {
  return (
    <dl className="divide-y divide-hairline">
      {rows.map(([label, value]) => (
        <div key={label} className="flex min-w-0 items-center justify-between gap-6 py-2.5">
          <dt className="min-w-0 text-sm leading-snug font-medium">{label}</dt>
          <dd className="numeric shrink-0 text-body text-muted-foreground">{value}</dd>
        </div>
      ))}
    </dl>
  )
}
