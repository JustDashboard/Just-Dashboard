"use client"

import { Check, Copy, Download } from "@/components/icons"
import { cn } from "@/lib/utils"
import { useCopy } from "@/hooks/use-copy"
import { CodeEditor } from "@/components/code-editor"
import { Pane, PaneHeader } from "@/components/panel"
import { Button } from "@/components/ui/button"

/** Hands the reader a text as a file of the given name. */
export function downloadText(text: string, filename: string) {
  const url = URL.createObjectURL(new Blob([text], { type: "text/plain" }))
  const link = document.createElement("a")
  link.href = url
  link.download = filename
  link.click()
  URL.revokeObjectURL(url)
}

/**
 * Code to read and take away: a generated schema, a table's definition, a
 * dump's header.
 *
 * It is the editor the query console uses, read-only, in a pane that names
 * what it holds and carries the two things done with code nobody is editing —
 * copy it, or save it as the file it is. Monaco comes from this origin and
 * highlights by `language`; the pane is sized by the caller, since a
 * definition wants a few lines and a generated schema wants the window.
 */
export function CodeView({
  code,
  language,
  filename,
  label,
  actions,
  className,
}: {
  code: string
  /** A Monaco language id: sql, pgsql, typescript, python, json… */
  language?: string
  /** What the code is saved as. Offers Download, and names the pane where no `label` does. */
  filename?: string
  /** What the code is, when it is not a file. */
  label?: React.ReactNode
  /** Controls of the caller's own, before Copy. */
  actions?: React.ReactNode
  /** The pane's size: a height, or `min-h-0 flex-1` inside a workbench. */
  className?: string
}) {
  const { copy, copied } = useCopy()
  return (
    // Framed: an editor is a working region with its own scroll (§2).
    <Pane data-slot="code-view" className={cn("h-72", className)}>
      <PaneHeader className="gap-2">
        <span className={cn("min-w-0 flex-1 truncate text-xs", !label && "font-mono")}>
          {label ?? filename}
        </span>
        {actions}
        <Button size="xs" variant="ghost" onClick={() => void copy(code)}>
          {copied ? <Check /> : <Copy />}
          Copy
        </Button>
        {filename && (
          <Button size="xs" variant="ghost" onClick={() => downloadText(code, filename)}>
            <Download />
            Download
          </Button>
        )}
      </PaneHeader>
      <CodeEditor className="min-h-0 flex-1" language={language} value={code} readOnly />
    </Pane>
  )
}
