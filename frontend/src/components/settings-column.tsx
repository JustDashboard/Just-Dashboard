"use client"

import { useId } from "react"
import { cn } from "@/lib/utils"
import { FormSections, InfoTip } from "@/components/form"
import { Status, type DotTone } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Switch } from "@/components/ui/switch"

/**
 * The settings column for a page that is not a deployment's: the dashboard's
 * own Configuration, and the account's Profile, Security and Workspace.
 *
 * `deploy/settings/setting-card.tsx` draws the column for a project, around a
 * configuration it reads itself. These pages have their own reads, so this is
 * the same column with nothing loaded in it: one centred run of
 * `SettingSection`s at the fields' width, what is pending at its head, and
 * room at its foot for the floating save bar (`SaveBarProvider`, which the
 * page puts around it), so the bar never lies over the last field.
 */
export function SettingsColumn({
  strip,
  className,
  children,
}: {
  /** A `PendingStrip`, or nothing: what is saved or typed and not in force yet. */
  strip?: React.ReactNode
  className?: string
  children: React.ReactNode
}) {
  return (
    <div
      data-slot="settings-column"
      className={cn("mx-auto w-full max-w-3xl min-w-0 space-y-8 pb-20", className)}
    >
      {strip}
      <FormSections>{children}</FormSections>
    </div>
  )
}

/**
 * A run of `SwitchRow`s, a hairline between each. Every row keeps the same
 * room above and below it and the same edge over it, the first too, where the
 * edge is not drawn: so a list of one is exactly as tall as a row of a longer
 * one, and the first row of a list as the rest.
 */
export function SwitchRows({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      className={cn(
        "min-w-0 [&>*]:border-t [&>*]:border-transparent [&>*]:py-1 [&>*+*]:border-hairline",
        className,
      )}
      {...props}
    />
  )
}

/**
 * A switch as a full-width row: what it switches at the left, the switch at
 * the far end, and nothing under it. An `OptionRow` carries a sentence under
 * its title; here the sentence is behind the ⓘ beside the name, so every row
 * of a settings column is one line, and a row with a reason is as tall as one
 * without.
 *
 * The name is the switch's label, and a press anywhere else on the row throws
 * the switch too, so the whole row is the pointer's target. The ⓘ stands
 * beside the label rather than inside it: a label may hold one control, and
 * that one is the switch.
 */
export function SwitchRow({
  leading,
  title,
  info,
  trailing,
  checked,
  onCheckedChange,
  disabled,
  className,
}: {
  /** The glyph or product the switch is about, before its name. */
  leading?: React.ReactNode
  title: React.ReactNode
  /** Why the switch exists and what it costs, behind the ⓘ. */
  info?: React.ReactNode
  /** A word before the switch: what the row is while it is off. */
  trailing?: React.ReactNode
  checked: boolean
  onCheckedChange: (checked: boolean) => void
  disabled?: boolean
  className?: string
}) {
  const id = useId()
  const name = `${id}-name`
  return (
    <div data-slot="switch-row" className={cn("min-w-0", className)}>
      {/* eslint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-static-element-interactions -- the pointer's larger target: the name is the label a keyboard and a reader reach the switch by */}
      <div
        onClick={(event) => {
          if (disabled || (event.target as HTMLElement).closest(CONTROL)) return
          onCheckedChange(!checked)
        }}
        className={cn(
          "-mx-3 flex min-h-11 min-w-0 items-center gap-2 rounded-md px-3 transition-colors hover:bg-row-hover sm:min-h-10",
          !disabled && "cursor-pointer",
        )}
      >
        <label
          htmlFor={id}
          id={name}
          className={cn(
            "flex min-w-0 items-center gap-2 text-sm leading-snug font-medium",
            !disabled && "cursor-pointer",
          )}
        >
          {leading}
          <span className="min-w-0 truncate">{title}</span>
        </label>
        {info && <InfoTip>{info}</InfoTip>}
        <span className="min-w-2 flex-1" />
        {trailing && <span className="shrink-0 text-hint text-muted-foreground">{trailing}</span>}
        <Switch
          id={id}
          className="shrink-0"
          aria-labelledby={name}
          checked={checked}
          onCheckedChange={onCheckedChange}
          disabled={disabled}
        />
      </div>
    </div>
  )
}

/** What in a row owns its own press, so a press on it is not a press on the row. */
const CONTROL = "a, button, label, [role='switch']"

/** A section holding a change that is not applied yet, in git's colour for a modified file. */
export function EditedTag() {
  return <Tag style={{ color: "var(--git-modified)" }}>Edited</Tag>
}

/**
 * A run of one-line notices under a page's identity line, a hairline between
 * each: a `Status` in the colour of what it means, what it is about in a few
 * muted words, and the verb that answers it at the far end. Where a framed
 * `Notice` with a paragraph stood between the header and the page.
 */
export function NoticeLines({ className, ...props }: React.ComponentProps<"ul">) {
  return (
    <ul
      data-slot="notice-lines"
      className={cn("-mt-3 min-w-0 divide-y divide-hairline md:-mt-5", className)}
      {...props}
    />
  )
}

export function NoticeLine({
  tone,
  title,
  detail,
  children,
}: {
  tone: DotTone
  title: React.ReactNode
  detail?: React.ReactNode
  /** The verb: a small outline button, or a link. */
  children?: React.ReactNode
}) {
  return (
    <li className="flex min-h-11 min-w-0 flex-wrap items-center gap-x-3 gap-y-1 py-2">
      <Status tone={tone} label={title} className="font-medium" />
      {detail && <span className="min-w-0 flex-1 text-hint text-muted-foreground">{detail}</span>}
      {children && <span className="ml-auto flex shrink-0 items-center gap-2">{children}</span>}
    </li>
  )
}
