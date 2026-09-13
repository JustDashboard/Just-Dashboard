"use client"

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
 */
export function SidePanel({
  open,
  onOpenChange,
  title,
  description,
  actions,
  footer,
  width = "lg",
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
  bodyClassName?: string
  className?: string
  children: React.ReactNode
}) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className={cn("flex w-full flex-col gap-0 p-0", WIDTHS[width], className)}
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

        <div className={cn("min-h-0 flex-1 overflow-y-auto", bodyClassName ?? "p-4")}>
          {children}
        </div>

        {footer && (
          <div className="flex shrink-0 flex-wrap items-center gap-2 border-t border-hairline bg-surface-header px-4 py-3">
            {footer}
          </div>
        )}
      </SheetContent>
    </Sheet>
  )
}
