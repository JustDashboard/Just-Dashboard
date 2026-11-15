import { Children, Fragment, cloneElement, isValidElement } from "react"
import { cn } from "@/lib/utils"
import { ProductGlyph } from "@/components/product-logo"

/**
 * The one line of facts under a name: "3 sites · 2 on TLS · via tailscale0".
 *
 * Facts on one line that wraps, with a dot between neighbours and none where
 * the line breaks. A dot drawn as an item of its own (`FactDot`) is left at
 * the end of a line when the next fact wraps, where it reads as a stray mark.
 * Here each fact carries its dot in front of it, in the gap, and the line is
 * drawn one gap to the left of a frame that clips it: the dot before a line's
 * first fact falls outside the frame, whichever fact the wrap makes first.
 *
 * The dot is real text, " · ", so a line's `textContent` still reads as the
 * sentence it is ("3 sites · 2 on TLS"), and it is `aria-hidden`, so a screen
 * reader says the facts and not the punctuation. The line's first fact has no
 * dot at all.
 *
 * `nowrap` is for a row that holds one line whatever its width: the facts
 * truncate instead of wrapping.
 *
 * A `FactDot` among the children is read as the old way of saying "a new fact
 * starts here": it is dropped, and what stood between two of them becomes one
 * fact, so a line still written with dots draws through this one unchanged in
 * its words.
 */
export function FactLine({
  nowrap,
  className,
  children,
  ...props
}: React.ComponentProps<"span"> & { nowrap?: boolean }) {
  return (
    <span
      data-slot="fact-line"
      className={cn("block max-w-full min-w-0 overflow-hidden", className)}
      {...props}
    >
      <span
        className={cn(
          "-ml-4 flex items-center gap-y-0.5",
          nowrap ? "flex-nowrap *:shrink *:truncate" : "flex-wrap",
        )}
      >
        {factItems(children)}
      </span>
    </span>
  )
}

/**
 * One fact in a `FactLine`. `product` draws that product's glyph before the
 * words, `mono` sets a literal from the host (a path, an address) in mono.
 * `title` goes to the fact's own words, so what a hover explains, and what a
 * test reads by it, is the fact without the dot before it; `className` is
 * the whole fact's, so a fact hidden on a phone takes its dot with it.
 */
export function Fact({
  product,
  mono,
  title,
  className,
  children,
  lead,
  ...props
}: Omit<React.ComponentProps<"span">, "title"> & {
  product?: string
  mono?: boolean
  title?: string
  /** Set by `FactLine` on its first fact, which has no dot before it. */
  lead?: boolean
}) {
  return (
    <span
      data-slot="fact"
      // Mono is the whole fact's, not an inline span's: a span in another
      // face inside the line box grew the line a pixel past its 16.
      className={cn("relative max-w-full min-w-0 pl-4", mono && "font-mono", className)}
      {...props}
    >
      {!lead && (
        <span aria-hidden className="absolute inset-y-0 left-0 w-4 text-center text-faint">
          {" · "}
        </span>
      )}
      <span
        title={title}
        className={
          product ? "inline-flex max-w-full min-w-0 items-center gap-1.5 align-top" : undefined
        }
      >
        {product && <ProductGlyph id={product} />}
        {children}
      </span>
    </span>
  )
}

/**
 * Facts from data: each entry that is not empty becomes a `Fact`, in order.
 * `facts([count && \`${count} sites\`, tls])` reads as the line it draws.
 */
export function facts(list: readonly React.ReactNode[]) {
  return list
    .filter((fact) => fact !== null && fact !== undefined && fact !== false && fact !== "")
    .map((fact, index) => <Fact key={index}>{fact}</Fact>)
}

/**
 * The old separator, a dot drawn as an item of its own. Inside a `FactLine`
 * (and so in an identity header's facts) it only marks where one fact ends
 * and the next begins; on its own it still draws, in `--faint`, for the rows
 * the page passes have not moved yet.
 *
 * @deprecated use `FactLine` and `Fact`; WP-39 bans this.
 */
export function FactDot() {
  return <span className="text-faint">·</span>
}

/** Children with fragments opened and nothing-renders (`false`, `null`) left out. */
function flatten(children: React.ReactNode, prefix = ""): React.ReactNode[] {
  const out: React.ReactNode[] = []
  // `toArray` keys every child by its place; the prefix keeps the keys of a
  // fragment's children apart from their neighbours' once they share a list.
  for (const child of Children.toArray(children)) {
    if (!isValidElement<{ children?: React.ReactNode }>(child)) {
      out.push(child)
    } else if (child.type === Fragment) {
      out.push(...flatten(child.props.children, `${prefix}${child.key}/`))
    } else {
      out.push(prefix ? cloneElement(child, { key: `${prefix}${child.key}` }) : child)
    }
  }
  return out
}

/** Whether a run of children is written as facts: with `Fact`s or with `FactDot`s between them. */
export function hasFacts(children: React.ReactNode) {
  return flatten(children).some(
    (child) => isValidElement(child) && (child.type === Fact || child.type === FactDot),
  )
}

/**
 * The line's items: every `Fact` as itself, and every run of other nodes
 * between two `FactDot`s (or a `Fact`) as one fact whose parts keep the gap
 * they had in the old flex line. The first item is marked as the lead.
 */
function factItems(children: React.ReactNode) {
  const items: React.ReactElement[] = []
  let run: React.ReactNode[] = []
  const flush = () => {
    const parts = run
    run = []
    if (parts.every((part) => typeof part === "string" && part.trim() === "")) return
    items.push(
      <Fact key={`run-${items.length}`}>
        <span className="inline-flex max-w-full min-w-0 flex-wrap items-center gap-x-2 align-top">
          {parts}
        </span>
      </Fact>,
    )
  }
  for (const child of flatten(children)) {
    if (isValidElement(child) && child.type === FactDot) {
      flush()
    } else if (isValidElement(child) && child.type === Fact) {
      flush()
      items.push(child as React.ReactElement)
    } else {
      run.push(child)
    }
  }
  flush()
  return items.map((item, index) =>
    cloneElement(item as React.ReactElement<{ lead?: boolean }>, {
      key: item.key ?? `fact-${index}`,
      lead: index === 0,
    }),
  )
}
