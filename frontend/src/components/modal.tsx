"use client"

import { cn } from "@/lib/utils"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"

const WIDTHS = {
  sm: "sm:max-w-md",
  md: "sm:max-w-lg",
  lg: "sm:max-w-2xl",
  xl: "sm:max-w-4xl",
} as const

/**
 * The centred task surface: create a container, rename a file, import a
 * certificate, edit a row.
 *
 * Deliberately the same anatomy as `SidePanel` — a title on a tinted strip with
 * a hairline under it, a body that is the only part that scrolls, and a footer
 * strip holding the buttons. The two are the same idea pointed in two
 * directions, and until this existed they looked nothing alike: a sheet had a
 * header strip and a dialog had `p-6` and a gap, so the same task rendered two
 * ways depending on how much room it needed.
 *
 * No icon plot in front of the title, for the reason written at `PanelHeader`:
 * a dialog that says "Remove volume" does not need a picture of a disk to say
 * it, and the red one the typed-confirmation dialog used to wear was never the
 * thing that stopped anybody — the phrase they have to type is.
 *
 * The footer being a strip rather than a row of buttons floating in the body is
 * what makes a long form usable: the raw `DialogContent` scrolls as one box, so
 * a form taller than the window pushed its own Save button off the bottom edge.
 * Here the body scrolls inside a frame whose header and footer stay put.
 */
export function Modal({
  open,
  onOpenChange,
  title,
  description,
  size = "md",
  footer,
  bodyClassName,
  className,
  children,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: React.ReactNode
  /** Read to a screen reader, never drawn. See the note at the call site. */
  description?: React.ReactNode
  size?: keyof typeof WIDTHS
  footer?: React.ReactNode
  bodyClassName?: string
  className?: string
  children: React.ReactNode
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className={cn(
          "flex max-h-[calc(100svh-4rem)] flex-col gap-0 overflow-hidden rounded-xl p-0",
          WIDTHS[size],
          className,
        )}
      >
        <DialogHeader className="shrink-0 gap-1 border-b border-hairline bg-surface-header px-4 py-3 pr-12 text-left">
          <div className="flex min-w-0 items-start">
            <div className="min-w-0 flex-1">
              <DialogTitle className="flex min-w-0 flex-wrap items-center gap-2 text-title leading-tight font-semibold">
                {title}
              </DialogTitle>
              {/* Not drawn. A dialog states its job in the title and the
                  controls below it; a sentence in between is a caption for a
                  heading the reader has already read. Radix still wants an
                  accessible description, and a screen reader is the one
                  audience that genuinely cannot see the rest of the box. */}
              {description && (
                <DialogDescription className="sr-only">{description}</DialogDescription>
              )}
            </div>
          </div>
        </DialogHeader>

        <div className={cn("min-h-0 flex-1 overflow-y-auto", bodyClassName ?? "p-4")}>
          {children}
        </div>

        {footer && (
          <div className="flex shrink-0 flex-wrap items-center justify-end gap-2 border-t border-hairline bg-surface-header px-4 py-3">
            {footer}
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}

/**
 * The other centred surface: a search overlay whose own input *is* its header —
 * the command palette, the file quick-open.
 *
 * It has no title strip because the box you type into is the title, and no
 * close button because Escape and a click outside are the only ways anyone
 * dismisses one. Both call sites had assembled this separately and drifted
 * apart on width, padding and — in the palette's case — on where the
 * accessible title lived, which was outside the content element and so was
 * never announced.
 */
export function PaletteModal({
  open,
  onOpenChange,
  label,
  description,
  className,
  children,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** The accessible name. Never drawn — the input carries the visible prompt. */
  label: string
  description: string
  className?: string
  children: React.ReactNode
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className={cn("gap-0 overflow-hidden rounded-xl p-0 sm:max-w-2xl", className)}
        showCloseButton={false}
      >
        <DialogHeader className="sr-only">
          <DialogTitle>{label}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        {children}
      </DialogContent>
    </Dialog>
  )
}
