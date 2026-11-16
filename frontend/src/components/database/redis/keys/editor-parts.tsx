"use client"

import { useState } from "react"
import { cn } from "@/lib/utils"
import { SearchInput } from "@/components/search-input"
import { globEscape, hasGlob } from "@/components/database/redis/bytes"

/** The strip between a key's head and its value: the editor's filter and its commands. */
export function EditorStrip({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="redis-editor-strip"
      className={cn(
        "flex min-h-10 shrink-0 flex-wrap items-center gap-x-2 gap-y-1.5 border-b border-hairline px-3 py-1.5",
        className,
      )}
      {...props}
    />
  )
}

/**
 * What the server matches members against, from what was typed: a glob is
 * sent as written, and plain text finds the members that contain it.
 */
export function matchOf(text: string): string | undefined {
  const typed = text.trim()
  if (!typed) return undefined
  return hasGlob(typed) ? typed : `*${globEscape(typed)}*`
}

/**
 * The filter over a key's members. Applied on Enter and when the reader
 * pauses: each change is a scan of the key on the server.
 */
export function FilterBox({
  label,
  placeholder,
  onApply,
}: {
  label: string
  placeholder: string
  onApply: (text: string) => void
}) {
  // The value in force; the box keeps what is being typed and applies it
  // after a pause, at once on Enter, and empty on Escape or the clear button.
  const [applied, setApplied] = useState("")
  return (
    <SearchInput
      dense
      aria-label={label}
      placeholder={placeholder}
      value={applied}
      spellCheck={false}
      autoComplete="off"
      stopEscape
      clearLabel="Clear the filter"
      className="font-mono"
      containerClassName="min-w-40 flex-1 sm:w-auto sm:max-w-64"
      onApply={(text) => {
        const typed = text.trim()
        setApplied(typed)
        onApply(typed)
      }}
    />
  )
}

/**
 * Hands the reader what a route answers as a file. The browser writes the
 * answer straight to disk, so a value of half a gigabyte is never held in
 * the page.
 */
export function saveFrom(url: string, filename = "redis-value.bin") {
  const link = document.createElement("a")
  link.href = url
  link.download = filename
  link.click()
}
