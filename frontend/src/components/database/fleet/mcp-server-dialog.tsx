"use client"

import { EXPIRY, useApiKeys, useApiOrigin } from "@/components/account/api-keys"
import { usableKeys } from "@/components/account/access"
import {
  MCP_CLIENTS,
  MCP_PATH,
  clientConfig,
  grantList,
  grantsOf,
  grantSummary,
  sameGrants,
  type Grants,
  type McpClient,
} from "@/components/database/fleet/mcp-server"
import { EngineGlyph, EnvironmentTag } from "@/components/database/kit"
import { useDatabases } from "@/components/database/shell/databases-context"
import { useConfirm } from "@/components/confirm-dialog"
import { Segments } from "@/components/deploy/settings/segments"
import { useNow } from "@/components/deploy/vocabulary"
import { Field, FormNote, FormSection } from "@/components/form"
import { Check, Copy, Key, Pencil, Plus, Slash, Sparkles } from "@/components/icons"
import { Modal } from "@/components/modal"
import { Well } from "@/components/panel"
import { ProductLogo } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { VerbActions } from "@/components/verbs"
import { useAuth } from "@/hooks/use-auth"
import { useCopy } from "@/hooks/use-copy"
import { del, post, put } from "@/lib/api"
import { keyProduct } from "@/lib/clients"
import { calendarDate, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { ApiToken, DbConnection } from "@/lib/types"
import { useRef, useState } from "react"

/**
 * Where an AI client reaches the databases: the MCP endpoint, the keys made
 * for it, and the databases each key was given. A key here opens the MCP
 * endpoint and nothing else, and reaches only what it was given, each either
 * to read or to change; the server holds every call to that and to its
 * owner's role, refuses protected databases outright, and audits each call.
 * It opens from the control center beside Metrics export, because a key
 * spans databases.
 */
export function McpServer() {
  const [open, setOpen] = useState(false)
  const opener = useRef<HTMLButtonElement>(null)
  return (
    <>
      <Button ref={opener} size="sm" variant="outline" onClick={() => setOpen(true)}>
        <Sparkles className="size-4" />
        MCP server
      </Button>
      {open && (
        <McpServerDialog
          onClose={() => setOpen(false)}
          onCloseAutoFocus={(event) => {
            event.preventDefault()
            opener.current?.focus()
          }}
        />
      )}
    </>
  )
}

type View =
  | { kind: "keys" }
  | { kind: "new"; draft: Draft }
  | { kind: "edit"; token: ApiToken; draft: Draft }
  | { kind: "made"; token: ApiToken; secret: string }

/** A key being made or re-pointed. The name and expiry are a new key's alone. */
type Draft = { name: string; ttl: string; grants: Grants }

function McpServerDialog({
  onClose,
  onCloseAutoFocus,
}: {
  onClose: () => void
  onCloseAutoFocus: (event: Event) => void
}) {
  const origin = useApiOrigin()
  const { status, can } = useAuth()
  const { connections } = useDatabases()
  const keys = useApiKeys()
  const now = useNow(30_000)
  const { confirm, dialog } = useConfirm()
  const [view, setView] = useState<View>({ kind: "keys" })
  const [busy, setBusy] = useState(false)
  const me = status?.user?.id
  // The query route's own question of its caller, which the endpoint asks too.
  const canRun = can("service.control")
  const mine = usableKeys(keys.data ?? [], now).filter(
    (token) => token.scope === "mcp" && token.userId === me,
  )
  const nameOf = (id: number) => connections.find((conn) => conn.id === id)?.name

  const revoke = (token: ApiToken) =>
    confirm({
      title: "Revoke MCP key",
      confirmLabel: "Revoke",
      subject: {
        name: token.name,
        mark: <ProductLogo id={keyProduct(token.name)} size="sm" fallback={Key} />,
        facts: <span className="font-mono">{token.prefix}…</span>,
      },
      description: (
        <p>
          Every client using this key loses its databases at its next call. The record stays under
          Account → API keys.
        </p>
      ),
      action: async () => {
        await del(`/tokens/${token.id}`)
        keys.refresh()
      },
    })

  const back = () => setView({ kind: "keys" })

  const save = async () => {
    if (view.kind !== "new" && view.kind !== "edit") return
    const { draft } = view
    const connections = grantList(draft.grants)
    setBusy(true)
    try {
      if (view.kind === "edit") {
        const saved = await put<ApiToken>(`/tokens/${view.token.id}/connections`, {
          connections,
        })
        notify.success(
          `${saved.name} reaches ${connections.length === 1 ? "1 database" : `${connections.length} databases`}`,
        )
        setView({ kind: "keys" })
      } else {
        const made = await post<{ token: ApiToken; secret: string }>("/tokens/mcp", {
          name: draft.name.trim(),
          ttlDays: Number(draft.ttl),
          connections,
        })
        setView({ kind: "made", token: made.token, secret: made.secret })
      }
      keys.refresh()
    } catch (err) {
      notify.error(
        view.kind === "edit" ? "Could not change the key's databases" : "Could not create the key",
        err,
      )
    } finally {
      setBusy(false)
    }
  }
  const ready =
    (view.kind === "new" &&
      view.draft.name.trim() !== "" &&
      grantList(view.draft.grants).length > 0) ||
    (view.kind === "edit" &&
      grantList(view.draft.grants).length > 0 &&
      !sameGrants(view.draft.grants, grantsOf(view.token)))

  return (
    <>
      <Modal
        open
        onOpenChange={(next) => !next && onClose()}
        size="lg"
        title={
          view.kind === "new"
            ? "New MCP key"
            : view.kind === "edit"
              ? `Databases for ${view.token.name}`
              : view.kind === "made"
                ? "Your new MCP key"
                : "MCP server"
        }
        description="Let an AI client such as Claude Code, Cursor or VS Code read and query the databases you choose."
        footer={
          view.kind === "new" || view.kind === "edit" ? (
            <>
              <Button variant="outline" onClick={back} disabled={busy}>
                Cancel
              </Button>
              <Button onClick={() => void save()} disabled={!ready || busy} pending={busy}>
                {view.kind === "edit" ? "Save databases" : "Create key"}
              </Button>
            </>
          ) : (
            <Button onClick={view.kind === "made" ? back : onClose}>Done</Button>
          )
        }
        onCloseAutoFocus={onCloseAutoFocus}
      >
        {view.kind === "new" || view.kind === "edit" ? (
          <KeyFields
            editing={view.kind === "edit"}
            draft={view.draft}
            onChange={(draft) => setView({ ...view, draft })}
            connections={connections}
            canRun={canRun}
            busy={busy}
          />
        ) : view.kind === "made" ? (
          <MadeKey origin={origin} secret={view.secret} />
        ) : (
          <div className="min-w-0 space-y-8">
            <FormSection title="Endpoint">
              <Field
                label="Server URL"
                hint="Streamable HTTP, with an MCP key as the bearer token. No other key and no browser session opens it."
                trailing={<CopyButton label="Copy URL" text={`${origin}${MCP_PATH}`} />}
              >
                <Well className="break-all">{`${origin}${MCP_PATH}`}</Well>
              </Field>
            </FormSection>

            <FormSection
              title="Keys"
              actions={
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() =>
                    setView({ kind: "new", draft: { name: "claude-code", ttl: "90", grants: {} } })
                  }
                >
                  <Plus className="size-4" />
                  New key
                </Button>
              }
            >
              {keys.error && !keys.data ? (
                <ErrorState error={keys.error} onRetry={keys.refresh} retryable />
              ) : !keys.data ? (
                <LoadingRows rows={2} />
              ) : mine.length === 0 ? (
                <EmptyNote>
                  No MCP keys yet. A key reaches only the databases you give it.
                </EmptyNote>
              ) : (
                <RowList aria-label="MCP keys">
                  {mine.map((token) => (
                    <Row
                      key={token.id}
                      className="group"
                      leading={<ProductLogo id={keyProduct(token.name)} size="sm" fallback={Key} />}
                      title={token.name}
                      subtitle={
                        <>
                          <span className="font-mono">{token.prefix}…</span> ·{" "}
                          {grantSummary(token, nameOf)}
                        </>
                      }
                      trailing={
                        <span
                          className="hidden text-xs text-muted-foreground sm:inline"
                          title={
                            token.expiresAt
                              ? `Expires ${calendarDate(token.expiresAt)}`
                              : "No expiry"
                          }
                        >
                          {token.lastUsedAt
                            ? `used ${relativeTime(token.lastUsedAt)}`
                            : "never used"}
                        </span>
                      }
                    >
                      <VerbActions
                        dim
                        menuLabel={`More actions for ${token.name}`}
                        verbs={[
                          {
                            key: "edit",
                            label: "Edit databases",
                            icon: Pencil,
                            inline: true,
                            run: () =>
                              setView({
                                kind: "edit",
                                token,
                                draft: { name: token.name, ttl: "0", grants: grantsOf(token) },
                              }),
                          },
                          {
                            key: "revoke",
                            label: "Revoke key",
                            icon: Slash,
                            danger: true,
                            run: () => revoke(token),
                          },
                        ]}
                      />
                    </Row>
                  ))}
                </RowList>
              )}
            </FormSection>

            <FormNote>
              A client can list the databases its key was given, read their structure, sample rows
              and explain or run read-only queries. Where the key may change a database it can run
              one statement at a time, held to your role: a delete, an overwrite or a drop needs an
              administrator. Protected databases are never reachable, and every call is in the audit
              log as the shape of its statement, without its values.
              {!canRun && " Your role runs no SQL, so your keys can read structure and rows only."}
            </FormNote>
          </div>
        )}
      </Modal>
      {dialog}
    </>
  )
}

