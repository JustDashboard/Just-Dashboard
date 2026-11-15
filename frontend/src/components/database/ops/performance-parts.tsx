"use client"

import { useCallback, useRef, useState } from "react"
import Link from "next/link"
import { ArrowDown, ArrowUp } from "@/components/icons"
import { cn } from "@/lib/utils"
import { ConfirmDialog, type ConfirmRequest } from "@/components/confirm-dialog"
import { FormNote } from "@/components/form"
import { EmptyState } from "@/components/state"
import { Status, type DotTone } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/chips"
import { Button } from "@/components/ui/button"
import { TableHead } from "@/components/ui/table"
import { nameHue, statementVerb } from "@/components/database/home/kinds"
import { EngineMark } from "@/components/database/kit"
import { oneLine } from "@/components/database/ops/queries"
import { SESSION_STATES } from "@/components/database/ops/performance-activity"
import type { DbSessionStatus } from "@/components/database/ops/performance-types"
import { useDatabase } from "@/components/database/shell/database-context"
import { statusLabel } from "@/components/database/shell/status"

/**
 * The small pieces the Performance views share, so that a name, a statement,
 * a session's state and a read that failed look the same in every one of
 * them.
 */

/**
 * A reading this server, or this account on it, does not give: the engine
 * drawn as itself, what is missing, and the server's own reason — which is
 * where the grant to ask for is named.
 */
export function NotAvailable({
  title,
  reason,
  action,
}: {
  title: string
  reason?: React.ReactNode
  action?: React.ReactNode
}) {
  const { engine } = useDatabase()
  return (
    <EmptyState
      mark={<EngineMark engine={engine} size="md" />}
      title={title}
      description={reason && <span className="wrap-anywhere">{reason}</span>}
      action={action}
    />
  )
}

/** The parts of a read the engine refused or cannot give, in its own sentences. */
export function Notes({ notes, className }: { notes?: string[]; className?: string }) {
  if (!notes?.length) return null
  return (
    <div className={cn("space-y-1", className)}>
      {notes.map((note) => (
        <FormNote key={note} className="wrap-anywhere">
          {note}
        </FormNote>
      ))}
    </div>
  )
}

/** A name — an account, an application, a schema — in its own hue, the same on every page that prints it. */
export function Named({
  name,
  mono,
  className,
}: {
  name: string
  mono?: boolean
  className?: string
}) {
  return (
    <span
      title={name}
      className={cn("min-w-0 truncate", mono && "font-mono", className)}
      style={{ color: nameHue(name) }}
    >
      {name}
    </span>
  )
}

/** A table by its schema and name: the schema in its hue, the name in ink. */
export function ObjectName({
  schema,
  name,
  className,
}: {
  schema?: string
  name: string
  className?: string
}) {
  return (
    <span className={cn("min-w-0 truncate font-mono", className)} title={`${schema ?? ""}.${name}`}>
      {schema && (
        <>
          <span style={{ color: nameHue(schema) }}>{schema}</span>
          <span className="text-muted-foreground">.</span>
        </>
      )}
      {name}
    </span>
  )
}

/**
 * A statement on one line, its first word in the hue of what it does — the
 * home's legend, so a list of statements is told apart before one is read.
 */
export function StatementLine({ text, className }: { text: string; className?: string }) {
  const line = oneLine(text)
  const verb = statementVerb(line)
  return (
    <span className={cn("block min-w-0 truncate font-mono", className)} title={line}>
      {verb ? (
        <>
          <span style={{ color: verb.color }}>{verb.word}</span>
          {verb.rest}
        </>
      ) : (
        line
      )}
    </span>
  )
}

const STATE_TONE: Record<DbSessionStatus, DotTone> = {
  active: "running",
  blocked: "warning",
  idle_in_transaction: "notice",
  idle: "stopped",
  background: "unknown",
}

/**
 * A session's state as a dot and a word. `late` is a state that has gone on
 * past its threshold: a blocked session then reads red, and one idle in a
 * transaction amber.
 */
