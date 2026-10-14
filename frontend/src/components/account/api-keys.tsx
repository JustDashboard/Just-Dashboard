"use client"

import { useRef, useState, useSyncExternalStore } from "react"
import { Check, Copy, Key, Plus, Trash } from "@/components/icons"
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
import { DimActions, IconAction } from "@/components/icon-action"
import { Modal } from "@/components/modal"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { Section } from "@/components/page"
import { ProductLogo } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { EmptyNote, EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { ShellWords } from "@/components/deploy/run-evidence"
import { useNow } from "@/components/deploy/vocabulary"
import { cn } from "@/lib/utils"
import { RoleChoice } from "@/components/account/role-choice"
import { ROLE_HUE } from "@/components/account/capabilities"
import {
  expired,
  expiringSoon,
  inKeyScope,
  usableKeys,
  type KeyScope,
} from "@/components/account/access"
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
  onRevoke,
}: {
  token: ApiToken
  now: number
  /** The account that made it, when that is not the reader. */
  owner?: DashboardUser
  onRevoke?: () => void
}) {
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
      }
      trailing={
        <>
          <span className="hidden text-xs text-muted-foreground md:inline">
            {token.lastUsedAt ? `used ${relativeTime(token.lastUsedAt)}` : "never used"}
          </span>
          <Tag className="hidden sm:inline-flex" style={{ color: ROLE_HUE[token.role] }}>
            {token.role}
          </Tag>
          {keyStatus(token, now)}
        </>
      }
    >
      {onRevoke && (
        <DimActions>
          <IconAction
            label={`Revoke ${token.name}`}
            className="text-destructive"
            onClick={onRevoke}
          >
            <Trash />
          </IconAction>
        </DimActions>
      )}
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

/** The chips over the list: each narrowing, and the tone its count takes while it is not zero. */
const SCOPES: { key: KeyScope; label: string; warn?: boolean }[] = [
  { key: "all", label: "All" },
  { key: "week", label: "Used this week" },
  { key: "unused", label: "Never used", warn: true },
  { key: "expiring", label: "Expiring soon", warn: true },
]

/**
 * The keys page's body: where the keys reach, drawn (`KeyMap`), then the
 * keys that still open a door under chips that count and narrow them, the
 * spent ones, and the command a key is for.
 */
