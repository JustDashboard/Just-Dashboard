"use client"

import { duration } from "@/lib/format"
import { cn } from "@/lib/utils"
import { ProductLogo } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { KEYCAP, Keycaps } from "./keycaps"
import { KINDS, ResultMark } from "./marks"
import type { Fact } from "./inventory"
import type { SearchItem } from "./model"

type Glyph = React.ComponentType<{ className?: string }>

export type PreviewItem = SearchItem & {
  icon?: Glyph
  product?: string
  state?: string
  keys?: string
  facts?: Fact[]
  /** When a recent destination was arrived at. */
  at?: number
  /** What a command does, in a sentence, where its label alone does not say. */
  hint?: string
  /** The rail's list the page sits in, so the preview can show where it is. */
  neighbours?: { label: string; pages: { href?: string; title: string; icon: Glyph }[] }
  /** See `InventoryItem.links`. */
  links?: string[]
  /** The other resources sharing one of its links. */
  connected?: PreviewItem[]
  previous?: boolean
  current?: boolean
}

export type Destination = { href: string; title: string; detail?: string; product?: string }

/** "4m ago": one unit is enough for a row, and it does not tick while it is read. */
export function visited(at: number) {
  const seconds = (Date.now() - at) / 1000
  return seconds < 45 ? "just now" : `${duration(seconds).split(" ")[0]} ago`
}

/**
 * The selected result, said in full beside the list.
 *
 * A row has one line, and fifty of them in a column are only findable if
 * that line is short. What the row leaves out — the image a container runs,
 * every domain a site answers, which pages sit beside the one about to open —
 * is here, and it follows the selection as the arrows move it, so walking
 * the list reads the dashboard rather than a column of names.
 *
 * Enter takes the selected result. The pages and resources listed under it
 * open on a click and are out of the tab order: the combobox owns the
 * keyboard, and every one of them is a result the same words can reach.
 */
export function Preview({
  item,
  onOpen,
}: {
  item: PreviewItem
  onOpen: (destination: Destination) => void
}) {
  const verb = item.kind === "command" ? "Run" : item.previous ? "Go back" : "Open"
  return (
    <aside
      aria-label="Selected result"
      className="hidden min-h-0 flex-col border-l border-hairline lg:flex"
    >
      {/* Keyed by the result, so each selection arrives rather than repaints
          the last one's words in place (§11 *arrived*). */}
      <div
        key={item.id}
        className="flex min-h-0 flex-1 animate-rise flex-col gap-5 overflow-y-auto px-5 pt-5 pb-4"
      >
        <div className="flex items-start gap-3">
          <ProductLogo id={item.product} fallback={item.icon ?? KINDS[item.kind].icon} />
          <div className="min-w-0 flex-1 pt-0.5">
            <p className="truncate text-hint text-muted-foreground">{eyebrow(item)}</p>
            <h3 className="text-title font-semibold break-words text-foreground">
              {item.previous && "Back to "}
              {item.title}
            </h3>
          </div>
        </div>

        {(item.state || item.current || item.previous) && (
          <p className="text-body text-muted-foreground">
            {item.state ? (
              <Status state={item.state} label={item.state} className="text-body" />
            ) : item.current ? (
              "You are on this page."
            ) : (
              "Where you were before this page."
            )}
          </p>
        )}

        {item.hint && <p className="text-body text-muted-foreground">{item.hint}</p>}

        {item.keys && (
          <div className="flex items-center justify-between gap-3 text-body text-muted-foreground">
            Shortcut
            <Keycaps keys={item.keys} />
          </div>
        )}

        {!!item.facts?.length && (
          <dl className="grid grid-cols-[6rem_minmax(0,1fr)] gap-x-3 gap-y-2.5 text-body">
            {item.facts.map((fact) => (
              <div key={fact.label} className="contents">
                <dt className="text-muted-foreground">{fact.label}</dt>
                <dd
                  className={cn(
                    "min-w-0 whitespace-pre-line text-foreground",
                    fact.mono ? "font-mono text-hint leading-5 break-all" : "break-words",
                  )}
                >
                  {fact.value}
                </dd>
              </div>
            ))}
          </dl>
        )}

        {!!item.connected?.length && (
          <Related label="Connected">
            {item.connected.map((other) => (
              <RelatedLink
                key={other.id}
                onClick={
                  other.href
                    ? () =>
                        onOpen({
                          href: other.href!,
                          title: other.title,
                          detail: other.detail,
                          product: other.product,
                        })
                    : undefined
                }
              >
                <ResultMark product={other.product} icon={other.icon ?? KINDS[other.kind].icon} />
                <span className="min-w-0 truncate text-foreground">{other.title}</span>
                <span className="ml-auto shrink-0 text-hint text-muted-foreground">
                  {KINDS[other.kind].noun}
                </span>
              </RelatedLink>
            ))}
          </Related>
        )}

        {item.neighbours && (
          <Related label={`In ${item.neighbours.label}`}>
            {item.neighbours.pages.map((page) => {
              const here = page.href === item.href
              return (
                <RelatedLink
                  key={page.href ?? page.title}
                  onClick={
                    page.href && !here
                      ? () => onOpen({ href: page.href!, title: page.title })
                      : undefined
                  }
                  className={here ? "text-foreground" : "text-muted-foreground"}
                >
                  <page.icon className={cn("size-4 shrink-0", here && "text-brand")} />
                  <span className="truncate">{page.title}</span>
                </RelatedLink>
              )
            })}
          </Related>
        )}
      </div>
      <div className="flex shrink-0 items-center justify-between gap-3 border-t border-hairline px-5 py-2.5 text-hint text-muted-foreground">
        <span className="min-w-0 truncate font-mono">{item.href}</span>
        <span className="inline-flex shrink-0 items-center gap-1.5">
          <kbd className={KEYCAP}>↵</kbd>
          {verb}
        </span>
      </div>
    </aside>
  )
}

function Related({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <p className="eyebrow">{label}</p>
      <ul className="-mx-2 flex flex-col">{children}</ul>
    </div>
  )
}

function RelatedLink({
  onClick,
  className,
  children,
}: {
  onClick?: () => void
  className?: string
  children: React.ReactNode
}) {
  const row = cn(
    "flex min-h-8 w-full items-center gap-2.5 rounded-md px-2 text-left text-body",
    className,
  )
  return (
    <li>
      {onClick ? (
        <button
          type="button"
          tabIndex={-1}
          onClick={onClick}
          className={cn(row, "transition-colors hover:bg-menu-hover")}
        >
          {children}
        </button>
      ) : (
        <span className={row}>{children}</span>
      )}
    </li>
  )
}

function eyebrow(item: PreviewItem) {
  if (item.kind === "recent") return item.at ? `Visited ${visited(item.at)}` : "Visited"
  if (item.kind === "page") return item.detail
  if (item.kind === "command") return item.detail ? `Command · ${item.detail}` : "Command"
  return KINDS[item.kind].noun
}