/**
 * Making a key or re-pointing one: which databases, and for each whether the
 * key may change it. A database the server would refuse — a protected one,
 * an engine the tools do not speak — is listed with why, so the reader does
 * not go looking for it.
 */
function KeyFields({
  editing,
  draft,
  onChange,
  connections,
  canRun,
  busy,
}: {
  editing: boolean
  draft: Draft
  onChange: (draft: Draft) => void
  connections: DbConnection[]
  canRun: boolean
  busy: boolean
}) {
  const { engineFor } = useDatabases()
  const now = useNow(30_000)
  const { name, ttl, grants } = draft
  const setGrants = (next: Grants) => onChange({ ...draft, grants: next })

  const toggle = (id: number, on: boolean) => {
    const next = { ...grants }
    if (on) next[id] = "read"
    else delete next[id]
    setGrants(next)
  }

  return (
    <fieldset disabled={busy} className="min-w-0 space-y-6">
      {!editing && (
        <Field
          label="Name"
          htmlFor="mcp-key-name"
          hint="The client it is for, so it is recognisable here and in the audit log."
        >
          <div className="flex min-w-0 items-center gap-2">
            <ProductLogo id={keyProduct(name)} size="sm" fallback={Key} />
            <Input
              id="mcp-key-name"
              value={name}
              autoFocus
              onChange={(event) => onChange({ ...draft, name: event.target.value })}
              placeholder="claude-code"
            />
          </div>
        </Field>
      )}

      <Field
        label="Databases"
        hint={
          canRun
            ? "Read runs queries that only read. Change also runs one statement at a time that changes data or structure."
            : "Your role runs no SQL, so a key you make reads structure and rows only."
        }
      >
        {connections.length === 0 ? (
          <EmptyNote>There are no saved databases to give a key.</EmptyNote>
        ) : (
          <ul aria-label="Databases" className="divide-y divide-hairline rounded-lg border">
            {connections.map((conn) => {
              const engine = engineFor(conn)
              const refusal = conn.readOnly
                ? "protected, so no MCP key reaches it"
                : !engine.can("mcp")
                  ? `${engine.label} is not reachable over MCP`
                  : undefined
              const access = grants[conn.id]
              const id = `mcp-grant-${conn.id}`
              return (
                <li
                  key={conn.id}
                  className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2 px-3 py-2.5"
                >
                  <Checkbox
                    id={id}
                    checked={access !== undefined}
                    disabled={refusal !== undefined}
                    onCheckedChange={(checked) => toggle(conn.id, checked === true)}
                  />
                  <label htmlFor={id} className="flex min-w-0 flex-1 items-center gap-2 text-body">
                    <EngineGlyph engine={engine} />
                    <span className="truncate font-medium">{conn.name}</span>
                    {refusal ? (
                      <span className="truncate text-hint text-muted-foreground">{refusal}</span>
                    ) : (
                      conn.environment && <EnvironmentTag environment={conn.environment} />
                    )}
                  </label>
                  {access !== undefined && (
                    <Segments
                      label={`What the key may do in ${conn.name}`}
                      value={access}
                      disabled={!canRun}
                      onChange={(next) => setGrants({ ...grants, [conn.id]: next })}
                      options={[
                        { value: "read", label: "Read" },
                        { value: "write", label: "Change" },
                      ]}
                    />
                  )}
                </li>
              )
            })}
          </ul>
        )}
      </Field>

      {!editing && (
        <Field
          label="Expires"
          hint={
            ttl === "0"
              ? "The key works until you revoke it."
              : `The key expires ${calendarDate(new Date(now + Number(ttl) * 86_400_000).toISOString())}.`
          }
        >
          <ToggleGroup
            type="single"
            value={ttl}
            onValueChange={(next) => next && onChange({ ...draft, ttl: next })}
            variant="outline"
            size="sm"
            aria-label="Expires"
            className="w-full"
          >
            {EXPIRY.map((option) => (
              <ToggleGroupItem key={option.value} value={option.value} className="flex-1 text-xs">
                {option.label}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        </Field>
      )}

      {editing && (
        <Notice title="The key keeps working">
          A client already set up with it sees the new list at its next call; nothing needs pasting
          again.
        </Notice>
      )}
    </fieldset>
  )
}

/** The key, once, and what to paste where to use it. */
function MadeKey({ origin, secret }: { origin: string; secret: string }) {
  const [client, setClient] = useState<McpClient>("claude-code")
  const config = clientConfig(client, origin, secret)
  return (
    <div className="min-w-0 space-y-6">
      <Field
        label="Key"
        hint="Copy it now. It is stored hashed, so it cannot be shown again."
        trailing={<CopyButton label="Copy key" text={secret} />}
      >
        <Well className="break-all select-all">{secret}</Well>
      </Field>
      <Field
        label="Connect a client"
        hint={MCP_CLIENTS.find((c) => c.id === client)?.where}
        trailing={<CopyButton label="Copy configuration" text={config} />}
      >
        <div className="min-w-0 space-y-2">
          <Segments
            label="Client"
            fill
            value={client}
            onChange={setClient}
            options={MCP_CLIENTS.map((c) => ({ value: c.id, label: c.label }))}
          />
          <Well className={client === "claude-code" ? "break-all" : "whitespace-pre"}>
            {config}
          </Well>
        </div>
      </Field>
    </div>
  )
}

function CopyButton({ label, text }: { label: string; text: string }) {
  const { copy, copied } = useCopy()
  return (
    <Button size="xs" variant="outline" onClick={() => void copy(text)} aria-label={label}>
      {copied ? <Check /> : <Copy />}
      {copied ? "Copied" : "Copy"}
    </Button>
  )
}
