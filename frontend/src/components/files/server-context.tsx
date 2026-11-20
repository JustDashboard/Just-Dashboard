"use client"

import { useEffect, useSyncExternalStore } from "react"
import Link from "next/link"
import { get } from "@/lib/api"
import { cn } from "@/lib/utils"
import {
  Archive,
  ArrowUpRight,
  Database,
  FolderOpen,
  Globe,
  LockClosed,
  Rocket,
  Warning,
} from "@/components/icons"
import { PaneSection } from "@/components/panel"
import { LogoGlyph } from "@/components/logo"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import {
  entryFacts,
  factProduct,
  partialSentence,
  phrase,
  rowFact,
  sentence,
} from "@/components/files/server-context-words"
import type { ContextFact, FileContext } from "@/components/files/server-context-types"

/**
 * What each folder and file is to this server, on the Files page: one phrase
 * on a row, a mark on a tile, and a section of sentences in the details pane.
 *
 * `useServerContextFeed` asks `GET /files/context` once the listing it
 * describes is on screen, and again whenever the listing is read again (its
 * poll, a change made here), so the facts follow the folder. The listing never
 * waits for it: an answer that is slow, refused or failed is simply no facts,
 * and every row is drawn exactly as it would be without this.
 *
 * The answer is kept in a small store rather than a React context so the page
 * feeds it with one hook call instead of wrapping its whole tree; there is one
 * Files page per document, and the store is emptied when it unmounts.
 */
type Held = { path: string; hidden: boolean; context: FileContext }

let held: Held | null = null
const listeners = new Set<() => void>()

function publish(next: Held | null) {
  held = next
  for (const listener of listeners) listener()
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

function useHeld(): Held | null {
  return useSyncExternalStore(
    subscribe,
    () => held,
    () => null,
  )
}

export function useServerContextFeed(path: string | null, hidden: boolean, listed: unknown) {
  useEffect(() => {
    if (!path || !listed) return
    const controller = new AbortController()
    // A task after the rows are committed, so the request never competes
    // with drawing them.
    const timer = setTimeout(() => {
      get<FileContext>("/files/context", { path, hidden }, controller.signal)
        .then((context) => publish({ path, hidden, context }))
        // No facts is the whole of a failure here: the listing is the page.
        .catch(() => undefined)
    }, 0)
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
  }, [path, hidden, listed])
  useEffect(() => () => publish(null), [])
}

/** The fallback glyph for a fact whose thing has no product mark. */
const GLYPH: Partial<Record<ContextFact["kind"], React.ComponentType<{ className?: string }>>> = {
  "deployment-source": Rocket,
  "deployment-data": Rocket,
  backup: Archive,
  "backup-target": Archive,
  "backup-excluded": Archive,
  "not-backed-up": Warning,
  "database-data": Database,
  "database-config": Database,
  "site-root": Globe,
  "site-config": Globe,
  "site-log": Globe,
  certificate: LockClosed,
}

/** A fact's mark at the size of a line of text: the product's logo, else a glyph. */
function FactMark({ fact, className }: { fact: ContextFact; className?: string }) {
  const product = factProduct(fact)
  if (product === "jd") {
    return <LogoGlyph small className={cn("size-3.5 shrink-0 text-brand", className)} />
  }
  if (product && hasProductLogo(product)) {
    return <ProductGlyph id={product} className={className} />
  }
  const Glyph = GLYPH[fact.kind] ?? FolderOpen
  return (
    <Glyph
      aria-hidden
      className={cn(
        "size-3.5 shrink-0",
        fact.kind === "not-backed-up" ? "text-warning" : "text-muted-foreground",
        className,
      )}
    />
  )
}

/**
 * The most telling fact about a row's entry, after its name and in the same
 * line. It gives way first: it shrinks a thousand times faster than the name
 * and ends in an ellipsis, so a long name keeps its room and the row keeps
 * its one height. A row with nothing known draws nothing here.
 */
export function EntryContextPhrase({ path }: { path: string }) {
  const fact = rowFact(useHeld()?.context.children[path])
  if (!fact) return null
  const words = phrase(fact)
  return (
    <span
      data-context-phrase
      title={words}
      className="flex min-w-0 shrink-[1000] items-center gap-1.5 text-hint leading-none text-muted-foreground"
    >
      <FactMark fact={fact} />
      <span className="truncate">{words}</span>
    </span>
  )
}

/**
 * The same fact on a tile: its mark in the tile's top corner, the phrase as
 * its tooltip and its name for a screen reader. Placed over the tile's
 * corner, so a tile with it and one without are the same size.
 */
export function TileContextMark({ path }: { path: string }) {
  const fact = rowFact(useHeld()?.context.children[path])
  if (!fact) return null
  const words = phrase(fact)
  return (
    <span
      data-context-mark
      role="img"
      aria-label={words}
      title={words}
      className="absolute top-1.5 right-1.5 z-raised flex size-5 items-center justify-center rounded-md border border-hairline bg-card"
    >
      <FactMark fact={fact} />
    </span>
  )
}

/**
 * Everything known about the entry the details pane shows, as sentences that
 * lead to the page owning each thing: its own facts first, then what it is
 * inside. With nothing chosen it is the folder on screen, which is where "not
 * backed up" is said. A source that did not answer in time is named under
 * them, so a short list is not mistaken for a complete one.
 */
export function ServerContextSection({ path, current }: { path: string; current: boolean }) {
  const answer = useHeld()
  // The folder on screen is the one the page asked about, by the path it
  // asked with: an answer for the folder before is never said of this one.
  if (!answer || (current && answer.path !== path)) return null
  const context = answer.context
  const facts = current ? context.facts : entryFacts(context, path)
  const note = partialSentence(context.partial)
  if (facts.length === 0 && !note) return null
  // Ages are told from when the server read the facts, which a poll keeps
  // within twenty seconds of now.
  const now = Date.parse(context.checkedAt)
  return (
    <PaneSection title="On this server">
      {facts.length > 0 && (
        <ul data-server-context className="space-y-2">
          {facts.map((fact, i) => (
            <li key={`${fact.kind}:${fact.href ?? ""}:${fact.from ?? ""}:${i}`}>
              <FactLine fact={fact} now={now} />
            </li>
          ))}
        </ul>
      )}
      {note && <p className="text-hint text-faint">{note}</p>}
    </PaneSection>
  )
}

function FactLine({ fact, now }: { fact: ContextFact; now: number }) {
  const words = sentence(fact, now)
  const body = (
    <>
      <FactMark fact={fact} className="mt-[3px]" />
      <span className="min-w-0 flex-1 break-words">{words}</span>
      {fact.href && (
        <ArrowUpRight
          aria-hidden
          className="mt-[3px] size-3.5 shrink-0 text-faint group-hover:text-foreground"
        />
      )}
    </>
  )
  const ink = fact.from ? "text-muted-foreground" : "text-foreground"
  if (!fact.href) {
    return <span className={cn("flex items-start gap-2 text-body", ink)}>{body}</span>
  }
  return (
    <Link
      href={fact.href}
      data-context-kind={fact.kind}
      className={cn(
        "group flex items-start gap-2 rounded-sm text-body focus-ring hover:text-foreground",
        ink,
      )}
    >
      {body}
    </Link>
  )
}
