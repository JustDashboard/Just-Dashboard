"use client"

import { cn } from "@/lib/utils"
import { Panel, PanelBody, PanelFooter, PanelHeader } from "@/components/panel"

/**
 * One setting, as a card: a title, the form, and a footer that says when the
 * change applies at the left and holds Save at the right.
 *
 * This is the one place the deployment redesign keeps a frame. A settings
 * page is a stack of things you fill in and confirm one at a time, and the
 * 0.6.7 strip-down already settled that a framed form with a footer action is
 * the shape such a page reads best in; Vercel's settings are the same stack
 * of bordered cards. Everything else in the section is plain.
 *
 * `note` is the footer's sentence — "Applies on the next deployment" — not a
 * caption under the title: what the form is *for* is its title, and what the
 * fields need is said by their own hints.
 */
export function SettingCard({
  title,
  tone = "default",
  actions,
  note,
  action,
  bodyClassName,
  className,
  children,
  ...props
}: Omit<React.ComponentProps<"section">, "title"> & {
  title: React.ReactNode
  tone?: "default" | "danger"
  /** Controls in the title strip — a status, a small secondary button. */
  actions?: React.ReactNode
  /** The footer's left-hand sentence. */
  note?: React.ReactNode
  /** The footer's right-hand control, usually the Save button. */
  action?: React.ReactNode
  bodyClassName?: string
}) {
  return (
    <Panel className={cn(tone === "danger" && "border-rule-danger", className)} {...props}>
      <PanelHeader title={title} actions={actions} />
      <PanelBody className={cn("space-y-4", bodyClassName)}>{children}</PanelBody>
      {(note || action) && (
        <PanelFooter className="justify-between">
          <p className="min-w-0 text-hint leading-relaxed text-muted-foreground">{note}</p>
          {action && <div className="flex shrink-0 items-center gap-2">{action}</div>}
        </PanelFooter>
      )}
    </Panel>
  )
}
