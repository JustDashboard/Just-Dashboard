"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import {
  Archive,
  Database,
  FolderOpen,
  Rocket,
  TerminalWindow,
  type Icon,
} from "@/components/icons"
import { post } from "@/lib/api"
import type { Board } from "@/lib/boards"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { ProductCard } from "@/components/choice-card"
import { Section } from "@/components/page"

/** One way to start something, and what it would add to. */
export type Shortcut = {
  key: string
  label: string
  /** What there is already, as a count where one is known: data, not a caption. */
  detail?: string
  product?: string
  fallback?: Icon
} & ({ href: string } | { create: "drawing" })

/**
 * The things a visit most often starts — a shell, a project, a drawing — as
 * one run of cards, each carrying what is already there. It is the only block
 * on the page made of choices, so it is the only one with the lit edge of a
 * thing you take (§16); every other block reads.
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
      {/* Two to a row until the content is wide enough for four: four across
          beside the sidebar at a tablet's width cut every label to its first
          word, and one to a row on a phone made the run a screen tall. */}
      <div data-slot="shortcuts" className="grid min-w-0 grid-cols-2 gap-3 xl:grid-cols-4">
        {shortcuts.map((shortcut) => (
          <ProductCard
            key={shortcut.key}
            product={shortcut.product}
            fallback={shortcut.fallback}
            label={shortcut.label}
            detail={shortcut.detail}
            working={"create" in shortcut && making === shortcut.create}
            disabled={making !== undefined && !("create" in shortcut && making === shortcut.create)}
            onClick={() =>
              "create" in shortcut ? void create(shortcut.create) : router.push(shortcut.href)
            }
          />
        ))}
      </div>
    </Section>
  )
}

/** A count in a shortcut's line, or nothing while it is unknown. */
export function counted(n: number | undefined, singular: string, many?: string) {
  return n === undefined ? undefined : plural(n, singular, many)
}

export const SHORTCUT_GLYPH = {
  deploy: Rocket,
  database: Database,
  files: FolderOpen,
  computer: TerminalWindow,
  backup: Archive,
} satisfies Record<string, Icon>
