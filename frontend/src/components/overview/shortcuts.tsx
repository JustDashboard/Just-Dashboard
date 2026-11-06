"use client"

import { useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { ArrowRight, FolderOpen, Plus, Rocket, type Icon } from "@/components/icons"
import { post } from "@/lib/api"
import type { Board } from "@/lib/boards"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { Section } from "@/components/page"
import { ProductLogo } from "@/components/product-logo"
import { BorderBeam } from "@/components/ui/border-beam"
import { SpotlightBorder } from "@/components/ui/spotlight-border"
import { cn } from "@/lib/utils"

/** One way to start something, and what it would add to. */
export type Shortcut = {
  key: string
  label: string
  /** What there is already, as a count where one is known: data, not a caption. */
  detail?: string
  product?: string
  fallback?: Icon
  /** Whether pressing it makes something new or goes to what is there. */
  verb: "new" | "open"
} & ({ href: string } | { create: "drawing" })

/**
 * The four things a visit most often starts, as one run of cards, each carrying
 * what is already there. It is the only block on the page made of choices, so
 * it is the only one with the lit edge of a thing you take (§16); every other
 * block reads.
 *
 * New drawing makes the thing in place rather than sending the reader to a
 * list to press New there: an empty drawing is what the list's own New makes,
 * and the editor is where it gets its name.
 */
export function Shortcuts({ shortcuts }: { shortcuts: Shortcut[] }) {
  const router = useRouter()
  const [making, setMaking] = useState<string>()

  if (shortcuts.length === 0) return null

  async function create(kind: "drawing") {
    if (making) return
    setMaking(kind)
    try {
      const drawing = await post<Board>("/boards/", { name: "Untitled drawing" })
      router.push(`/excalidraw/${drawing.id}`)
    } catch (cause) {
      notify.error("Could not create the drawing", cause)
      setMaking(undefined)
    }
  }

  return (
    <Section title="Shortcuts">
      {/* Measured against the content's own width, not the window's: the sidebar
          takes a different share at every breakpoint, and four across is right
          exactly when each card still holds its label on one line. */}
      <div className="@container min-w-0">
        <div data-slot="shortcuts" className="grid grid-cols-2 gap-2 @2xl:grid-cols-4 @2xl:gap-3">
          {shortcuts.map((shortcut) => (
            <ShortcutCard
              key={shortcut.key}
              shortcut={shortcut}
              working={"create" in shortcut && making === shortcut.create}
              // A drawing being made is about to change the page, so nothing
              // else on this run can be taken until it has.
              idle={making !== undefined && !("create" in shortcut && making === shortcut.create)}
              onCreate={create}
            />
          ))}
        </div>
      </div>
    </Section>
  )
}

/**
 * Two short lines beside a mark, in a phone's half width and in a quarter of a
 * desktop's alike; only a wide run gives the mark and the label a step more.
 *
 * Not a `ProductCard`, which is a button you pick one of in a form. Three of
 * these go somewhere, so they are links the whole way across: a middle press or
 * a modified one opens a new tab, which a button pushing the route never could.
 * The glyph at the end says which of the two a card does, and gives way
 * wherever the card is narrow, because the label needs the width more.
 */
function ShortcutCard({
  shortcut,
  working,
  idle,
  onCreate,
}: {
  shortcut: Shortcut
  working: boolean
  idle: boolean
  onCreate: (kind: "drawing") => void
}) {
  const Verb = shortcut.verb === "new" ? Plus : ArrowRight
  const face = (
    <>
      <ProductLogo
        id={shortcut.product}
        size="sm"
        fallback={shortcut.fallback}
        // The glyphs at the terminal mark's own weight: muted, as a tile's
        // fallback is, they read as the two cards that had not loaded yet.
        className="@4xl:size-10 [&>img]:@4xl:size-5.5 [&>svg]:size-4.5 [&>svg]:text-foreground [&>svg]:@4xl:size-5"
      />
      <span className="flex min-w-0 flex-1 flex-col">
        <span className="truncate text-body font-medium @4xl:text-title">{shortcut.label}</span>
        {/* Held open while its count is read, so the card does not grow under
            the pointer when the answer arrives. */}
        <span className="truncate text-hint text-muted-foreground">{shortcut.detail ?? " "}</span>
      </span>
      <Verb
        aria-hidden
        className="hidden size-4 shrink-0 text-muted-foreground transition-colors group-hover/shortcut:text-foreground @md:block @2xl:hidden @4xl:block"
      />
      {working && (
        <span aria-hidden className="pointer-events-none absolute -inset-px rounded-xl">
          <BorderBeam size={60} duration={4} />
        </span>
      )}
    </>
  )
  const shape = cn(
    "group/shortcut flex w-full min-w-0 items-center gap-2.5 rounded-xl px-2.5 py-2.5 text-left focus-ring",
    "sm:gap-3 sm:px-3",
    idle && "pointer-events-none opacity-50",
  )
  return (
    <SpotlightBorder className="h-full">
      {"create" in shortcut ? (
        <button
          type="button"
          data-slot="shortcut"
          disabled={working}
          onClick={() => onCreate(shortcut.create)}
          className={shape}
        >
          {face}
        </button>
      ) : (
        <Link data-slot="shortcut" href={shortcut.href} className={shape}>
          {face}
        </Link>
      )}
    </SpotlightBorder>
  )
}

/** A count in a shortcut's line, or nothing while it is unknown. */
export function counted(n: number | undefined, singular: string, many?: string) {
  return n === undefined ? undefined : plural(n, singular, many)
}

export const SHORTCUT_GLYPH = {
  deploy: Rocket,
  files: FolderOpen,
} satisfies Record<string, Icon>
