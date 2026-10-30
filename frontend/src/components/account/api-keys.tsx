"use client"

import { useRef, useState, useSyncExternalStore } from "react"
import {
  Check,
  ChevronDown,
  Copy,
  Key,
  Plus,
  RefreshClockwise,
  Rocket,
  Slash,
  Trash,
} from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { del, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { calendarDate, plural, relativeTime } from "@/lib/format"
import { keyProduct } from "@/lib/clients"
import type { ApiToken, DashboardUser, DbConnection, Role } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useCopy } from "@/hooks/use-copy"
import { useConfirm } from "@/components/confirm-dialog"
import { Field, FormNote } from "@/components/form"
import { Modal } from "@/components/modal"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { SearchInput, Section } from "@/components/page"
import { ProductLogo } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { EmptyNote, EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Checkbox } from "@/components/ui/checkbox"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { VerbActions, type Verb } from "@/components/verbs"
import {
  inKeyFilter,
  matchesKey,
  orderKeys,
  replacementRoles,
  type KeyFilter,
  type KeyOrder,
} from "@/components/account/key-list"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { ShellWords } from "@/components/deploy/run-evidence"
import { useNow } from "@/components/deploy/vocabulary"
import { cn } from "@/lib/utils"
import { RoleChoice } from "@/components/account/role-choice"
import { ROLE_HUE } from "@/components/account/capabilities"
import { expired, expiringSoon, usableKeys } from "@/components/account/access"
import { KeyMap } from "@/components/account/key-map"
import {
  clientConfig,
  deploymentsWithin,
  grantList,
  grantsOf,
  grantsWithin,
} from "@/components/database/fleet/mcp-server"
import { UserAvatar, displayNameOf } from "@/components/account/user-avatar"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle } from "@/components/choice-card"
import { Segments } from "@/components/deploy/settings/segments"
import { ProjectMark } from "@/components/deploy/project-mark"
import { useProjects } from "@/hooks/use-projects"
import {
  CLI_DOWNLOAD,
  CLI_ON_SERVER,
  DEPLOY_KEY_CAN,
  DEPLOY_KEY_CANNOT,
  DEPLOY_KEY_HOST_RULE,
  OPENAPI_DESCRIPTION,
  deployKeyBody,
  deployKeyReady,
  deployReach,
  jdLogin,
} from "@/components/account/deploy-keys"

export function useApiKeys() {
  return usePoll((signal) => get<ApiToken[]>("/tokens/", undefined, signal), 30000)
}

/**
 * The address a script would use, taken from the page rather than typed into
 * a config: it is the one URL guaranteed to reach this dashboard, because it
 * just did. Read after mount so the server render and the first client render
 * agree on the placeholder.
 */
export function useApiOrigin() {
  return useSyncExternalStore(
    subscribeNever,
    () => window.location.origin,
    () => "https://your-dashboard",
  )
}

/** The origin never changes under a mounted page; there is nothing to watch. */
function subscribeNever() {
  return () => {}
}

/** What the keys are for, as products, each once — for a reading that counts them. */
export function keyProducts(tokens: ApiToken[]) {
  return [...new Set(tokens.map((k) => keyProduct(k.name)).filter((id) => id !== undefined))]
}

export function curlExample(origin: string, key = "vpsd_…", scope?: ApiToken["scope"]) {
  if (scope === "mcp") return clientConfig("claude-code", origin, key)
  if (scope === "deploy") return jdLogin(origin, key)
  const path = scope === "metrics.read" ? "metrics/databases" : "system/host"
  return `curl -H "Authorization: Bearer ${key}" ${origin}/api/v1/${path}`
}

/** A key narrowed to one job, in the words a row or a node has room for. */
export function scopeWords(token: ApiToken) {
  if (token.scope === "metrics.read") return "metrics only"
  if (token.scope === "mcp") return "MCP only"
  if (token.scope === "deploy") return "deploy only"
  return undefined
}

/**
 * What an MCP key's replacement reaches: the databases the key reaches that a
 * key can still be given, and the deployments as the key had them — the
 * grant, and the deployments it was narrowed to, so a replacement is never a
 * key that reaches more, and the generated tools only if the key had them. A
 * database protected since, or forgotten, is refused
 * every call already, and the server would refuse the whole list for it.
 */
