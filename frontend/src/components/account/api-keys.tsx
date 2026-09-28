"use client"

import { useState, useSyncExternalStore } from "react"
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
import { ProductGlyphs, ProductLogo } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { RoleChoice } from "@/components/account/role-choice"
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

const DAY = 86_400_000

/** How close an expiry has to be before a key's state says so in amber. */
const EXPIRING_MS = 14 * DAY

function expired(token: ApiToken) {
  return Boolean(token.expiresAt && new Date(token.expiresAt).getTime() < Date.now())
}

/** Called within the last week — the keys something is actually using. */
function usedThisWeek(token: ApiToken) {
  return Boolean(token.lastUsedAt && Date.now() - new Date(token.lastUsedAt).getTime() < 7 * DAY)
}

function expiringSoon(token: ApiToken) {
  if (!token.expiresAt || token.revoked || expired(token)) return false
  return new Date(token.expiresAt).getTime() - Date.now() < EXPIRING_MS
}

/** The keys that would still open a door right now. */
export function usableKeys(tokens: ApiToken[]) {
  return tokens.filter((k) => !k.revoked && !expired(k))
}

/** What the keys are for, as products, each once — for a reading that counts them. */
export function keyProducts(tokens: ApiToken[]) {
  return [...new Set(tokens.map((k) => keyProduct(k.name)).filter((id) => id !== undefined))]
}

export function curlExample(origin: string, key = "vpsd_…") {
  return `curl -H "Authorization: Bearer ${key}" ${origin}/api/v1/system/host`
}

/** The one line a key's state comes down to. */
function keyStatus(token: ApiToken) {
  if (token.revoked) return <Status tone="stopped" label="revoked" />
  if (expired(token)) return <Status tone="stopped" label="expired" />
  if (expiringSoon(token)) {
    return <Status tone="warning" label={`expires ${relativeTime(token.expiresAt)}`} />
  }
  if (token.expiresAt) {
    return <Status tone="running" label={`until ${calendarDate(token.expiresAt)}`} />
  }
  return <Status tone="running" label="no expiry" />
}

/**
 * Four readings over the keys, each a question an operator asks of a list of
 * credentials: how many still open a door, which of them anything is actually
 * using, which were made and never used — a key nobody uses is a key nobody
 * would notice being used — and which are about to stop working under a job
 * that still needs them.
 */
function KeyReadings({ tokens }: { tokens: ApiToken[] }) {
  const usable = usableKeys(tokens)
  const week = usable.filter(usedThisWeek)
  const lastUse = usable
    .map((k) => k.lastUsedAt)
    .filter(Boolean)
    .sort()
    .at(-1)
  const unused = usable
    .filter((k) => !k.lastUsedAt)
    .sort((a, b) => a.createdAt.localeCompare(b.createdAt))
  const expiring = usable
    .filter(expiringSoon)
    .sort((a, b) => (a.expiresAt ?? "").localeCompare(b.expiresAt ?? ""))

  return (
    <StatGrid columns={4}>
      <StatTile
        label="In use"
        value={usable.length}
        hint={
          <span className="inline-flex max-w-full min-w-0 items-center gap-2">
            <span className="truncate">
              {tokens.length - usable.length > 0
                ? `${tokens.length - usable.length} revoked or expired`
                : "none revoked"}
            </span>
            <ProductGlyphs ids={keyProducts(usable)} />
          </span>
        }
      />
      <StatTile
        label="Used this week"
        value={week.length}
        hint={lastUse ? `last call ${relativeTime(lastUse)}` : "no calls yet"}
      />
      <StatTile
        label="Never used"
        value={unused.length}
        tone={unused.length > 0 ? "warning" : "default"}
        hint={
          unused.length > 0
            ? `oldest made ${calendarDate(unused[0].createdAt)}`
            : "every key has been used"
        }
      />
      <StatTile
        label="Expiring soon"
        value={expiring.length}
        tone={expiring.length > 0 ? "warning" : "default"}
        hint={
          expiring.length > 0
            ? `${expiring[0].name} ${relativeTime(expiring[0].expiresAt)}`
            : "none within two weeks"
        }
      />
    </StatGrid>
  )
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
  owner,
  onRevoke,
}: {
  token: ApiToken
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
          className={token.revoked || expired(token) ? "opacity-50 grayscale" : undefined}
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
          <Tag className="hidden sm:inline-flex">{token.role}</Tag>
          {keyStatus(token)}
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
        <Well className="break-all">{curlExample(origin)}</Well>
        <p className="text-hint text-muted-foreground">
          Send the key as a bearer token on any request; this one answers what the Overview shows.
        </p>
      </PanelBody>
    </Panel>
  )
}

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
  const usable = usableKeys(data).sort(byNewest)
  const spent = data.filter((k) => !usable.includes(k)).sort(byNewest)

  return (
    <>
      <KeyReadings tokens={data} />

      <Panel plain>
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
          {usable.length === 0 ? (
            <EmptyState
              icon={Key}
              title="No key opens anything"
              description="Every key here was revoked or has expired."
            />
          ) : (
            <RowList className="animate-rise">
              {usable.map((token) => (
                <KeyRow
                  key={token.id}
                  token={token}
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
                <KeyRow key={token.id} token={token} owner={ownerOf(token)} />
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
  const [busy, setBusy] = useState(false)

  const allowed: Role[] =
    status?.user?.role === "admin"
      ? ["admin", "limited", "readonly"]
      : status?.user?.role === "limited"
        ? ["limited", "readonly"]
        : ["readonly"]

  const create = async () => {
    setBusy(true)
    try {
      const res = await post<{ secret: string }>("/tokens/", {
        name: name.trim(),
        role,
        ttlDays: Number(ttl),
      })
      setSecret(res.secret)
      onDone()
    } catch (err) {
      notify.error("Could not create the key", err)
    } finally {
      setBusy(false)
    }
  }

  const reset = () => {
    setSecret(null)
    setName("")
    setRole("readonly")
    setTtl("90")
  }

  return (
    <>
      <Button size="sm" onClick={() => setOpen(true)}>
        <Plus className="size-4" />
        New key
      </Button>
      <Modal
        open={open}
        onOpenChange={(o) => {
          setOpen(o)
          if (!o) reset()
        }}
        title={secret ? "Your new key" : "New API key"}
        description="A credential for scripts and other machines to call this dashboard's API."
        footer={
          secret ? (
            <Button onClick={() => setOpen(false)}>Done</Button>
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
