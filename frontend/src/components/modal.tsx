"use client"

import { useRef } from "react"
import { Cross } from "@/components/icons"
import { cn } from "@/lib/utils"
import { useFocusReturn } from "@/hooks/use-focus-return"
import { FormFacts } from "@/components/form"
import { OperationProgress, type OperationReading } from "@/components/operation-progress"
import {
  Dialog,
  DialogBareContent,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogOverlay,
  DialogPortal,
  DialogTitle,
} from "@/components/ui/dialog"

/**
 * The overlays' size names, one vocabulary for `Modal` and `SidePanel`: the
 * prop is `size` on both, the steps are `sm`, `md`, `lg` and `xl` from the
 * narrowest, and a name says what the overlay holds rather than a width:
 *
 *   sm  a name, a sentence and a button: a rename, a confirmation
 *   md  a form of a few fields — the default
 *   lg  a form with a list, a table or a block of code in it
 *   xl  an editor or a table that wants the width
 *
 * Each surface turns the name into its own width. A sheet is a column the
 * height of the window and a dialog a box in the middle of it, so a sheet's
 * step is the wider of the two (`side-panel.tsx`). `full`, the whole window,
 * is the dialog's one named exception, for a viewer.
 */
export type OverlaySize = "sm" | "md" | "lg" | "xl"

const WIDTHS = {
  sm: "sm:max-w-md",
  md: "sm:max-w-lg",
  lg: "sm:max-w-2xl",
  xl: "sm:max-w-4xl",
  // The whole viewport, for the one task that is looking at a thing rather
  // than filling in a form: the file viewer. Still a dialog — it owns the
  // keyboard and Escape closes it — with its title strip and footer intact,
  // so a picture opens the same way a form does and closes the same way too.
  full: "h-svh max-h-svh w-screen max-w-none rounded-none border-0 sm:max-w-none",
} as const satisfies Record<OverlaySize | "full", string>

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
 * it; the action and its subject are named in the confirmation body.
 *
 * The footer being a strip rather than a row of buttons floating in the body is
 * what makes a long form usable: the raw `DialogContent` scrolls as one box, so
 * a form taller than the window pushed its own Save button off the bottom edge.
 * Here the body scrolls inside a frame whose header and footer stay put.
 *
 * What goes in the body — the subject first, kinds as cards, sections under
 * hairlines, the command last and on the right — is written once, at
 * `SidePanel`, and holds here unchanged.
 */