async function replacementGrants(token: ApiToken | undefined, role: Role) {
  const saved = await get<DbConnection[]>("/databases/")
  const open = new Set(saved.filter((conn) => !conn.readOnly).map((conn) => conn.id))
  const connections = grantList(grantsWithin(grantsOf(token), role !== "readonly")).filter(
    (grant) => open.has(grant.connectionId),
  )
  const deployments = deploymentsWithin(token?.deployments, role !== "readonly")
  if (connections.length === 0 && deployments === "") {
    throw new Error("None of the databases this key reaches can be given to a key any more.")
  }
  return {
    connections,
    deployments,
    deploymentIds: deployments === "" ? [] : [...(token?.deploymentIds ?? [])],
    operations: deployments !== "" && (token?.operations ?? false),
  }
}

/** The one line a key's state comes down to. */
function keyStatus(token: ApiToken, now: number) {
  if (token.revoked) return <Status tone="stopped" label="revoked" />
  if (expired(token, now)) return <Status tone="stopped" label="expired" />
  if (expiringSoon(token, now)) {
    return <Status tone="warning" label={`expires ${relativeTime(token.expiresAt)}`} />
  }
  if (token.expiresAt) {
    return <Status tone="running" label={`until ${calendarDate(token.expiresAt)}`} />
  }
  return <Status tone="running" label="no expiry" />
}

/**
 * One key, drawn as what holds it: a key named for GitHub Actions carries
 * GitHub's mark, one named for a cron job keeps the key glyph (`keyProduct`
 * reads the name the key was given, and guesses nothing). The prefix is the
 * part of the secret a log line shows, so it is the second line's literal;
 * an administrator reading everyone's keys also sees whose each one is.
 */
function KeyRow({
  token,
  now,
  owner,
  verbs,
}: {
  token: ApiToken
  now: number
  owner?: DashboardUser
  verbs: Verb[]
}) {
  const usage = token.lastUsedAt ? `used ${relativeTime(token.lastUsedAt)}` : "never used"
  // A key narrowed to one job says the job, where the role beneath it would
  // read as the key being able to do everything that role can.
  const can = scopeWords(token) ?? token.role
  return (
    <Row
      className="group"
      leading={
        <ProductLogo
          id={keyProduct(token.name)}
          size="sm"
          fallback={Key}
          className={token.revoked || expired(token, now) ? "opacity-50 grayscale" : undefined}
        />
      }
      title={token.name}
      subtitle={
        <>
          <span className="inline-flex max-w-full min-w-0 items-center gap-2">
            <span className="shrink-0 font-mono">{token.prefix}…</span>
            {owner && (
              <span className="inline-flex min-w-0 items-center gap-1.5">
                <UserAvatar user={owner} scope="admin" size="xs" />
                <span className="truncate">{displayNameOf(owner)}</span>
              </span>
            )}
            <span className="hidden shrink-0 sm:inline">made {calendarDate(token.createdAt)}</span>
            {/* Which deployments is the first thing asked of a deploy key that
                leaked, so the row answers it without opening anything. */}
            {token.scope === "deploy" && (
              <span className="hidden shrink-0 sm:inline">· {deployReach(token)}</span>
            )}
          </span>
          <span className="block md:hidden">
            <span style={{ color: ROLE_HUE[token.role] }}>{can}</span> · {usage}
          </span>
        </>
      }
      trailing={
        <>
          <span
            className="hidden text-xs text-muted-foreground md:inline"
            title={token.lastUsedAt ? new Date(token.lastUsedAt).toLocaleString() : undefined}
          >
            {usage}
          </span>
          <Tag className="hidden md:inline-flex" style={{ color: ROLE_HUE[token.role] }}>
            {can}
          </Tag>
          <span title={token.expiresAt ? new Date(token.expiresAt).toLocaleString() : undefined}>
            {keyStatus(token, now)}
          </span>
        </>
      }
    >
      <VerbActions dim verbs={verbs} menuLabel={`More actions for ${token.name}`} />
    </Row>
  )
}

/**
 * The command a key is for, with this dashboard's own address in it — the
 * sentence the page used to open on, now the one thing a reader who already
 * has a key comes back for.
 */
function CallExample() {
  const origin = useApiOrigin()
  const { copy, copied } = useCopy()
  return (
    <Panel plain>
      <PanelHeader
        title="Call the API"
        actions={
          <Button size="xs" variant="outline" onClick={() => void copy(curlExample(origin))}>
            {copied ? <Check /> : <Copy />}
            {copied ? "Copied" : "Copy"}
          </Button>
        }
      />
      <PanelBody className="space-y-2">
        <Well className="break-all">
          <ShellWords command={curlExample(origin)} />
        </Well>
        <p className="text-hint text-muted-foreground">
          Send the key as a bearer token on any request; this one answers what the Overview shows.
        </p>
      </PanelBody>
    </Panel>
  )
}

