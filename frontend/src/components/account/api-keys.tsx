"use client"

import { useRef, useState, useSyncExternalStore } from "react"
import {
  Check,
  ChevronDown,
  Copy,
  Key,
  Plus,
  RefreshClockwise,
  Slash,
  Trash,
} from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { del, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { calendarDate, plural, relativeTime } from "@/lib/format"
import { keyProduct } from "@/lib/clients"
import type { ApiToken, DashboardUser, Role } from "@/lib/types"
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
import { UserAvatar, displayNameOf } from "@/components/account/user-avatar"

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

export function curlExample(origin: string, key = "vpsd_…") {
  return `curl -H "Authorization: Bearer ${key}" ${origin}/api/v1/system/host`
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
          </span>
          <span className="block md:hidden">
            <span style={{ color: ROLE_HUE[token.role] }}>{token.role}</span> · {usage}
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
            {token.role}
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

const EXPIRY = [
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
  const [ttl, setTtl] = useState("90")
  const [secret, setSecret] = useState<string | null>(null)
  const generation = useRef(0)
  const [busy, setBusy] = useState(false)

  const allowed = replacementRoles(replacement?.role ?? "admin", status?.user?.role ?? "readonly")

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
    if (pending.current || !name.trim()) return
    pending.current = true
    const request = generation.current
    setBusy(true)
    try {
      const res = await post<{ secret: string }>("/tokens/", {
        name: name.trim(),
        role,
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
            <Button onClick={create} disabled={!name.trim() || busy} pending={busy}>
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
              hint="Send the key as a bearer token on any request. This one returns what the Overview shows."
              trailing={
                <Button
                  size="xs"
                  variant="outline"
                  onClick={() => copyText(curlExample(origin, secret), "Command copied")}
                >
                  <Copy />
                  Copy
                </Button>
              }
            >
              <Well className="break-all">{curlExample(origin, secret)}</Well>
            </Field>
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
                  placeholder="github-actions"
                />
              </div>
            </Field>
            <RoleChoice label="Can" value={role} onChange={setRole} roles={allowed} />
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
            <FormNote>
              A key can do at most what your role can, and never more than it could at the time — if
              your account is demoted or disabled, every key it made goes with it. Keys cannot
              change passwords, make other keys or manage accounts; those need a real sign-in.
              {replacement && " A replacement can keep or narrow the previous key’s role."}
            </FormNote>
          </fieldset>
        )}
      </Modal>
      {dialog}
    </>
  )
}
