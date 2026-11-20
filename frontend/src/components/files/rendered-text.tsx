"use client"

import { useMemo } from "react"
import { CodeBracket, Eye } from "@/components/icons"
import { bytes, plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { FilePreview } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { Markdown, type MarkdownFiles } from "@/components/markdown"
import { Well } from "@/components/panel"
import { IconAction } from "@/components/icon-action"
import { baseOf, mediaKind, parentOf, rawUrl } from "@/components/files/media"
import { TextHead } from "@/components/files/preview-bits"
import { HighlightedCode } from "@/components/files/code-highlight"
import {
  indentJson,
  parseDelimited,
  resolveRef,
  textViewOf,
  type Delimited,
  type TextView,
} from "@/components/files/text-view"

/**
 * A text file drawn as what it is: Markdown rendered, a CSV or TSV as a
 * table, JSON that arrived on one line indented. The inspector and the viewer
 * share it, and both keep a way back to the numbered lines.
 *
 * It reads only the head the preview already carries. A head that was cut
 * short is drawn as far as it goes and says so underneath; it is never offered
 * as if it were the whole file.
 *
 * Markdown here is a file on the server this dashboard controls, drawn on the
 * dashboard's own origin, so it goes through the same reader as a pull
 * request's description (`lib/markdown.ts`: never HTML, no `javascript:`), with
 * one difference: a reference with no scheme is another file. A link to one
 * opens it in Files, a picture beside it is drawn through `GET /files/raw` and
 * its allowlist, and a picture from another site is named and not fetched.
 */

/** The view this preview gets, or nothing for lines that are only lines. */
export function renderedViewOf(preview: FilePreview | undefined): TextView | null {
  if (!preview || preview.kind !== "text") return null
  // A preview without its name (an answer of another shape) is lines, never a throw.
  return textViewOf(preview.name ?? "", preview.text ?? "")
}

/** Rendered or source, remembered per browser: whoever reads Markdown as source reads it so everywhere. */
export function useRenderedChoice() {
  return useViewState<boolean>("files.rendered", true)
}

/** The press that switches between the rendered file and its lines. */
export function SourceToggle({
  rendered,
  onChange,
  className,
}: {
  rendered: boolean
  onChange: (rendered: boolean) => void
  className?: string
}) {
  return (
    <IconAction
      label={rendered ? "Show the source" : "Show it rendered"}
      aria-pressed={!rendered}
      className={cn("size-6", className)}
      onClick={() => onChange(!rendered)}
    >
      {rendered ? <CodeBracket /> : <Eye />}
    </IconAction>
  )
}

/** What a section about this text is called: its rows, or its lines, and whether it is all of it. */
export function renderedTitle(preview: FilePreview, view: TextView | null): string {
  if (view === "csv" || view === "tsv") {
    const table = parseDelimited(
      preview.text ?? "",
      view === "tsv" ? "\t" : ",",
      !!preview.truncated,
    )
    const rows = plural(table.rows.length, "row")
    return preview.truncated || table.cut ? `First ${rows}` : rows
  }
  const lines = plural(preview.lines ?? 0, "line")
  return preview.truncated ? `First ${lines}` : lines
}

export function RenderedText({
  preview,
  view,
  onOpenPath,
  stage,
  frame,
}: {
  preview: FilePreview
  view: TextView
  /** A link to another file on this server: a folder to enter, or a file to show. */
  onOpenPath?: (path: string, folder: boolean) => void
  /** The viewer's full stage rather than the inspector's column. */
  stage?: boolean
  /** The height the scrolling part keeps to: a cap in a column, the rest of a stage. */
  frame?: string
}) {
  const text = preview.text ?? ""
  const truncated = !!preview.truncated
  const table = useMemo(
    () =>
      view === "csv" || view === "tsv"
        ? parseDelimited(text, view === "tsv" ? "\t" : ",", truncated)
        : null,
    [text, view, truncated],
  )
  const shown = table ? plural(table.rows.length, "row") : plural(preview.lines ?? 0, "line")
  return (
    <div
      data-rendered={view}
      className={cn("flex min-h-0 min-w-0 flex-col gap-2", stage && "flex-1")}
    >
      {view === "markdown" && (
        <div className={cn("min-h-0 overflow-auto", frame)}>
          <MarkdownFile path={preview.path} text={text} onOpenPath={onOpenPath} stage={stage} />
        </div>
      )}
      {table && <DelimitedTable table={table} className={frame} />}
      {view === "json" && (
        <Well className={cn("min-h-0 p-0 whitespace-pre", frame)}>
          <TextHead text={indentJson(text)} />
        </Well>
      )}
      {(truncated || table?.cut) && (
        <p
          data-truncated
          className={cn(
            "shrink-0 text-hint text-muted-foreground",
            stage && "mx-auto w-full max-w-3xl",
          )}
        >
          Only the first {shown} of {bytes(preview.size)} are shown. Open it in the editor or
          download it for the rest.
        </p>
      )}
    </div>
  )
}

function MarkdownFile({
  path,
  text,
  onOpenPath,
  stage,
}: {
  path: string
  text: string
  onOpenPath?: (path: string, folder: boolean) => void
  stage?: boolean
}) {
  const files = useMemo<MarkdownFiles>(
    () => ({
      href: (ref) => {
        const target = resolveRef(path, ref)
        if (!target) return undefined
        const query = target.folder
          ? new URLSearchParams({ path: target.path })
          : new URLSearchParams({ path: parentOf(target.path), entry: target.path })
        return `/files?${query}`
      },
      open: (ref) => {
        const target = resolveRef(path, ref)
        if (target) onOpenPath?.(target.path, target.folder)
      },
      image: (ref) => {
        const target = resolveRef(path, ref)
        if (!target || target.folder || mediaKind(baseOf(target.path)) !== "image") return undefined
        return rawUrl(target.path)
      },
    }),
    [path, onOpenPath],
  )
  return (
    <Markdown
      source={text}
      files={files}
      code={(block) => <HighlightedCode lang={block.lang} text={block.v} />}
      className={cn(stage && "mx-auto w-full max-w-3xl")}
    />
  )
}

/**
 * A CSV or TSV as the table it is: its first row as the header, each column
 * as wide as what it holds (to a limit, past which a cell is cut and keeps the
 * rest in its tooltip), numbers set to the right, one line per row.
 */
function DelimitedTable({ table, className }: { table: Delimited; className?: string }) {
  if (table.header.length === 0) {
    return <p className="text-hint text-muted-foreground">Empty</p>
  }
  return (
    <div
      data-file-table
      className={cn(
        "min-h-0 w-fit max-w-full overflow-auto rounded-lg border border-hairline",
        className,
      )}
    >
      <table className="text-hint">
        <thead className="sticky top-0 bg-surface-header">
          <tr>
            {table.header.map((cell, c) => (
              <th
                key={c}
                scope="col"
                title={cell}
                className={cn(
                  "max-w-72 truncate border-b border-hairline px-2.5 py-1.5 font-medium whitespace-nowrap",
                  table.numeric[c] ? "text-right" : "text-left",
                )}
              >
                {cell}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y divide-hairline">
          {table.rows.map((row, r) => (
            <tr key={r}>
              {row.map((cell, c) => (
                <td
                  key={c}
                  title={cell.length > 40 || cell.includes("\n") ? cell : undefined}
                  className={cn(
                    "max-w-72 truncate px-2.5 py-1 whitespace-nowrap",
                    table.numeric[c] ? "numeric text-right" : "text-left",
                  )}
                >
                  {cell.replace(/\s*\n\s*/g, " ")}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