export function ApiKeysView({
  keys,
  users,
}: {
  keys: ReturnType<typeof useApiKeys>
  /** Every account, for an administrator — to say whose key each one is. */
  users?: DashboardUser[]
}) {
  const { status } = useAuth()
  const { confirm, dialog } = useConfirm()
  const origin = useApiOrigin()
  const now = useNow(30_000)
  const [scope, setScope] = useState<KeyScope>("all")
  const { data, error, loading, refresh } = keys
  if (loading && !data) return <LoadingRows rows={3} />
  if (error && !data) return <ErrorState error={error} onRetry={refresh} />
  if (!data) return null

  if (data.length === 0) {
    return (
      <EmptyState
        icon={Key}
        title="No API keys yet"
        description="A key lets a CI pipeline, a cron job or a tool on your laptop call this dashboard the way you do — restart a container, pull a metrics window, trigger a deployment — without a browser or your password."
        action={<CreateApiKeyDialog onDone={refresh} />}
      />
    )
  }

  const me = status?.user?.id
  const ownerOf = (token: ApiToken) =>
    token.userId === me ? undefined : users?.find((u) => u.id === token.userId)
  const revoke = (token: ApiToken) =>
    confirm({
      title: "Revoke key",
      confirmLabel: "Revoke",
      description: (
        <p>
          Anything using <b>{token.name}</b> stops working immediately. This cannot be undone; make
          a new key instead if it is needed again.
        </p>
      ),
      action: async (c) => {
        await del(`/tokens/${token.id}`, { confirm: c })
        refresh()
      },
    })

  // Newest first within each: the key just made is the one being looked for.
  const byNewest = (a: ApiToken, b: ApiToken) => b.createdAt.localeCompare(a.createdAt)
  const usable = usableKeys(data, now).sort(byNewest)
  const spent = data.filter((k) => !usable.includes(k)).sort(byNewest)
  const shown = usable.filter((k) => inKeyScope(k, scope, now))

  return (
    <>
      {usable.length > 0 && (
        <Section title="Where the keys reach">
          <KeyMap tokens={usable} origin={origin} />
        </Section>
      )}

      <Panel plain id="keys" className="scroll-mt-6">
        <PanelHeader
          title="In use"
          actions={
            <span className="flex items-center gap-3">
              <span className="numeric text-hint text-muted-foreground">
                {plural(usable.length, "key")}
              </span>
              <CreateApiKeyDialog onDone={refresh} />
            </span>
          }
        />
        <PanelBody flush>
          {usable.length > 0 && (
            <ChipStrip role="group" aria-label="Show" className="pt-3 pb-1">
              {SCOPES.map(({ key, label, warn }) => {
                const count = usable.filter((k) => inKeyScope(k, key, now)).length
                if (key !== "all" && count === 0) return null
                return (
                  <FilterChip
                    key={key}
                    selected={scope === key}
                    onClick={() => setScope(scope === key && key !== "all" ? "all" : key)}
                  >
                    {label}
                    <ChipCount className={cn(warn && "text-warning opacity-100")}>
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
              description="Every key here was revoked or has expired."
            />
          ) : shown.length === 0 ? (
            <EmptyNote className="py-4">No key matches.</EmptyNote>
          ) : (
            <RowList className="animate-rise">
              {shown.map((token) => (
                <KeyRow
                  key={token.id}
                  token={token}
                  now={now}
                  owner={ownerOf(token)}
                  onRevoke={() => revoke(token)}
                />
              ))}
            </RowList>
          )}
        </PanelBody>
      </Panel>

      {spent.length > 0 && (
        <Panel plain>
          <PanelHeader
            title="Revoked or expired"
            actions={
              <span className="numeric text-hint text-muted-foreground">
                {plural(spent.length, "key")}
              </span>
            }
          />
          <PanelBody flush>
            <RowList className="text-muted-foreground">
              {spent.map((token) => (
                <KeyRow key={token.id} token={token} now={now} owner={ownerOf(token)} />
              ))}
            </RowList>
          </PanelBody>
        </Panel>
      )}

      <CallExample />
      {dialog}
    </>
  )
}

const EXPIRY = [
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
export function CreateApiKeyDialog({ onDone }: { onDone: () => void }) {
  const { status } = useAuth()
  const origin = useApiOrigin()
  const [open, setOpen] = useState(false)
  const [name, setName] = useState("")
  const [role, setRole] = useState<Role>("readonly")
  const [ttl, setTtl] = useState("90")
  const [secret, setSecret] = useState<string | null>(null)
  const generation = useRef(0)
  const [busy, setBusy] = useState(false)

  const allowed: Role[] =
    status?.user?.role === "admin"
      ? ["admin", "limited", "readonly"]
      : status?.user?.role === "limited"
        ? ["limited", "readonly"]
        : ["readonly"]

  const create = async () => {
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
    setOpen(false)
    reset()
  }

  return (
    <>
      <Button
        size="sm"
        onClick={() => {
          reset()
          setOpen(true)
        }}
      >
        <Plus className="size-4" />
        New key
      </Button>
      <Modal
        open={open}
        onOpenChange={(o) => {
          if (!o) close()
          else setOpen(true)
        }}
        title={secret ? "Your new key" : "New API key"}
        description="A credential for scripts and other machines to call this dashboard's API."
        footer={
          secret ? (
            <Button onClick={close}>Done</Button>
          ) : (
            <Button onClick={create} disabled={!name.trim() || busy} pending={busy}>
              Create key
            </Button>
          )
        }
      >
        {secret ? (
          <div className="space-y-4">
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
          <div className="space-y-5">
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
                  onChange={(e) => setName(e.target.value)}
                  placeholder="github-actions"
                />
              </div>
            </Field>
            <RoleChoice label="Can" value={role} onChange={setRole} roles={allowed} />
            <Field label="Expires" hint="A key that outlives its job is a key nobody is watching.">
              <ToggleGroup
                type="single"
                value={ttl}
                onValueChange={(next) => next && setTtl(next)}
                variant="outline"
                size="sm"
                aria-label="Expires"
                className="w-full"
              >
                {EXPIRY.map((e) => (
                  <ToggleGroupItem key={e.value} value={e.value} className="flex-1 text-xs">
                    {e.label}
                  </ToggleGroupItem>
                ))}
              </ToggleGroup>
            </Field>
            <FormNote>
              A key can do at most what your role can, and never more than it could at the time — if
              your account is demoted or disabled, every key it made goes with it. Keys cannot
              change passwords, make other keys or manage accounts; those need a real sign-in.
            </FormNote>
          </div>
        )}
      </Modal>
    </>
  )
}
