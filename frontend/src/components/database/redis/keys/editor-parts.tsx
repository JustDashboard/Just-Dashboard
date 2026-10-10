"use client"

import { useEffect, useRef, useState } from "react"
import { Cross } from "@/components/icons"
import { cn } from "@/lib/utils"
import { SearchInput } from "@/components/page"
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
  const [draft, setDraft] = useState("")
  const [applied, setApplied] = useState("")
  const apply = useRef(onApply)
  useEffect(() => {
    apply.current = onApply
  })
  const send = (text: string) => {
    setApplied(text)
    apply.current(text)
  }
  useEffect(() => {
    const typed = draft.trim()
    if (typed === applied) return
    const timer = setTimeout(() => {
      setApplied(typed)
      apply.current(typed)
    }, 400)
    return () => clearTimeout(timer)
  }, [draft, applied])
  return (
    <SearchInput
      dense
      aria-label={label}
      placeholder={placeholder}
      value={draft}
      spellCheck={false}
      autoComplete="off"
      // Room for the clear control only once there is something to clear: the
      // placeholder has the whole box until then.
      className={cn("font-mono", draft && "pr-7")}
      containerClassName="min-w-40 flex-1 sm:w-auto sm:max-w-64"
      onChange={(event) => setDraft(event.target.value)}
      onKeyDown={(event) => {
        if (event.key === "Enter") send(draft.trim())
        if (event.key === "Escape" && draft) {
          event.stopPropagation()
          setDraft("")
          send("")
        }
      }}
      trailing={
        draft && (
          <button
            type="button"
            aria-label="Clear the filter"
            onClick={() => {
              setDraft("")
              send("")
            }}
            className="flex size-5 items-center justify-center rounded-sm text-muted-foreground focus-ring hover:text-foreground"
          >
            <Cross className="size-3" />
          </button>
        )
      }
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
