"use client"

import Link from "next/link"
import { ArrowLeft } from "@/components/icons"
import { PageContext } from "@/components/page"
import { PaneHeader } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"

/** The one way out of a workbench: a press, or a link to the list it was opened from. */
export type WorkbenchBack = {
  /** The control's name, which specs and screen readers find it by: "Back to repositories". */
  label: string
  /** Said on hover and focus, when it says more than the name. */
  hint?: string
} & ({ onClick: () => void; href?: never } | { href: string; onClick?: never })

/**
 * The strip a workbench opens on: the way back, what is open, and what can be
 * done to it.
 *
 * The board, Git and the editor each drew this themselves, three ways: the
 * back as an icon in one and as the words "← Files" in another, the name as a
 * field, as two lines or as a visible heading on a second row, the verbs
 * wherever the strip had room. They do one job (go back, say what is open,
 * act on it), so they are one strip now, and moving between workbenches reads
 * as moving inside one product. The session bar of a remote screen and the
 * terminal's window strip are other controls and keep their own.
 *
 * Read left to right: the back, always an arrow (`PaneBack`'s look, shown at
 * every width, since a workbench has no other way back on its page); the
 * mark, the product or the thing drawn as itself; the name, with its state
 * beside it where the strip has room and under it where it does not, and a
 * subline (the remote, the folder) under both; then the tools; then the one
 * command and the `⋯`. A workbench draws no page header, so the page's `h1`
 * is here, said and not drawn, and names the tab.
 *
 * Narrower than one row holds, it folds on purpose rather than wrapping where
 * the next control ran out of room: the back, the name and the verbs on the
 * first row, the tools stretched across a second. It measures itself, not the
 * window, so a workbench beside an open sidebar folds as a phone does.
 */
export function WorkbenchHeader({
  back,
  heading,
  mark,
  name,
  subline,
  state,
  tools,
  command,
  menu,
  className,
}: {
  back: WorkbenchBack
  /** The page's `h1`, said for a screen reader and the tab: "Drawing: Release plan". */
  heading: string
  /** The product logo or the thing's own mark, at the strip's size. */
  mark?: React.ReactNode
  /** What is open: a name, or a field that renames it. */
  name: React.ReactNode
  /** A line under the name: the remote a repository tracks, the folder a file is in. */
  subline?: React.ReactNode
  /** How it is: saved, unsaved, live with others. Beside the name, or under it on a narrow strip. */
  state?: React.ReactNode
  /** The workbench's own controls between the name and its verbs, a row of their own when narrow. */
  tools?: React.ReactNode
  /** The one command at the strip's end. */
  command?: React.ReactNode
  /** The `⋯` menu after it, and anything that closes the strip (an account). */
  menu?: React.ReactNode
  className?: string
}) {
  return (
    <>
      {/* Beside the strip, not in it: a phone hides the strip while a pane
          is read full screen, and the page keeps its name. */}
      <PageContext title={heading} />
      <PaneHeader
        data-workbench-header=""
        className={cn("@container h-auto min-h-12 px-2 py-1.5", className)}
      >
        <div className="grid w-full min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-2 gap-y-1.5 @4xl:flex">
          <div className="flex shrink-0 items-center gap-2">
            <BackButton back={back} />
            {mark}
          </div>
          <div className="min-w-0 @4xl:flex-1" data-slot="workbench-name">
            <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-0.5 @lg:flex-nowrap">
              <div className="flex min-w-0 items-center gap-2 text-body font-semibold">{name}</div>
              {state && (
                <div
                  data-slot="workbench-state"
                  className="flex min-w-0 items-center gap-x-3 @max-lg:basis-full"
                >
                  {state}
                </div>
              )}
            </div>
            {subline && (
              <div className="flex min-w-0 items-center gap-1.5 text-micro text-muted-foreground">
                {subline}
              </div>
            )}
          </div>
          {tools && (
            <div
              data-slot="workbench-tools"
              className="order-last col-span-3 flex min-w-0 items-center gap-2 @4xl:order-none"
            >
              {tools}
            </div>
          )}
          {(command || menu) && (
            <div className="flex shrink-0 items-center gap-2 justify-self-end">
              {command}
              {menu}
            </div>
          )}
        </div>
      </PaneHeader>
    </>
  )
}

function BackButton({ back }: { back: WorkbenchBack }) {
  const face = <ArrowLeft className="size-4" />
  const button = (
    <Button
      size="icon-sm"
      variant="ghost"
      className="shrink-0 text-muted-foreground hover:text-foreground"
      aria-label={back.label}
      asChild={back.href !== undefined}
      onClick={back.onClick}
    >
      {back.href !== undefined ? <Link href={back.href}>{face}</Link> : face}
    </Button>
  )
  return (
    <Tooltip>
      <TooltipTrigger asChild>{button}</TooltipTrigger>
      <TooltipContent>{back.hint ?? back.label}</TooltipContent>
    </Tooltip>
  )
}