export function SessionState({ status, late }: { status: DbSessionStatus; late?: boolean }) {
  const label = SESSION_STATES.find((state) => state.id === status)?.label ?? status
  const tone: DotTone =
    late && status === "blocked"
      ? "danger"
      : late && status === "idle_in_transaction"
        ? "warning"
        : STATE_TONE[status]
  return <Status tone={tone} label={label} />
}

/**
 * A column's head that orders the rows under it. The head says which way the
 * column runs (`aria-sort`), and the arrow is drawn only on the one in use.
 */
export function SortHead({
  label,
  active,
  descending,
  onSort,
  align = "left",
  className,
  after,
}: {
  label: string
  active: boolean
  descending: boolean
  onSort: () => void
  align?: "left" | "right"
  className?: string
  /** Something beside the head's button that is not part of it: what the column means, as an `InfoTip`. */
  after?: React.ReactNode
}) {
  const Arrow = descending ? ArrowDown : ArrowUp
  return (
    <TableHead
      aria-sort={active ? (descending ? "descending" : "ascending") : "none"}
      className={cn(align === "right" && "text-right", className)}
    >
      <button
        type="button"
        onClick={onSort}
        className={cn(
          "inline-flex items-center gap-1 rounded-sm focus-ring transition-colors hover:text-foreground",
          active && "text-foreground",
        )}
      >
        {label}
        {active && <Arrow aria-hidden className="size-3" />}
      </button>
      {after && <span className="ml-1 inline-flex align-middle">{after}</span>}
    </TableHead>
  )
}

/** A dash where a row has no figure: the engine does not count it. */
export function NoFigure() {
  return <span className="text-faint">—</span>
}

/**
 * Which schema a list is narrowed to, as chips: every schema the list spans,
 * with how many of its rows each holds. Drawn only where there is more than
 * one, and "All" is always among them — so a list is never narrowed by
 * something the page does not show.
 */
export function ScopeChips({
  schemas,
  scope,
  onScope,
  noun,
}: {
  schemas: { name: string; count: number }[]
  /** The schema chosen; empty is every schema. */
  scope: string
  onScope: (schema: string) => void
  /** What the engine calls a schema, in the plural: "schemas", "databases". */
  noun: string
}) {
  if (schemas.length < 2) return null
  const total = schemas.reduce((sum, schema) => sum + schema.count, 0)
  return (
    <ChipStrip role="group" aria-label={`Narrow to one of the ${noun}`}>
      <FilterChip selected={!scope} onClick={() => onScope("")}>
        All {noun}
        <ChipCount>{total.toLocaleString()}</ChipCount>
      </FilterChip>
      {schemas.map((schema) => (
        <FilterChip
          key={schema.name}
          selected={scope === schema.name}
          onClick={() => onScope(scope === schema.name ? "" : schema.name)}
        >
          <span className="font-mono" style={{ color: nameHue(schema.name) }}>
            {schema.name}
          </span>
          <ChipCount>{schema.count.toLocaleString()}</ChipCount>
        </FilterChip>
      ))}
    </ChipStrip>
  )
}

/**
 * What had the keyboard when a surface was asked for. A press inside a menu
 * is answered with the menu's own trigger: the item is gone by the time
 * anything closes.
 */
function opener(): HTMLElement | null {
  const active = document.activeElement
  if (!(active instanceof HTMLElement) || active === document.body) return null
  const trigger = active.closest('[role="menu"]')?.getAttribute("aria-labelledby")
  return (trigger && document.getElementById(trigger)) || active
}

/**
 * Hands the keyboard back to where it was when a panel, a confirmation or a
 * run's dialog closes.
 *
 * The shared sheet and dialog are opened by state, with no trigger of their
 * own to return to, so closing one left the keyboard on the page's body and
 * the next Tab started from the top. `remember` is called by the press that
 * opens the surface and `restore` when it closes; the element is given focus
 * once the surface has let go of it, and only if nothing else has taken it —
 * a second surface opened from the first keeps the keyboard.
 */
