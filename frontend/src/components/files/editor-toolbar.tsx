"use client"

import { useRef } from "react"
import {
  ArrowLeftRight,
  CodeWrap,
  CornerUpLeft,
  CornerUpRight,
  External,
  Fullscreen,
  FullscreenClose,
  Location,
  MagnifyingGlass,
  MoreHorizontal,
  RotateCounterClockwise,
  Sparkles,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import type { EditorCommands } from "@/components/code-editor"
import { IconAction } from "@/components/icon-action"
import { StripGroup, STRIP_SEGMENT } from "@/components/files/strip"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

const FONT = { min: 10, max: 22 }
const INDENTS = ["2", "4", "8"]

/** A segment that is on or off: the same ghost, held down while it is on. */
const PRESSED = "aria-pressed:bg-accent aria-pressed:text-accent-foreground"

/**
 * The editor's commands, grouped the way the Files strip groups its own.
 *
 * They were a dozen equal grey controls in a row — two words, six bare
 * glyphs, A− and A+, two selects and another word — so nothing said which
 * belonged together, and on a phone the row wrapped to three above five lines
 * of code. Now each idea is one edge (`StripGroup`): finding, the way back
 * and forward through your own edits, how the text is drawn, and what you
 * have changed. What is set once and left — the text size, the indent — and
 * the palette of everything else are words in the menu at the end.
 *
 * Below `lg` the strip is one row: the language, find, undo and redo, and the
 * menu, which then also holds the groups there was no room to draw. A control
 * that only applies sometimes (review, discard) is drawn disabled rather than
 * arriving, so typing the first character does not move the row.
 */
export function EditorToolbar({
  language,
  languages,
  onLanguage,
  commands,
  canEdit,
  dirty,
  wrap,
  onWrap,
  minimap,
  onMinimap,
  fontSize,
  onFontSize,
  indent,
  onIndent,
  onFormat,
  reviewing,
  onReview,
  onRevert,
  fullscreen,
  onFullscreen,
  onOpenFull,
}: {
  language: string | undefined
  languages: string[]
  onLanguage: (language: string) => void
  /** The editor's own actions, once it has mounted. */
  commands: EditorCommands | undefined
  canEdit: boolean
  dirty: boolean
  wrap: boolean
  onWrap: (wrap: boolean) => void
  minimap: boolean
  onMinimap: (minimap: boolean) => void
  fontSize: number
  onFontSize: (size: number) => void
  indent: string
  onIndent: (indent: string) => void
  onFormat: () => void
  reviewing: boolean
  onReview: (reviewing: boolean) => void
  onRevert: () => void
  /** A sheet can take the whole window; a page already has it. */
  fullscreen?: boolean
  onFullscreen?: (fullscreen: boolean) => void
  /** A sheet can hand its draft to the page. */
  onOpenFull?: () => void
}) {
  // A command that puts the caret somewhere (the find box, the palette) runs
  // once the menu has closed. Run from the item, the menu's own return of
  // focus to its button lands afterwards and shuts what was just opened.
  const afterClose = useRef<(() => void) | null>(null)
  const replace = () => commands?.run("editor.action.startFindReplaceAction")

  return (
    <div data-slot="editor-toolbar" className="flex min-w-0 flex-1 items-center gap-2 lg:flex-wrap">
      <Select value={language} onValueChange={onLanguage}>
        <SelectTrigger
          size="sm"
          aria-label="File language"
          // One height with the faces beside it: a select alone grows to a
          // thumb's 40px on a phone, and the row is 32px like the Files strip.
          className="h-8 w-32 min-w-0 shrink text-body max-lg:w-36 max-sm:h-8 max-sm:text-body"
        >
          <SelectValue />
        </SelectTrigger>
        <SelectContent className="max-h-72">
          {languages.map((lang) => (
            <SelectItem key={lang} value={lang} className="text-xs">
              {lang}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>

      <StripGroup aria-label="Find in this file">
        <Button
          size="sm"
          variant="ghost"
          aria-label="Find"
          disabled={!commands}
          className="h-full rounded-none px-2.5 focus-ring-inset max-lg:w-8 max-lg:px-0"
          onClick={() => commands?.run("actions.find")}
        >
          <MagnifyingGlass className="size-3.5" />
          <span className="max-lg:hidden">Find</span>
        </Button>
        {canEdit && (
          <Button
            size="sm"
            variant="ghost"
            disabled={!commands}
            className="h-full rounded-none px-2.5 focus-ring-inset max-lg:hidden"
            onClick={replace}
          >
            Replace
          </Button>
        )}
      </StripGroup>

      <StripGroup aria-label="History">
        <IconAction
          label="Undo"
          className={STRIP_SEGMENT}
          disabled={!commands || !canEdit}
          onClick={() => commands?.undo()}
        >
          <CornerUpLeft />
        </IconAction>
        <IconAction
          label="Redo"
          className={STRIP_SEGMENT}
          disabled={!commands || !canEdit}
          onClick={() => commands?.redo()}
        >
          <CornerUpRight />
        </IconAction>
      </StripGroup>

      <StripGroup aria-label="How the text is drawn" className="max-lg:hidden">
        <IconAction
          label="Wrap long lines"
          aria-pressed={wrap}
          className={cn(STRIP_SEGMENT, PRESSED)}
          onClick={() => onWrap(!wrap)}
        >
          <CodeWrap />
        </IconAction>
        <IconAction
          label="Minimap"
          aria-pressed={minimap}
          className={cn(STRIP_SEGMENT, PRESSED)}
          onClick={() => onMinimap(!minimap)}
        >
          <Location />
        </IconAction>
      </StripGroup>

      {canEdit && (
        <StripGroup aria-label="Your changes" className="max-lg:hidden">
          <IconAction
            label="Format this document"
            className={STRIP_SEGMENT}
            disabled={!commands}
            onClick={onFormat}
          >
            <Sparkles />
          </IconAction>
          <IconAction
            label={reviewing ? "Back to editing" : "Review the changes before saving"}
            aria-pressed={reviewing}
            className={cn(STRIP_SEGMENT, PRESSED)}
            disabled={!dirty}
            onClick={() => onReview(!reviewing)}
          >
            <ArrowLeftRight />
          </IconAction>
          <IconAction
            label="Discard changes and reload from disk"
            className={STRIP_SEGMENT}
            disabled={!dirty}
            onClick={onRevert}
          >
            <RotateCounterClockwise />
          </IconAction>
        </StripGroup>
      )}

      {onFullscreen && (
        <IconAction
          variant="outline"
          label={fullscreen ? "Leave full screen" : "Full screen"}
          aria-pressed={fullscreen}
          // Below `lg` the sheet is already the width of the window.
          className="size-8 max-lg:hidden"
          onClick={() => onFullscreen(!fullscreen)}
        >
          {fullscreen ? <FullscreenClose /> : <Fullscreen />}
        </IconAction>
      )}

      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button size="icon-sm" variant="outline" aria-label="More editor commands">
            <MoreHorizontal className="size-3.5" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align="end"
          className="w-56"
          onCloseAutoFocus={(event) => {
            const run = afterClose.current
            afterClose.current = null
            if (!run) return
            event.preventDefault()
            run()
          }}
        >
          {/* What the strip draws from `lg` and has no room for below it. */}
          <div className="lg:hidden">
            {canEdit && (
              <DropdownMenuItem
                disabled={!commands}
                onSelect={() => {
                  afterClose.current = replace
                }}
              >
                Replace
              </DropdownMenuItem>
            )}
            <DropdownMenuCheckboxItem checked={wrap} onCheckedChange={onWrap}>
              Wrap long lines
            </DropdownMenuCheckboxItem>
            <DropdownMenuCheckboxItem checked={minimap} onCheckedChange={onMinimap}>
              Minimap
            </DropdownMenuCheckboxItem>
            {canEdit && (
              <>
                <DropdownMenuSeparator />
                <DropdownMenuItem disabled={!commands} onSelect={onFormat}>
                  Format this document
                </DropdownMenuItem>
                <DropdownMenuCheckboxItem
                  checked={reviewing}
                  disabled={!dirty}
                  onCheckedChange={onReview}
                >
                  Review the changes
                </DropdownMenuCheckboxItem>
                <DropdownMenuItem disabled={!dirty} onSelect={onRevert}>
                  Discard changes
                </DropdownMenuItem>
              </>
            )}
            <DropdownMenuSeparator />
          </div>
          {/* The size is pressed more than once, so the menu stays for it. */}
          <DropdownMenuItem
            disabled={fontSize <= FONT.min}
            onSelect={(event) => {
              event.preventDefault()
              onFontSize(Math.max(FONT.min, fontSize - 1))
            }}
          >
            Smaller text
            <span className="numeric ml-auto text-hint text-muted-foreground">{fontSize}px</span>
          </DropdownMenuItem>
          <DropdownMenuItem
            disabled={fontSize >= FONT.max}
            onSelect={(event) => {
              event.preventDefault()
              onFontSize(Math.min(FONT.max, fontSize + 1))
            }}
          >
            Larger text
          </DropdownMenuItem>
          <DropdownMenuSub>
            <DropdownMenuSubTrigger>
              Indentation
              <span className="numeric ml-auto text-hint text-muted-foreground">
                {indent} spaces
              </span>
            </DropdownMenuSubTrigger>
            <DropdownMenuSubContent className="w-36">
              <DropdownMenuRadioGroup value={indent} onValueChange={onIndent}>
                {INDENTS.map((size) => (
                  <DropdownMenuRadioItem key={size} value={size}>
                    {size} spaces
                  </DropdownMenuRadioItem>
                ))}
              </DropdownMenuRadioGroup>
            </DropdownMenuSubContent>
          </DropdownMenuSub>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            disabled={!commands}
            onSelect={() => {
              afterClose.current = () => commands?.run("editor.action.quickCommand")
            }}
          >
            Commands
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      {onOpenFull && (
        <Button
          size="sm"
          variant="outline"
          aria-label="Open full editor"
          className="ml-auto max-lg:w-8 max-lg:px-0"
          onClick={onOpenFull}
        >
          <External className="size-3.5" />
          <span className="max-lg:hidden">Open full editor</span>
        </Button>
      )}
    </div>
  )
}