const SCOPES: { key: KeyFilter; label: string; warn?: boolean }[] = [
  { key: "all", label: "All" },
  { key: "week", label: "Used this week" },
  { key: "unused", label: "Never used", warn: true },
  { key: "idle", label: "Unused for 30 days", warn: true },
  { key: "expiring", label: "Expiring soon", warn: true },
  { key: "unbounded", label: "No expiry" },
]
const ORDERS: { key: KeyOrder; label: string }[] = [
  { key: "newest", label: "Newest first" },
  { key: "used", label: "Last used" },
  { key: "expiry", label: "Expiring first" },
  { key: "name", label: "Name" },
]

/** Reading register: counts stay on filters and the existing map, as on the other account pages. */
export function ApiKeysView({
  keys,
  users,
}: {
  keys: ReturnType<typeof useApiKeys>
  users?: DashboardUser[]
}) {
  const { status, can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const origin = useApiOrigin()
  const now = useNow(30_000)
  const [scope, setScope] = useState<KeyFilter>("all")
  const [query, setQuery] = useState("")
  const [order, setOrder] = useState<KeyOrder>("newest")
  const [mine, setMine] = useState(false)
  const [historyOpen, setHistoryOpen] = useState(false)
  const [editor, setEditor] = useState<ApiToken | "new" | null>(null)
  const { data, error, loading, refresh } = keys
  if (loading && !data) return <LoadingRows rows={3} />
  if (error && !data) return <ErrorState error={error} onRetry={refresh} />
  if (!data) return null

  const me = status?.user?.id
  const ownerOf = (token: ApiToken) => users?.find((user) => user.id === token.userId)
  const subject = (token: ApiToken) => ({
    name: token.name,
    mark: <ProductLogo id={keyProduct(token.name)} size="sm" fallback={Key} />,
    facts: <span className="font-mono">{token.prefix}…</span>,
  })
  const revoke = (token: ApiToken) =>
    confirm({
      title: "Revoke key",
      confirmLabel: "Revoke",
      subject: subject(token),
      description: (
        <p>
          Anything using this key stops working immediately. Its record stays in history; the key
          cannot be used again.
        </p>
      ),
      action: async () => {
        await del(`/tokens/${token.id}`)
        refresh()
      },
    })
  const remove = (token: ApiToken) =>
    confirm({
      title: "Delete key record",
      confirmLabel: "Delete record",
      subject: subject(token),
      description: (
        <p>
          This key already cannot authenticate. Delete its record from this list permanently? The
          audit trail is kept.
        </p>
      ),
      action: async () => {
        await del(`/tokens/${token.id}/record`)
        refresh()
      },
    })
  const verbsOf = (token: ApiToken): Verb[] => {
    const active = !token.revoked && !expired(token, now)
    return [
      ...(token.userId === me
        ? [
            {
              key: "replace",
              label: "Replace key",
              icon: RefreshClockwise,
              run: () => setEditor(token),
            },
          ]
        : []),
      {
        key: "prefix",
        label: "Copy prefix",
        icon: Copy,
        run: () => {
          void copyText(token.prefix, "Prefix copied")
        },
      },
      ...(active
        ? [
            {
              key: "revoke",
              label: "Revoke key",
              icon: Slash,
              danger: true,
              run: () => revoke(token),
            },
          ]
        : []),
      ...(!active && can("system.admin") && can("destructive")
        ? [
            {
              key: "delete",
              label: "Delete record",
              icon: Trash,
              danger: true,
              run: () => remove(token),
            },
          ]
        : []),
    ]
  }
  const usable = usableKeys(data, now)
  const spent = data.filter((token) => token.revoked || expired(token, now))
  const matches = (token: ApiToken) =>
    (!mine || token.userId === me) && matchesKey(token, query, ownerOf(token))
  const candidates = usable.filter(matches)
  const shown = orderKeys(
    candidates.filter((token) => inKeyFilter(token, scope, now)),
    order,
  )
  const inactive = orderKeys(spent.filter(matches), order)
  const filtered = Boolean(query.trim() || mine || scope !== "all")
  const clear = () => {
    setQuery("")
    setScope("all")
    setMine(false)
  }
  const createButton = (
    <Button size="sm" onClick={() => setEditor("new")}>
      <Plus className="size-4" />
      New key
    </Button>
  )
  const row = (token: ApiToken) => (
    <KeyRow
      key={token.id}
      token={token}
      now={now}
      owner={token.userId === me ? undefined : ownerOf(token)}
      verbs={verbsOf(token)}
    />
  )

  return (
    <>
      {error && (
        <Notice tone="warning" title="Could not refresh keys">
          These are the last keys received.{" "}
          <button className="text-brand hover:underline" onClick={refresh}>
            Retry
          </button>
        </Notice>
      )}
      {usable.length > 0 && (
        <Section title="Where the keys reach">
          <KeyMap tokens={usable} origin={origin} />
        </Section>
      )}
      {data.length === 0 ? (
        <EmptyState
          icon={Key}
          title="No API keys yet"
          description="A key lets a CI pipeline, a cron job or a tool on your laptop call this dashboard without a browser or your password."
          action={createButton}
        />
      ) : (
        <>
          <Panel plain id="keys" className="scroll-mt-6">
            <PanelHeader
              title="In use"
              actions={
                <span className="flex items-center gap-3">
                  <span className="numeric text-hint text-muted-foreground" aria-live="polite">
                    {filtered
                      ? `${shown.length} of ${plural(usable.length, "key")}`
                      : plural(usable.length, "key")}
                  </span>
                  {createButton}
                </span>
              }
            />
            <PanelBody flush>
              <div className="flex flex-wrap items-center gap-2 pt-3">
                <SearchInput
                  aria-label="Search keys"
                  placeholder="Search name, prefix, role or owner…"
                  value={query}
                  onChange={(event) => {
                    setQuery(event.target.value)
                    if (event.target.value.trim()) setHistoryOpen(true)
                  }}
                  containerClassName="sm:flex-1"
                />
                <Select value={order} onValueChange={(value) => setOrder(value as KeyOrder)}>
                  <SelectTrigger size="sm" aria-label="Sort keys">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {ORDERS.map((option) => (
                      <SelectItem key={option.key} value={option.key}>
                        {option.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                {can("system.admin") && data.some((token) => token.userId !== me) && (
                  <FilterChip selected={mine} onClick={() => setMine(!mine)}>
                    Yours
                    <ChipCount>{usable.filter((token) => token.userId === me).length}</ChipCount>
                  </FilterChip>
                )}
                {filtered && (
                  <Button size="xs" variant="ghost" onClick={clear}>
                    Clear filters
                  </Button>
                )}
              </div>
              {usable.length > 0 && (
                <ChipStrip role="group" aria-label="Show" className="pt-3 pb-1">
                  {SCOPES.map(({ key, label, warn }) => {
                    const count = candidates.filter((token) => inKeyFilter(token, key, now)).length
                    if (key !== "all" && count === 0 && scope !== key) return null
                    return (
                      <FilterChip
                        key={key}
                        selected={scope === key}
                        onClick={() => setScope(scope === key && key !== "all" ? "all" : key)}
                      >
                        {label}
                        <ChipCount className={cn(warn && count > 0 && "text-warning opacity-100")}>
                          {count}
                        </ChipCount>
                      </FilterChip>
                    )
                  })}
                </ChipStrip>
              )}
              {usable.length === 0 ? (
                <EmptyState
                  icon={Key}
                  title="No key opens anything"
                  description="Every key here was revoked or has expired. Create a new key, or replace one from history."
                />
              ) : shown.length === 0 ? (
                <EmptyNote className="py-4">No active key matches these filters.</EmptyNote>
              ) : (
                <RowList className="animate-rise">{shown.map(row)}</RowList>
              )}
            </PanelBody>
          </Panel>
          {spent.length > 0 && (
            <Collapsible open={historyOpen} onOpenChange={setHistoryOpen}>
              <Panel plain>
                <PanelHeader
                  title="Revoked or expired"
                  actions={
                    <span className="flex items-center gap-3">
                      <span className="numeric text-hint text-muted-foreground">
                        {query.trim() || mine
                          ? `${inactive.length} of ${plural(spent.length, "key")}`
                          : plural(spent.length, "key")}
                      </span>
                      <CollapsibleTrigger asChild>
                        <Button
                          size="xs"
                          variant="outline"
                          aria-label={historyOpen ? "Hide inactive keys" : "Show inactive keys"}
                        >
                          <ChevronDown
                            className={cn(
                              "size-3.5 transition-transform motion-reduce:transition-none",
                              historyOpen && "rotate-180",
                            )}
                          />
                          {historyOpen ? "Hide" : "Show"}
                        </Button>
                      </CollapsibleTrigger>
                    </span>
                  }
                />
                <CollapsibleContent>
                  <PanelBody flush>
                    <p className="py-3 text-hint text-muted-foreground">
                      These keys cannot authenticate.{" "}
                      {can("system.admin")
                        ? "Delete record removes an entry here; its audit trail is kept."
                        : "An administrator can delete inactive records; their audit trail is kept."}
                    </p>
                    {inactive.length > 0 ? (
                      <RowList className="text-muted-foreground">{inactive.map(row)}</RowList>
                    ) : (
                      <EmptyNote className="py-4">No inactive key matches this search.</EmptyNote>
                    )}
                  </PanelBody>
                </CollapsibleContent>
              </Panel>
            </Collapsible>
          )}
        </>
      )}
      <CallExample />
      {editor !== null && (
        <KeyDialog
          key={editor === "new" ? "new" : editor.id}
          replacement={editor === "new" ? undefined : editor}
          onClose={() => setEditor(null)}
          onDone={refresh}
        />
      )}
      {dialog}
    </>
  )
}

export const EXPIRY = [
  { value: "7", label: "7 days" },
  { value: "30", label: "30 days" },
  { value: "90", label: "90 days" },
  { value: "365", label: "1 year" },
  { value: "0", label: "Never" },
]

/**
 * Minting a key: what it is called, what it may do, and how long it lasts —
 * then the key itself, once, with the command that uses it.
 *
 * The role is three cards with the sentence each one means, because
 * "limited" means nothing to somebody making their first key, and a key may
 * only ever narrow its owner's role: the server enforces that too, so the
 * cards only avoid a pointless round trip. The name's field carries the mark
 * the key will be drawn with, so "github-actions" shows GitHub's as it is
 * typed.
 *
 * An administrator is also offered a deploy key, the kind `jd` and a CI
 * pipeline run on, as a second card beside the ordinary one: the choice is
 * between two kinds of credential, each with its sentence (§4). Only an
 * administrator may mint one — the server asks for an administrator's
 * session — so nobody else is shown a choice that would bounce.
 */
/** Kept outside the empty/list switch so refreshing the first key cannot lose its secret. */
function KeyDialog({
  replacement,
  onClose,
  onDone,
}: {
  replacement?: ApiToken
  onClose: () => void
  onDone: () => void
}) {
  const { status } = useAuth()
  const origin = useApiOrigin()
  const { confirm, dialog } = useConfirm()
  const now = useNow(30_000)
  const pending = useRef(false)
  const [previousRevoked, setPreviousRevoked] = useState(false)
  const previousActive =
    replacement && !replacement.revoked && !expired(replacement, now) && !previousRevoked
  const [name, setName] = useState(replacement?.name ?? "")
  const [role, setRole] = useState<Role>(
    replacement
      ? replacementRoles(replacement.role, status?.user?.role ?? "readonly")[0]
      : "readonly",
  )
  const [kind, setKind] = useState<KeyKind>(replacement?.scope === "deploy" ? "deploy" : "api")
  const [every, setEvery] = useState(true)
  const [chosen, setChosen] = useState<ReadonlySet<number>>(new Set())
  const [ttl, setTtl] = useState("90")
  const [secret, setSecret] = useState<string | null>(null)
  const generation = useRef(0)
  const [busy, setBusy] = useState(false)

  const admin = status?.user?.role === "admin"
  const allowed = replacementRoles(replacement?.role ?? "admin", status?.user?.role ?? "readonly")
  // The kind of key being made: a replacement is always the kind it replaces.
  const scope = replacement
    ? replacement.scope || undefined
    : kind === "deploy"
      ? "deploy"
      : undefined
  const deploying = scope === "deploy"
  const ready = deploying
    ? replacement
      ? name.trim() !== ""
      : deployKeyReady(name, every, chosen)
    : name.trim() !== ""

  const revokePrevious = () => {
    if (!replacement) return
    confirm({
      title: "Revoke previous key",
      confirmLabel: "Revoke previous key",
      description: (
        <p>
          Revoke <b>{replacement.name}</b> ({replacement.prefix}…)? Tools still using the previous
          key stop working immediately. Check that they use your new key first.
        </p>
      ),
      action: async () => {
        await del(`/tokens/${replacement.id}`)
        setPreviousRevoked(true)
        onDone()
      },
    })
  }

  const create = async () => {
    if (pending.current || !ready) return
    pending.current = true
    const request = generation.current
    setBusy(true)
    try {
      // A replacement is made the same way as the key it replaces: a key limited
      // to one job is never replaced by one that can do what its owner can,
      // an MCP key's replacement reaches the same databases at its role, and a
      // deploy key's the same deployments — there is no route to re-point one.
      const res =
        scope === "mcp"
          ? await post<{ secret: string }>("/tokens/mcp", {
              name: name.trim(),
              role,
              ttlDays: Number(ttl),
              ...(await replacementGrants(replacement, role)),
            })
          : scope === "deploy"
            ? await post<{ secret: string }>(
                "/tokens/deploy",
                deployKeyBody(
                  name,
                  ttl,
                  replacement ? (replacement.deploymentIds ?? []) : every ? [] : [...chosen],
                ),
              )
            : await post<{ secret: string }>("/tokens/", {
                name: name.trim(),
                ...(scope ? { scope } : { role }),
                ttlDays: Number(ttl),
              })
      if (request === generation.current) setSecret(res.secret)
      onDone()
    } catch (err) {
      notify.error("Could not create the key", err)
    } finally {
      pending.current = false
      if (request === generation.current) setBusy(false)
    }
  }

  const reset = () => {
    generation.current += 1
    setBusy(false)
    setSecret(null)
    setName("")
    setRole("readonly")
    setKind("api")
    setEvery(true)
    setChosen(new Set())
    setTtl("90")
  }

  const close = () => {
    if (pending.current) return
    onClose()
    reset()
  }

  return (
    <>
      <Modal
        open
        onOpenChange={(o) => {
          if (!o) close()
        }}
        title={secret ? "Your new key" : replacement ? "Replace API key" : "New API key"}
        description="A credential for scripts and other machines to call this dashboard's API."
        footer={
          secret ? (
            <>
              {previousActive && (
                <Button variant="outline" onClick={revokePrevious}>
                  Revoke previous key
                </Button>
              )}
              <Button onClick={close}>Done</Button>
            </>
          ) : (
            <Button onClick={create} disabled={!ready || busy} pending={busy}>
              {replacement ? "Create replacement" : "Create key"}
            </Button>
          )
        }
      >
        {secret ? (
          <div className="space-y-4">
            {replacement && (
              <Notice
                tone={previousActive ? "warning" : "success"}
                title={
                  previousActive
                    ? "New key ready; previous key still works"
                    : "Previous key is inactive"
                }
              >
                {previousActive
                  ? "Copy the new key into your tool and check it works, then revoke the previous key below. Closing this dialog keeps both keys working."
                  : "Use this new key in your tool. The previous key cannot authenticate."}
              </Notice>
            )}
            <Field
              label="Key"
              hint="Copy it now. It is stored hashed, so it cannot be shown again — only replaced."
              trailing={
                <Button size="xs" variant="outline" onClick={() => copyText(secret, "Key copied")}>
                  <Copy />
                  Copy
                </Button>
              }
            >
              <Well className="break-all select-all">{secret}</Well>
            </Field>
            <Field
              label="Try it"
              hint={
                scope === "mcp"
                  ? "Adds the dashboard to Claude Code. Databases → MCP server has Cursor's and VS Code's."
                  : scope === "deploy"
                    ? `Signs jd in with this key. On this server jd is already at ${CLI_ON_SERVER}; in CI, set JD_URL and JD_KEY instead.`
                    : scope
                      ? "Send the key as a bearer token. This one returns the recorded database metrics."
                      : "Send the key as a bearer token on any request. This one returns what the Overview shows."
              }
              trailing={
                <Button
                  size="xs"
                  variant="outline"
                  onClick={() => copyText(curlExample(origin, secret, scope), "Command copied")}
                >
                  <Copy />
                  Copy
                </Button>
              }
            >
              <Well className="break-all">{curlExample(origin, secret, scope)}</Well>
            </Field>
            {deploying && <CliDownload />}
          </div>
        ) : (
          <fieldset disabled={busy} className="min-w-0 space-y-5">
            {replacement && (
              <Notice title={`Replacing ${replacement.name}`}>
                <span className="font-mono">{replacement.prefix}…</span>
                {previousActive
                  ? " keeps working until you revoke it. Create a new key, update your tool, then retire this one."
                  : " is inactive. This creates a separate key; it does not reactivate the old one."}
              </Notice>
            )}
            <Field
              label="Name"
              htmlFor="key-name"
              hint="Where it will live — a CI pipeline, a cron job, a laptop — so it is recognisable in this list later."
            >
              <div className="flex min-w-0 items-center gap-2">
                <ProductLogo id={keyProduct(name)} size="sm" fallback={Key} />
                <Input
                  id="key-name"
                  value={name}
                  autoFocus
                  disabled={busy}
                  onChange={(e) => setName(e.target.value)}
                  placeholder={kind === "deploy" ? "jd-laptop" : "github-actions"}
                />
              </div>
            </Field>
            {!replacement && admin && <KindChoice value={kind} onChange={setKind} />}
            {scope === "mcp" ? (
              <Notice title="Opens the MCP endpoint only">
                The replacement reaches the same databases and deployments as the key it replaces,
                and opens nothing else on this dashboard.
              </Notice>
            ) : scope === "deploy" ? (
              <>
                {replacement ? (
                  <Notice title="Reaches the same deployments">
                    The replacement reaches {deployReach(replacement)}, as the key it replaces does,
                    and nothing outside Deployments.
                  </Notice>
                ) : (
                  <DeploymentChoice
                    every={every}
                    onEvery={setEvery}
                    chosen={chosen}
                    onChosen={setChosen}
                  />
                )}
                <DeployKeyReach />
              </>
            ) : scope ? (
              <Notice title="Reads database metrics only">
                The replacement is limited the same way: it can scrape the metrics endpoint and
                nothing else on this dashboard.
              </Notice>
            ) : (
              <RoleChoice label="Can" value={role} onChange={setRole} roles={allowed} />
            )}
            <Field
              label="Expires"
              hint={
                ttl === "0"
                  ? "This key will keep working until you revoke it. Use an expiry for temporary jobs."
                  : `The new key expires ${calendarDate(new Date(now + Number(ttl) * 86_400_000).toISOString())}.`
              }
            >
              <ToggleGroup
                type="single"
                value={ttl}
                onValueChange={(next) => next && !busy && setTtl(next)}
                variant="outline"
                size="sm"
                aria-label="Expires"
                className="w-full"
              >
                {EXPIRY.map((e) => (
                  <ToggleGroupItem
                    key={e.value}
                    value={e.value}
                    disabled={busy}
                    className="flex-1 text-xs"
                  >
                    {e.label}
                  </ToggleGroupItem>
                ))}
              </ToggleGroup>
            </Field>
            {deploying ? (
              <FormNote>
                A deploy key works only while your account is an administrator: if it is demoted or
                disabled, the key goes with it. Every change it makes is in the audit log under its
                name.
              </FormNote>
            ) : (
              <FormNote>
                A key can do at most what your role can, and never more than it could at the time —
                if your account is demoted or disabled, every key it made goes with it. Keys cannot
                change passwords, make other keys or manage accounts; those need a real sign-in.
                {replacement && " A replacement can keep or narrow the previous key’s role."}
              </FormNote>
            )}
          </fieldset>
        )}
      </Modal>
      {dialog}
    </>
  )
}

type KeyKind = "api" | "deploy"

/** The two kinds of key an administrator may make, as the sentence each one means. */
const KINDS: { value: KeyKind; title: string; hint: string; Mark: typeof Key }[] = [
  {
    value: "api",
    title: "API key",
    hint: "Calls the whole API as the role you give it, from scripts and tools.",
    Mark: Key,
  },
  {
    value: "deploy",
    title: "Deploy key",
    hint: "For jd and CI: ships and configures deployments, and reaches nothing else.",
    Mark: Rocket,
  },
]

/**
 * Which kind of key, drawn as `RoleChoice` draws a role: cards with their
 * sentence, since the difference is the sentence.
 */
function KindChoice({ value, onChange }: { value: KeyKind; onChange: (kind: KeyKind) => void }) {
  return (
    <fieldset className="min-w-0 space-y-1.5">
      <legend className="mb-1.5 text-body font-medium">Kind</legend>
      <div className="grid gap-2 sm:grid-cols-2">
        {KINDS.map(({ value: kind, title, hint, Mark }) => {
          const selected = value === kind
          return (
            <ChoiceCard
              key={kind}
              selected={selected}
              onClick={() => onChange(kind)}
              className="min-h-0 flex-row items-center gap-3 px-3 py-2.5"
            >
              <Mark
                aria-hidden
                className={cn("size-4 shrink-0", selected ? "text-brand" : "text-muted-foreground")}
              />
              <span className="flex min-w-0 flex-col">
                <ChoiceCardTitle>{title}</ChoiceCardTitle>
                <ChoiceCardHint>{hint}</ChoiceCardHint>
              </span>
            </ChoiceCard>
          )
        })}
      </div>
    </fieldset>
  )
}

/**
 * Which deployments a deploy key reaches, or an MCP key's deployment grant:
 * all of them, the ones made later included, or the ones ticked — a key for
 * one repository's pipeline that leaks then reaches that one application, not
 * the server's every one. The list is the fleet the deployment pages read,
 * drawn as each project draws itself there.
 */
export function DeploymentChoice({
  every,
  onEvery,
  chosen,
  onChosen,
  label = "Deployments",
  idPrefix = "deploy-key",
}: {
  every: boolean
  onEvery: (every: boolean) => void
  chosen: ReadonlySet<number>
  onChosen: (chosen: ReadonlySet<number>) => void
  label?: string
  idPrefix?: string
}) {
  const projects = useProjects()
  const [query, setQuery] = useState("")
  const fleet = [...projects.values()].sort((a, b) => a.name.localeCompare(b.name))
  const words = query.trim().toLocaleLowerCase()
  const shown = words ? fleet.filter((d) => d.name.toLocaleLowerCase().includes(words)) : fleet
  const toggle = (id: number, on: boolean) => {
    const next = new Set(chosen)
    if (on) next.add(id)
    else next.delete(id)
    onChosen(next)
  }
  return (
    <Field
      label={label}
      hint={
        every
          ? "Every deployment on this server, including the ones made after the key."
          : chosen.size === 0
            ? "Tick at least one. A key limited to some deployments also reaches the ones it creates."
            : `Only ${plural(chosen.size, "deployment")}, and the ones the key creates from its own drafts.`
      }
    >
      <div className="min-w-0 space-y-2">
        <Segments
          label="Which deployments the key reaches"
          fill
          value={every ? "every" : "some"}
          onChange={(next) => onEvery(next === "every")}
          options={[
            { value: "every", label: "Every deployment" },
            { value: "some", label: "Only these" },
          ]}
        />
        {!every &&
          (fleet.length === 0 ? (
            <EmptyNote>There are no deployments to choose from yet.</EmptyNote>
          ) : (
            <>
              {fleet.length > 6 && (
                <SearchInput
                  aria-label="Search deployments"
                  placeholder="Search deployments…"
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                />
              )}
              <ul
                aria-label="Deployments"
                className="max-h-64 divide-y divide-hairline overflow-y-auto rounded-lg border"
              >
                {shown.map((deployment) => {
                  const id = `${idPrefix}-${deployment.id}`
                  return (
                    <li key={deployment.id} className="flex min-w-0 items-center gap-3 px-3 py-2.5">
                      <Checkbox
                        id={id}
                        checked={chosen.has(deployment.id)}
                        onCheckedChange={(checked) => toggle(deployment.id, checked === true)}
                      />
                      <label
                        htmlFor={id}
                        className="flex min-w-0 flex-1 items-center gap-2 text-body"
                      >
                        <ProjectMark deployment={deployment} size="xs" />
                        <span className="truncate font-medium">{deployment.name}</span>
                        <span className="truncate text-hint text-muted-foreground">
                          {deployment.environmentName}
                        </span>
                      </label>
                    </li>
                  )
                })}
                {shown.length === 0 && (
                  <li className="px-3 py-2.5">
                    <EmptyNote>No deployment matches this search.</EmptyNote>
                  </li>
                )}
              </ul>
            </>
          ))}
      </div>
    </Field>
  )
}

/**
 * What a deploy key does and never does, as two short lists rather than a
 * paragraph: the second list is the one read when deciding whether to put
 * the key in a pipeline at all, so it is not a clause at the end of the first.
 */
function DeployKeyReach() {
  return (
    <div className="min-w-0 space-y-3">
      <div className="grid min-w-0 gap-4 sm:grid-cols-2">
        <ReachList title="It can" items={DEPLOY_KEY_CAN} mark={Check} />
        <ReachList title="It can’t" items={DEPLOY_KEY_CANNOT} mark={Slash} />
      </div>
      <FormNote>{DEPLOY_KEY_HOST_RULE}</FormNote>
    </div>
  )
}

function ReachList({
  title,
  items,
  mark: Mark,
}: {
  title: string
  items: string[]
  mark: typeof Check
}) {
  return (
    <section className="min-w-0 space-y-1.5" aria-label={title}>
      <h3 className="text-body font-medium">{title}</h3>
      <ul className="space-y-1">
        {items.map((item) => (
          <li key={item} className="flex min-w-0 gap-2 text-hint text-muted-foreground">
            <Mark aria-hidden className="mt-0.5 size-3.5 shrink-0" />
            <span className="min-w-0">{item}</span>
          </li>
        ))}
      </ul>
    </section>
  )
}

/**
 * `jd` for another machine: the dashboard serves the build it ships for its
 * own Linux, so the link is only as wide as that. Any other system builds it
 * from the repository.
 */
function CliDownload() {
  return (
    <FormNote>
      Another Linux machine of this server’s architecture can{" "}
      <a href={CLI_DOWNLOAD} download="jd" className="text-brand hover:underline">
        download jd
      </a>{" "}
      from this dashboard; for anything else, build it from the repository with{" "}
      <span className="font-mono">go build ./cmd/jd</span> in{" "}
      <span className="font-mono">backend</span>. Generate a client from the dashboard’s{" "}
      {/* A new tab: following it here would leave the dialog that shows the
          secret once. */}
      <a
        href={OPENAPI_DESCRIPTION}
        target="_blank"
        rel="noopener noreferrer"
        className="text-brand hover:underline"
      >
        OpenAPI description
      </a>{" "}
      of the deployment API.
    </FormNote>
  )
}