export function useReturnFocus() {
  const from = useRef<HTMLElement | null>(null)
  const region = useRef<HTMLElement | null>(null)
  const remember = useCallback((element?: HTMLElement | null) => {
    const asked = element === undefined ? opener() : element
    from.current = asked
    region.current = asked?.closest<HTMLElement>("[data-return-focus]") ?? null
  }, [])
  const restore = useCallback(() => {
    const element = from.current
    const around = region.current
    if (!element) return
    const deadline = performance.now() + 1500
    const attempt = () => {
      // What the surface did can have taken its own control away — a session
      // ended, a finding fixed. The keyboard then goes to the view it was in.
      const target = element.isConnected ? element : around?.isConnected ? around : null
      if (!target) return
      const active = document.activeElement
      if (!active || active === document.body) {
        target.focus({ preventScroll: true })
        if (target === element && around) keep(element, around)
      } else if (active !== target && performance.now() < deadline) requestAnimationFrame(attempt)
    }
    requestAnimationFrame(attempt)
  }, [])
  return { remember, restore, from }
}

/**
 * Keeps the keyboard in the view when the control it was handed back to is
 * taken away a moment later: the list is read again after an action, and the
 * row or the finding the action was about is often the one that goes.
 */
function keep(element: HTMLElement, around: HTMLElement) {
  const observer = new MutationObserver(() => {
    if (element.isConnected) return
    observer.disconnect()
    const active = document.activeElement
    if (around.isConnected && (!active || active === document.body)) {
      around.focus({ preventScroll: true })
    }
  })
  observer.observe(around, { childList: true, subtree: true })
  // The read that follows an action has answered long before this.
  setTimeout(() => observer.disconnect(), 20_000)
}

/**
 * What a view's panel carries so that it can take the keyboard when the
 * control that opened something is no longer there to take it back.
 */
export const RETURNS_FOCUS = { tabIndex: -1, "data-return-focus": "" } as const

/**
 * A reading table on a plain section, set on what holds it: its hairlines and
 * row washes end where the section's title hairline does, and its outer
 * cells start and end on the title's edges. The tables were bled out 16px on
 * each side so their cells' own padding lined the text up with the title,
 * which ran every row's hairline past the title's on both sides (PGB-11).
 * Important, so it also wins over the unlayered outer-column rule in
 * globals.css where the table is a flush body's own child.
 */
export const FIT_COLUMN = "[&_tr>*:first-child]:pl-0! [&_tr>*:last-child]:pr-0!"

export type ReturnFocus = ReturnType<typeof useReturnFocus>

/**
 * A confirmation that gives the keyboard back to the control that asked for
 * it, whichever way it closes. `useConfirm` with the one thing added.
 */
export function useAsk(focus?: ReturnFocus) {
  const [request, setRequest] = useState<ConfirmRequest | null>(null)
  const own = useReturnFocus()
  const { remember, restore } = focus ?? own
  const confirm = useCallback(
    (next: ConfirmRequest) => {
      remember()
      setRequest(next)
    },
    [remember],
  )
  const dialog = (
    <ConfirmDialog
      request={request}
      onOpenChange={(open) => {
        if (open) return
        setRequest(null)
        restore()
      }}
    />
  )
  return { confirm, dialog }
}

/** Whether the server is known not to be there to ask: stopped, paused, or a connection that cannot be opened. */
export function isDown(state: string): boolean {
  return state === "stopped" || state === "paused" || state === "broken"
}

/**
 * A page that reads the live server, on a server that is not answering. It
 * is asked nothing — a stopped server is not dialled by its own pages — and
 * the page says so, and where the server is started from.
 */
export function ServerDown({ what }: { what: string }) {
  const { conn, engine, status, href } = useDatabase()
  return (
    <EmptyState
      mark={<EngineMark engine={engine} size="md" />}
      title={`${conn.name} is ${statusLabel(status.state)}`}
      description={
        <>
          {what} is read from the running server, so there is nothing to show until it answers
          again.{status.error ? <span className="wrap-anywhere"> {status.error}</span> : null}
        </>
      }
      action={
        <span className="flex flex-wrap justify-center gap-2">
          <Button size="sm" variant="outline" asChild>
            <Link href={href("home")}>Open the database&apos;s Home</Link>
          </Button>
          <Button size="sm" variant="ghost" onClick={status.refresh}>
            Check again
          </Button>
        </span>
      }
    />
  )
}
