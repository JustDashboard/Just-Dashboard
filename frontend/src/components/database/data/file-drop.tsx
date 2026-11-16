"use client"

import { useRef } from "react"
import { CloudUpload } from "@/components/icons"
import { bytes } from "@/lib/format"
import { Button } from "@/components/ui/button"

/**
 * Where a database import takes its file: a dashed button saying "Choose a
 * file, or drop one here" with the formats it reads under it, then, once one
 * is chosen, the file's name and size and the way to choose another.
 *
 * The table editor's import had this and MongoDB's had the browser's own
 * file input — the system's "Choose File" button, with no drop. Both read the
 * same kinds of file, so both pick it the same way. `meta` stands after the
 * size: the format the file was read as.
 */
export function FileDrop({
  file,
  accept,
  formats,
  onFile,
  disabled,
  meta,
}: {
  file: File | null
  /** The input's `accept`: extensions and media types. */
  accept: string
  /** The formats in words, under the invitation: "CSV · JSON · NDJSON". */
  formats: string
  onFile: (file: File) => void
  disabled?: boolean
  meta?: React.ReactNode
}) {
  const picker = useRef<HTMLInputElement>(null)
  return (
    <>
      <input
        ref={picker}
        type="file"
        hidden
        accept={accept}
        disabled={disabled}
        onChange={(event) => {
          const next = event.currentTarget.files?.[0] ?? null
          // Cleared, so choosing the same file again is still a change.
          event.currentTarget.value = ""
          if (next) onFile(next)
        }}
      />
      {!file ? (
        <button
          type="button"
          disabled={disabled}
          className="flex w-full flex-col items-center gap-2 rounded-lg border border-dashed px-6 py-10 text-center focus-ring transition-colors hover:bg-row-hover disabled:pointer-events-none disabled:opacity-60"
          onClick={() => picker.current?.click()}
          onDragOver={(event) => event.preventDefault()}
          onDrop={(event) => {
            event.preventDefault()
            const dropped = event.dataTransfer.files[0]
            if (dropped && !disabled) onFile(dropped)
          }}
        >
          <CloudUpload className="size-5 text-muted-foreground" />
          <span className="text-body font-medium">Choose a file, or drop one here</span>
          <span className="text-hint text-muted-foreground">{formats}</span>
        </button>
      ) : (
        <div className="flex min-w-0 items-center gap-2">
          <span className="min-w-0 truncate font-mono text-body">{file.name}</span>
          <span className="numeric shrink-0 text-hint text-muted-foreground">
            {bytes(file.size)}
          </span>
          {meta}
          <Button
            size="xs"
            variant="ghost"
            className="ml-auto"
            disabled={disabled}
            onClick={() => picker.current?.click()}
          >
            Choose another
          </Button>
        </div>
      )}
    </>
  )
}