export function Modal({
  open,
  onOpenChange,
  title,
  description,
  size = "md",
  variant = "form",
  role,
  actions,
  footer,
  progress,
  initialFocus = "first",
  bodyClassName,
  className,
  onCloseAutoFocus,
  children,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: React.ReactNode
  /** Read to a screen reader, never drawn. See the note at the call site. */
  description?: React.ReactNode
  size?: keyof typeof WIDTHS
  /**
   * `alert` for a question that interrupts: a confirmation. It is announced
   * as an alert dialog, a press outside it does not dismiss it (only Cancel,
   * the cross or Escape do), and its sentence is the dialog's accessible
   * description — drawn, in the body, through `ModalDescription`.
   */
  variant?: "form" | "alert"
  /**
   * The role an alert is announced with; `alertdialog` unless held back.
   * Held back to `dialog` only while a caller's browser specs still find it
   * by the old role — see `ConfirmDialog`.
   */
  role?: "dialog" | "alertdialog"
  /** Controls that sit in the title strip, beside the close button. */
  actions?: React.ReactNode
  footer?: React.ReactNode
  /**
   * The action in flight, drawn into the footer: its words beside the buttons
   * and its track along the footer's top edge, so the dialog keeps its frame.
   */
  progress?: (OperationReading & { startedAt?: number | string }) | false
  /**
   * Where the keyboard lands when the dialog opens. A form wants its first
   * field; a viewer wants nothing in particular — its first control is an
   * icon button whose tooltip would open with the dialog, and Escape would
   * then close the tooltip rather than the viewer.
   */
  initialFocus?: "first" | "body" | React.RefObject<HTMLElement | null>
  bodyClassName?: string
  className?: string
  /**
   * Where the keyboard goes on close, for a dialog that knows better than the
   * default: `preventDefault()` and focus it. Left alone, the keyboard goes
   * back to what opened the dialog (`useFocusReturn`).
   */
  onCloseAutoFocus?: (event: Event) => void
  children: React.ReactNode
}) {
  const body = useRef<HTMLDivElement>(null)
  const returnFocus = useFocusReturn(open)
  const alert = variant === "alert"
  // A press outside an alert is not an answer to it (WAI-ARIA's alert
  // dialog, and Radix's own AlertDialog, which this is built the way of).
  const holdOutside = alert ? (event: Event) => event.preventDefault() : undefined
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        // Spread, not `role={undefined}`: Radix spreads its props after its
        // own `role="dialog"`, and an undefined one would take the role away.
        {...(alert && { role: role ?? "alertdialog" })}
        data-variant={variant}
        className={cn(
          "flex max-h-[calc(100svh-4rem)] flex-col gap-0 overflow-hidden rounded-xl p-0",
          WIDTHS[size],
          className,
        )}
        onPointerDownOutside={holdOutside}
        onInteractOutside={holdOutside}
        onOpenAutoFocus={
          initialFocus === "first"
            ? undefined
            : (event) => {
                const target = initialFocus === "body" ? body.current : initialFocus.current
                if (!target) return
                event.preventDefault()
                target.focus({ preventScroll: initialFocus === "body" })
              }
        }
        onCloseAutoFocus={(event) => {
          onCloseAutoFocus?.(event)
          returnFocus(event)
        }}
      >
        <DialogHeader className="shrink-0 gap-1 border-b border-hairline bg-surface-header px-4 py-3 pr-12 text-left">
          <div className="flex min-w-0 items-center gap-3">
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
            {actions && (
              <div className="flex shrink-0 flex-wrap items-center gap-1.5">{actions}</div>
            )}
          </div>
        </DialogHeader>

        <div
          ref={body}
          tabIndex={initialFocus === "body" ? -1 : undefined}
          // Focus is only ever sent here (tabIndex -1), which a ring would box whole.
          // eslint-disable-next-line design-system/tokens
          className={cn("min-h-0 flex-1 overflow-y-auto outline-none", bodyClassName ?? "p-4")}
        >
          {children}
        </div>

        {(footer || progress) && (
          <div className="relative flex shrink-0 flex-wrap items-center justify-end gap-2 border-t border-hairline bg-surface-header px-4 py-3">
            {progress && <OperationProgress {...progress} />}
            {footer}
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}

/**
 * The sentence of an alert `Modal`, drawn in its body and read as the
 * dialog's description: what will happen, in plain language. A confirmation
 * had it as body text only, so a screen reader announced "Remove volume,
 * alert dialog" and nothing of what removing it would do.
 */
export function ModalDescription({
  className,
  children,
}: {
  className?: string
  children: React.ReactNode
}) {
  return (
    <DialogDescription
      asChild
      className={cn("space-y-2 text-body leading-relaxed text-foreground", className)}
    >
      <div>{children}</div>
    </DialogDescription>
  )
}

/**
 * The thing a dialog acts on, drawn as itself at the head of the body: its
 * mark (a `ProductLogo`, `ProjectMark` or `EngineMark` at `size="sm"`), its
 * name, and the facts that tell it from its neighbours as `FormFact`s.
 * "Remove credential" over GitHub's logo and "GitHub PAT · github.com" is
 * recognised before it is read, which is the moment a wrong one gets caught
 * (§14). `ConfirmDialog` draws its `subject` with this, and a dialog that
 * edits one thing — a key's member, a set of documents — opens on it the same
 * way rather than composing its own.
 */
export function DialogSubject({
  mark,
  name,
  mono,
  facts,
  className,
}: {
  mark?: React.ReactNode
  name: React.ReactNode
  /** A name that is an identifier — a key, a path — rather than a word. */
  mono?: boolean
  facts?: React.ReactNode
  className?: string
}) {
  return (
    <div data-slot="dialog-subject" className={cn("flex min-w-0 items-center gap-3", className)}>
      {mark}
      <div className="min-w-0 space-y-0.5">
        <p className={cn("truncate text-body font-medium", mono && "font-mono")}>{name}</p>
        {facts && <FormFacts>{facts}</FormFacts>}
      </div>
    </div>
  )
}

const STAGE_BUTTON =
  "flex size-8 shrink-0 items-center justify-center rounded-md text-white focus-ring transition-colors hover:bg-white/10 pointer-coarse:size-10 [&_svg]:size-4"

/**
 * The third dialog surface: a viewer that takes the whole window on a dark
 * ground — a picture in the lightbox. Not `Modal size="full"`, whose title
 * strip and footer are a form's chrome on the page's own ground; a picture
 * wants nothing between it and the dark but a strip of white controls.
 *
 * It is still a dialog, assembled here so that nothing outside this file
 * reaches for the raw one: the keyboard is held inside it, Escape and the
 * cross close it, its `title` is its accessible name, and the keyboard goes
 * back to what opened it.
 */
export function StageModal({
  open,
  onOpenChange,
  title,
  actions,
  slot = "stage",
  onKeyDown,
  className,
  children,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Drawn in the strip, and the dialog's accessible name. */
  title: React.ReactNode
  /** Controls in the strip, before the cross. */
  actions?: React.ReactNode
  /** The data-slot of the content; the ground is `${slot}-overlay`. */
  slot?: string
  onKeyDown?: React.KeyboardEventHandler<HTMLDivElement>
  className?: string
  children: React.ReactNode
}) {
  const returnFocus = useFocusReturn(open)
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogPortal>
        <DialogOverlay data-slot={`${slot}-overlay`} className="bg-black/95 backdrop-blur-sm" />
        <DialogBareContent
          data-slot={slot}
          aria-describedby={undefined}
          onKeyDown={onKeyDown}
          onCloseAutoFocus={returnFocus}
          className={cn(
            "flex flex-col duration-base data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:animate-in data-[state=open]:fade-in-0",
            className,
          )}
        >
          <div className="flex h-12 shrink-0 items-center gap-2 px-3 text-white">
            <DialogTitle className="min-w-0 flex-1 truncate text-body font-normal">
              {title}
            </DialogTitle>
            {actions}
            <DialogClose aria-label="Close" title="Close (Esc)" className={STAGE_BUTTON}>
              <Cross />
            </DialogClose>
          </div>
          {children}
        </DialogBareContent>
      </DialogPortal>
    </Dialog>
  )
}

/** A control in a `StageModal`'s strip: white on the dark ground, the cross's size. */
export const STAGE_CONTROL = STAGE_BUTTON

/**
 * The other dialog surface: a search overlay whose own input *is* its header —
 * the command palette.
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
  onEscapeKeyDown,
  onCloseAutoFocus,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** The accessible name. Never drawn — the input carries the visible prompt. */
  label: string
  description: string
  className?: string
  children: React.ReactNode
  onEscapeKeyDown?: (event: KeyboardEvent) => void
  onCloseAutoFocus?: (event: Event) => void
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {/* Hung from a fixed line near the top rather than centred: centred, the
          dialog re-centred on every keystroke as the results grew and shrank,
          and the input being typed into jumped up and down the screen. On a
          phone the line is the top, clear of the keyboard that rises under it. */}
      <DialogContent
        className={cn(
          "top-4 max-h-[calc(100svh-2rem)] translate-y-0 gap-0 overflow-hidden rounded-xl p-0 data-[state=closed]:slide-out-to-top-2 data-[state=open]:slide-in-from-top-2 sm:top-[12svh] sm:max-h-[calc(88svh-2rem)] sm:max-w-2xl",
          className,
        )}
        showCloseButton={false}
        onEscapeKeyDown={onEscapeKeyDown}
        onCloseAutoFocus={onCloseAutoFocus}
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
