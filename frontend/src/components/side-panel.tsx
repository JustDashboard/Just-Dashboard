"use client"

import { createContext, useContext, useRef, useState } from "react"
import { createPortal } from "react-dom"
import { cn } from "@/lib/utils"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"

const WIDTHS = {
  sm: "sm:max-w-lg",
  md: "sm:max-w-2xl",
  lg: "sm:max-w-3xl",
  xl: "sm:max-w-5xl",
} as const

/**
 * The detail surface: a container's stats, a repository's history, a vhost's
 * config, a backup's runs.
 *
 * Its header is the title and the actions, with no icon plot in front of them —
 * see the note at `PanelHeader`.
 *
 * Eight places opened a Sheet and each laid its own header out — some padded,
 * some not, some with the title in the body. They are one component now, so a
 * detail view opens the same way whichever page you came from, and the body is
 * the only part that scrolls: the title and the actions stay put while you
 * read down a long log.
 *
 * **What goes inside one.** The frame being one component was not enough: the
 * deploy sheets each put a different body in it — a Select for a kind, a
 * `<legend>` in eyebrow caps for a group, Cancel on one and not the next — so
 * a sheet was still recognisable by which page opened it. The anatomy, in
 * order, which is `Modal`'s too (they are one idea pointed two ways):
 *
 *   1. A sheet that edits a thing that exists opens on that thing: its
 *      `ProductLogo size="sm"`, its name, and `FormFacts` under them. The
 *      title may carry the same small logo inline — the title node is already
 *      a row.
 *   2. A choice between kinds — a credential kind, a channel kind, a webhook's
 *      provider — is a `ChoiceGrid columns={2}` of `ChoiceCard`s with their
 *      `logo`, never a `Select`: a kind is picked by its mark (§16).
 *   3. Groups of fields are stacked `FormSection`s, a title and a hairline,
 *      never an eyebrow `<legend>` quieter than the labels it heads (§8).
 *   4. Options are an `OptionList` of `OptionRow`s.
 *   5. The footer is Cancel (outline) and then the one command (brand),
 *      right-aligned the way `Modal`'s is. A line about what the command will
 *      do goes first with `mr-auto`, so it takes the left and pushes the
 *      buttons to the edge — `FormNote` or a hint `<span>`. A body that
 *      brings its own command — a form that opens in two sheets, a step whose
 *      command changes as it goes — puts it there with `SidePanelFooter`,
 *      after the sheet's own Cancel, rather than drawing a second foot in
 *      the body.
 */
export function SidePanel({
  open,
  onOpenChange,
  title,
  description,
  actions,
  footer,
  width = "lg",
  initialFocus = "first",
  bodyClassName,
  className,
  children,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: React.ReactNode
  /** Read to a screen reader, never drawn. */
  description?: React.ReactNode
  actions?: React.ReactNode
  footer?: React.ReactNode
  width?: keyof typeof WIDTHS
  /**
   * Where the keyboard lands when the sheet opens, as `Modal`'s does. A form
   * wants its first field; one whose first field already holds a sensible
   * value wants the body, because landing in it selects that value.
   */
  initialFocus?: "first" | "body"
  bodyClassName?: string
  className?: string
  children: React.ReactNode
}) {
  const body = useRef<HTMLDivElement>(null)
  const [slot, setSlot] = useState<HTMLElement | null>(null)
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className={cn("flex w-full flex-col gap-0 p-0", WIDTHS[width], className)}
        onOpenAutoFocus={
          initialFocus === "body"
            ? (event) => {
                event.preventDefault()
                body.current?.focus({ preventScroll: true })
              }
            : undefined
        }
      >
        <SheetHeader className="shrink-0 gap-1 border-b border-hairline bg-surface-header px-4 py-3 pr-12">
          <div className="flex min-w-0 items-start">
            <div className="min-w-0 flex-1">
              <SheetTitle className="flex min-w-0 flex-wrap items-center gap-2 text-title leading-tight">
                {title}
              </SheetTitle>
              {/* Not drawn — see the note in components/modal.tsx. Kept as
                  the accessible description Radix asks for. */}
              {description && (
                <SheetDescription className="sr-only">{description}</SheetDescription>
              )}
            </div>
          </div>
          {actions && <div className="flex flex-wrap items-center gap-2 pt-2">{actions}</div>}
        </SheetHeader>

        <div
          ref={body}
          tabIndex={initialFocus === "body" ? -1 : undefined}
          className={cn("min-h-0 flex-1 overflow-y-auto outline-none", bodyClassName ?? "p-4")}
        >
          <FooterSlot.Provider value={{ node: slot }}>{children}</FooterSlot.Provider>
        </div>

        <div
          className={cn(
            "flex shrink-0 flex-wrap items-center justify-end gap-2 border-t border-hairline bg-surface-header px-4 py-3",
            // No footer of its own and nothing brought into it: no strip.
            !footer && "has-[>[data-slot=side-panel-footer]:empty]:hidden",
          )}
        >
          {footer}
          <div ref={setSlot} data-slot="side-panel-footer" className="contents" />
        </div>
      </SheetContent>
    </Sheet>
  )
}

const FooterSlot = createContext<{ node: HTMLElement | null } | undefined>(undefined)

/** Whether this is drawn inside a `SidePanel`, whose footer `SidePanelFooter` can reach. */
export function useInSidePanel() {
  return useContext(FooterSlot) !== undefined
}

/**
 * A command the body brings with it, drawn in the sheet's footer after the
 * sheet's own. A form whose submit lands here is outside the form in the
 * document, so the button names it with `form`.
 */
export function SidePanelFooter({ children }: { children: React.ReactNode }) {
  const slot = useContext(FooterSlot)
  return slot?.node ? createPortal(children, slot.node) : null
}
