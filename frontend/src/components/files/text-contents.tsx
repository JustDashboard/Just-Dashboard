"use client"

import { ArrowUpRight } from "@/components/icons"
import { bytes, plural } from "@/lib/format"
import type { FilePreview } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { PaneSection, Well } from "@/components/panel"
import { TextHead } from "@/components/files/preview-bits"
import {
  RenderedText,
  SourceToggle,
  renderedTitle,
  renderedViewOf,
  useRenderedChoice,
} from "@/components/files/rendered-text"

/**
 * The inspector's "what is inside" for a text file that reads as something:
 * Markdown, a table, one-line JSON (`renderedViewOf` says which). Anything
 * else keeps the numbered head the inspector has always drawn.
 */
export function TextContents({
  preview,
  openLabel,
  onOpen,
  onOpenPath,
}: {
  preview: FilePreview
  openLabel: string
  onOpen: () => void
  onOpenPath?: (path: string, folder: boolean) => void
}) {
  const view = renderedViewOf(preview)
  const [rendered, setRendered] = useRenderedChoice()
  if (!view) return null
  return (
    <PaneSection
      title={rendered ? renderedTitle(preview, view) : renderedTitle(preview, null)}
      aside={
        <span className="flex items-center gap-1">
          <SourceToggle rendered={rendered} onChange={setRendered} />
          <Button size="xs" variant="ghost" onClick={onOpen}>
            {openLabel}
            <ArrowUpRight className="size-3" />
          </Button>
        </span>
      }
    >
      {rendered ? (
        <RenderedText preview={preview} view={view} onOpenPath={onOpenPath} frame="max-h-[28rem]" />
      ) : (
        <Well className="max-h-72 p-0 whitespace-pre">
          <TextHead text={preview.text ?? ""} />
        </Well>
      )}
    </PaneSection>
  )
}

/**
 * The viewer's stage for a text file: rendered where it reads as something,
 * with the switch to its lines and back in the line under it.
 */
export function TextStage({
  preview,
  onOpenPath,
}: {
  preview: FilePreview
  onOpenPath?: (path: string, folder: boolean) => void
}) {
  const view = renderedViewOf(preview)
  const [rendered, setRendered] = useRenderedChoice()
  const showRendered = view !== null && rendered
  return (
    <div className="flex min-h-0 flex-1 flex-col gap-2 p-3">
      {showRendered ? (
        <RenderedText preview={preview} view={view} onOpenPath={onOpenPath} stage frame="flex-1" />
      ) : (
        <Well className="min-h-0 flex-1 p-0 whitespace-pre">
          <TextHead text={preview.text ?? ""} />
        </Well>
      )}
      <div className="flex shrink-0 items-center gap-2 text-hint text-muted-foreground">
        {!showRendered && (
          <p>
            {preview.truncated
              ? `First ${plural(preview.lines ?? 0, "line")} of ${bytes(preview.size)}. Open it in the editor for the rest.`
              : plural(preview.lines ?? 0, "line")}
          </p>
        )}
        {view && <SourceToggle rendered={rendered} onChange={setRendered} className="ml-auto" />}
      </div>
    </div>
  )
}
