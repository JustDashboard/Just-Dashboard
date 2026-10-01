"use client"

import { useRef, useState } from "react"
import { Check, Copy, Eye, EyeOff, Linked } from "@/components/icons"
import { get } from "@/lib/api"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useSessionState } from "@/lib/view-state"
import type { DbAccess } from "@/lib/types"
import { useCopy } from "@/hooks/use-copy"
import { usePoll } from "@/hooks/use-poll"
import { IconAction } from "@/components/icon-action"
import { Well } from "@/components/panel"
import { FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Segments } from "@/components/deploy/settings/segments"
import { EngineMark } from "@/components/database/kit/engine-mark"
import {
  connectFormats,
  connectSnippet,
  connectTargets,
  holdsSecret,
  maskedParts,
  revealedParts,
  type ConnectTargetId,
} from "@/components/database/shell/connection-string"
import { useDatabase } from "@/components/database/shell/database-context"
import { useDatabases } from "@/components/database/shell/databases-context"

/**
 * Connect: the string an application is given, where it is asked for.
 *
 * After the first day this is what an operator opens a database's page to
 * get, so it is one press from every page of the database rather than a
 * panel on one of them. It asks the two questions a string depends on — where
 * the program runs (on this server, in a container linked to the database, or
 * anywhere) and what will read it (a URL, an `.env` line, the engine's own
 * shell, a client library) — and draws the answer with the password hidden.
 *
 * The hidden one is built from what the page already holds, so opening the
 * popover reveals nothing and audits nothing. Show and Copy read the real
 * string from `GET /databases/{id}/url`, which only an administrator may call
 * and which is recorded each time; for any other role those two controls are
 * not drawn. A shape that holds no password — a shell that asks for it, a
 * file's path — is copied as it stands by anyone.
 */
export function ConnectPopover({ className }: { className?: string }) {
  const { conn, engine } = useDatabase()
  const { admin } = useDatabases()
  const { copy, copied } = useCopy()
  const [open, setOpen] = useState(false)
  const [from, setFrom] = useState<ConnectTargetId>("host")
  const [format, setFormat] = useSessionState("databases.connect.format", "url")
  // The real string per target, once shown; pressing again hides it.
  const [shown, setShown] = useState<Partial<Record<ConnectTargetId, string>>>({})
  const [busy, setBusy] = useState(false)

  // Where the server is published is an administrator's reading, asked for
  // when the popover opens and not before.
  const access = usePoll(
    (signal) => get<DbAccess>(`/databases/${conn.id}/access`, undefined, signal),
    0,
    [conn.id],
    { enabled: open && admin && engine.can("server") },
  )

  const targets = connectTargets(conn, engine, access.data)
  const target = targets.find((t) => t.id === from) ?? targets[0]
  const formats = connectFormats(engine)
  const shape = formats.some((f) => f.id === format) ? format : "url"

  const masked = maskedParts(conn, engine, target)
  const real = shown[target.id]
  const text = connectSnippet(engine, real ? revealedParts(masked, engine, real) : masked, shape)
  const secret = holdsSecret(connectSnippet(engine, masked, shape))

  // Which opening of the popover this is. The real string is asked for while
  // it is open and may arrive after it has closed; an answer that belongs to
  // an earlier opening is dropped, so a password is never put on the screen
  // or the clipboard of somebody who has already looked away from it.
  const opening = useRef(0)

  /** The real string, or `null` when the popover closed while it was being read. */
  const read = async () => {
    const asked = opening.current
    const { url } = await get<{ url: string }>(`/databases/${conn.id}/url`, { target: target.id })
    return asked === opening.current ? url : null
  }

  const reveal = async () => {
    if (real) {
      setShown((s) => ({ ...s, [target.id]: undefined }))
      return
    }
    setBusy(true)
    try {
      const url = await read()
      if (url !== null) setShown((s) => ({ ...s, [target.id]: url }))
    } catch (err) {
      notify.error("Could not read the connection string", err)
    } finally {
      setBusy(false)
    }
  }

  const copyIt = async () => {
    if (!secret) {
      await copy(text, "Copied")
      return
    }
    setBusy(true)
    try {
      const url = real ?? (await read())
      if (url !== null) {
        await copy(
          connectSnippet(engine, revealedParts(masked, engine, url), shape),
          "Connection string copied",
        )
      }
    } catch (err) {
      notify.error("Could not read the connection string", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        setOpen(next)
        // A revealed password does not outlive the popover it was shown in,
        // and one still on its way is not shown in the next.
        if (!next) {
          opening.current += 1
          setShown({})
        }
      }}
    >
      <PopoverTrigger asChild>
        <Button size="sm" variant="outline" className={className}>
          <Linked className="size-3.5" />
          {/* On a phone the strip has room for the glyph alone. */}
          <span className="max-sm:sr-only">Connect</span>
        </Button>
      </PopoverTrigger>
      <PopoverContent
        align="end"
        data-slot="connect-popover"
        className="flex w-[30rem] max-w-[calc(100vw-1.5rem)] flex-col gap-3 rounded-lg p-3"
      >
        <div className="flex min-w-0 items-center gap-2.5">
          <EngineMark engine={engine} size="sm" />
          <div className="min-w-0">
            <p className="truncate text-body font-medium">{conn.name}</p>
            <p className="truncate text-hint text-muted-foreground">
              {engine.label}
              {conn.user ? ` · as ${conn.user}` : ""}
            </p>
          </div>
        </div>

        {targets.length > 1 && (
          <Segments
            label="Connect from"
            value={target.id}
            options={targets.map((t) => ({ value: t.id, label: t.label }))}
            onChange={setFrom}
            fill
          />
        )}

        {target.unavailable ? (
          <Well plain className="text-hint leading-relaxed text-muted-foreground">
            {target.unavailable}
          </Well>
        ) : (
          <>
            <div className="flex min-w-0 items-center justify-between gap-2">
              <div role="group" aria-label="Shape" className="flex min-w-0 flex-wrap gap-1">
                {formats.map((f) => (
                  <FilterChip
                    key={f.id}
                    selected={shape === f.id}
                    onClick={() => setFormat(f.id)}
                    className={cn(f.id === "cli" && "font-mono")}
                  >
                    {f.label}
                  </FilterChip>
                ))}
              </div>
              {(admin || !secret) && (
                <div className="flex shrink-0 items-center gap-1 self-start">
                  {secret && (
                    <IconAction
                      label={real ? "Hide the password" : "Show the password"}
                      onClick={reveal}
                      disabled={busy}
                    >
                      {real ? <EyeOff /> : <Eye />}
                    </IconAction>
                  )}
                  <Button size="sm" variant="outline" onClick={copyIt} disabled={busy}>
                    {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
                    Copy
                  </Button>
                </div>
              )}
            </div>
            <Well
              data-slot="connection-string"
              className={cn(
                "max-h-48 break-all whitespace-pre-wrap select-all",
                real ? "text-foreground" : "text-muted-foreground",
              )}
            >
              {text}
            </Well>
            <p className="text-hint text-pretty text-muted-foreground">
              {target.note}
              {secret && !admin && " Only an administrator can show or copy the password."}
            </p>
          </>
        )}
      </PopoverContent>
    </Popover>
  )
}
