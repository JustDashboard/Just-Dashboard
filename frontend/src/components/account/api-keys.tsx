"use client"

import { useState, useSyncExternalStore } from "react"
import { Copy, Key, Plus, Trash } from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { del, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { calendarDate, relativeTime } from "@/lib/format"
import type { ApiToken, Role } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { Field, FieldRow, FormNote } from "@/components/form"
import { Modal } from "@/components/modal"
import { Panel, PanelBody, Well } from "@/components/panel"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { ROLE_SUMMARY } from "@/components/account/capabilities"

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

/** The keys that would still open a door right now. */
export function usableKeys(tokens: ApiToken[]) {
  const now = Date.now()
  return tokens.filter(
    (k) => !k.revoked && !(k.expiresAt && new Date(k.expiresAt).getTime() < now),
  )
}

export function curlExample(origin: string, key = "vpsd_…") {
  return `curl -H "Authorization: Bearer ${key}" ${origin}/api/v1/system/host`
}

/** The one line a key's state comes down to. */
function keyStatus(token: ApiToken) {
  if (token.revoked) return <Status tone="stopped" label="revoked" />
  if (token.expiresAt && new Date(token.expiresAt).getTime() < Date.now()) {
    return <Status tone="stopped" label="expired" />
  }
  if (token.expiresAt) {
    return <Status tone="running" label={`expires ${relativeTime(token.expiresAt)}`} />
  }
  return <Status tone="running" label="active" />
}

export function ApiKeysTable({ keys }: { keys: ReturnType<typeof useApiKeys> }) {
  const { confirm, dialog } = useConfirm()
  const { data, error, loading, refresh } = keys
  if (loading && !data) return <LoadingPanel rows={3} />
  if (error) return <ErrorState error={error} />

  return (
    <>
      <Panel plain>
        <PanelBody flush>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-full">Name</TableHead>
                <TableHead>Begins</TableHead>
                <TableHead>Can</TableHead>
                <TableHead>Last used</TableHead>
                <TableHead>State</TableHead>
                <TableHead className="w-px" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {data?.map((token) => (
                <TableRow key={token.id} className={token.revoked ? "opacity-50" : undefined}>
                  <TableCell>
                    <span className="block truncate text-body font-medium">{token.name}</span>
                    <span className="block truncate text-hint text-muted-foreground">
                      created {calendarDate(token.createdAt)}
                    </span>
                  </TableCell>
                  <TableCell className="font-mono">{token.prefix}…</TableCell>
                  <TableCell>
                    <Tag>{token.role}</Tag>
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {token.lastUsedAt ? relativeTime(token.lastUsedAt) : "never"}
                  </TableCell>
                  <TableCell>{keyStatus(token)}</TableCell>
                  <TableCell>
                    {!token.revoked && (
                      <Button
                        size="icon-xs"
                        variant="ghost"
                        aria-label={`Revoke ${token.name}`}
                        className="text-destructive"
                        onClick={() =>
                          confirm({
                            title: "Revoke key",
                            confirmLabel: "Revoke",
                            description: (
                              <p>
                                Anything using <b>{token.name}</b> stops working immediately. This
                                cannot be undone; make a new key instead if it is needed again.
                              </p>
                            ),
                            action: async (c) => {
                              await del(`/tokens/${token.id}`, { confirm: c })
                              refresh()
                            },
                          })
                        }
                      >
                        <Trash />
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              ))}
              {data?.length === 0 && (
                <TableRow>
                  <TableCell colSpan={6} className="p-0">
                    <EmptyState
                      icon={Key}
                      title="No API keys yet"
                      description="Make one when a script, a CI job or another machine needs to talk to this dashboard."
                    />
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </PanelBody>
      </Panel>
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
 * The role picker explains each choice in a sentence because "limited" means
 * nothing to somebody making their first key, and a key may only ever narrow
 * its owner's role: the server enforces that too, so the picker only avoids a
 * pointless round trip.
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
                <Button
                  size="xs"
                  variant="outline"
                  onClick={() => copyText(secret, "Key copied")}
                >
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
          <div className="space-y-4">
            <Field
              label="Name"
              htmlFor="key-name"
              hint="Where it will live — a CI pipeline, a cron job, a laptop — so it is recognisable in this list later."
            >
              <Input
                id="key-name"
                value={name}
                autoFocus
                onChange={(e) => setName(e.target.value)}
                placeholder="github-actions"
              />
            </Field>
            <FieldRow>
              <Field label="Can" hint={ROLE_SUMMARY[role]}>
                <Select value={role} onValueChange={(v) => setRole(v as Role)}>
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {allowed.map((r) => (
                      <SelectItem key={r} value={r}>
                        {r}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field label="Expires" hint="A key that outlives its job is a key nobody is watching.">
                <Select value={ttl} onValueChange={setTtl}>
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {EXPIRY.map((e) => (
                      <SelectItem key={e.value} value={e.value}>
                        {e.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            </FieldRow>
            <FormNote>
              A key can do at most what your role can, and never more than it could at the time —
              if your account is demoted or disabled, every key it made goes with it. Keys cannot
              change passwords, make other keys or manage accounts; those need a real sign-in.
            </FormNote>
          </div>
        )}
      </Modal>
    </>
  )
}
