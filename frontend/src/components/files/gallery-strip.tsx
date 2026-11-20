"use client"

import { useEffect, useRef } from "react"
import { cn } from "@/lib/utils"
import type { FileEntry } from "@/lib/types"
import { Thumbnail } from "@/components/files/thumbnail"
import { stripWindow } from "@/components/files/gallery"

/**
 * The viewer's strip of the folder's other files, to jump by: each drawn as
 * the listing draws it (a picture as its thumbnail), the one on the stage
 * ringed. It holds a window of the folder round the current item, so a folder
 * of thousands is a strip of eighty, and keeps the current one in view.
 */
export function GalleryStrip({
  items,
  index,
  onPick,
}: {
  items: FileEntry[]
  index: number
  onPick: (index: number) => void
}) {
  const strip = useRef<HTMLDivElement>(null)
  const [start, end] = stripWindow(items.length, index)

  useEffect(() => {
    const current = strip.current?.querySelector<HTMLElement>("[aria-current='true']")
    current?.scrollIntoView({ block: "nearest", inline: "center" })
  }, [index])

  return (
    <div
      ref={strip}
      data-gallery-strip
      role="toolbar"
      aria-label="Files in this folder"
      className="flex shrink-0 [scrollbar-width:thin] gap-1.5 overflow-x-auto border-t border-hairline px-3 py-2"
    >
      {items.slice(start, end).map((item, offset) => {
        const at = start + offset
        const current = at === index
        return (
          <button
            key={item.path}
            type="button"
            aria-label={`${item.name}, ${at + 1} of ${items.length}`}
            aria-current={current ? "true" : undefined}
            title={item.name}
            onClick={() => onPick(at)}
            className={cn(
              "shrink-0 rounded-md p-0.5 focus-ring transition-opacity",
              current ? "ring-2 ring-brand" : "opacity-70 hover:opacity-100",
            )}
          >
            <Thumbnail entry={item} size="strip" />
          </button>
        )
      })}
    </div>
  )
}
