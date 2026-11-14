"use client"

import { Fragment } from "react"
import { Check } from "@/components/icons"
import { useAuth } from "@/hooks/use-auth"
import { PURPOSES, pressPurpose, purposeShown, type SectionKey } from "@/lib/sidebar-sections"
import { cn } from "@/lib/utils"
import { NAV, hideableSections, shownFor, type NavChild, type NavEntry } from "@/components/nav"
import { ChoiceCard } from "@/components/choice-card"
import { ProductLogo } from "@/components/product-logo"
import { SwitchRow, SwitchRows } from "@/components/settings-column"
import { Tag } from "@/components/tag"

const NOTHING_HIDDEN: ReadonlySet<string> = new Set()

/**
 * The sections the role may keep out of its sidebar. Only those are offered:
 * a switch for Terminal on an account that cannot open a terminal would
 * decide nothing.
 */
function useOffered() {
  const { can } = useAuth()
  const sections = hideableSections(can).flatMap((group) => group.sections)
  return {
    can,
    sections,
    offered: new Set<string>(sections.map(({ key }) => key)),
  }
}

/** How many of the role's sections a set of hidden ones leaves in the rail. */
export function useSectionCount(hidden: ReadonlySet<string>) {
  const { sections } = useOffered()
  return {
    shown: sections.filter(({ key }) => !hidden.has(key)).length,
    total: sections.length,
  }
}

/**
 * What an experimental purpose says on its card. Stable is the rule and
 * carries no mark; the one purpose still being tested is the exception, so
 * it is flagged where it is chosen rather than on a shelf of its own.
 */
export function ExperimentalMark({ className }: { className?: string }) {
  return (
    <span className={cn("flex min-w-0 items-baseline gap-2", className)}>
      <Tag tone="warning" className="shrink-0">
        Experimental
      </Tag>
      <span className="truncate text-hint text-muted-foreground">Working, still being tested</span>
    </span>
  )
}

/**
 * The first-run question's purposes, after the first run, in its order and
 * with its mark on the experimental one. A purpose is chosen when every
 * section it needs is in the rail —
 * only the sections are stored — so a card reads what the switches beside it
 * say, and pressing one brings its sections back or takes them out. Under
 * each name are the sections it means, the ones out of the rail dimmed, which
 * is what makes a purpose that is partly shown legible without a count.
 */
export function PurposePicker({
  hidden,
  onChange,
}: {
  hidden: ReadonlySet<string>
  onChange: (hidden: Set<string>) => void
}) {
  const { sections, offered } = useOffered()

  return (
    <div role="group" aria-label="Purposes" className="grid min-w-0 gap-2.5 sm:grid-cols-2">
      {PURPOSES.map((purpose) => {
        const on = purposeShown(purpose.key, hidden, offered)
        const uses = sections.filter(({ key }) =>
          (purpose.sections as readonly SectionKey[]).includes(key),
        )
        return (
          <ChoiceCard
            key={purpose.key}
            selected={on}
            aria-label={purpose.title}
            onClick={() => onChange(pressPurpose(purpose.key, hidden, offered))}
            // One height for every card: the longest purpose lists its sections
            // on two lines, and a grid whose rows differ reads as a broken one.
            className="min-h-20 flex-row items-center gap-3 px-3.5 py-3"
          >
            <span className="flex shrink-0 gap-1">
              {purpose.products.map((product) => (
                <ProductLogo key={product} id={product} size="sm" />
              ))}
            </span>
            <span className="grid min-w-0 flex-1 gap-1">
              <span className="flex min-w-0 items-baseline gap-2">
                <span className="shrink-0 text-title leading-tight font-semibold">
                  {purpose.title}
                </span>
                {/* The mark alone: its sentence has no room in a grid of two. */}
                {purpose.stage === "experimental" && (
                  <Tag tone="warning" className="shrink-0" title="Working, still being tested">
                    Experimental
                  </Tag>
                )}
              </span>
              <span className="line-clamp-2 text-hint text-muted-foreground">
                {uses.map(({ key, entry }, index) => (
                  <Fragment key={key}>
                    {index > 0 && " · "}
                    <span className={cn(hidden.has(key) && "text-muted-foreground/45")}>
                      {entry.title}
                    </span>
                  </Fragment>
                ))}
              </span>
            </span>
            <Check
              aria-hidden
              className={cn(
                "size-4 shrink-0 text-brand transition-opacity",
                on ? "opacity-100" : "opacity-0",
              )}
            />
          </ChoiceCard>
        )
      })}
    </div>
  )
}

/**
 * The rail's entries, with a switch on every one the account may hide, as the
 * rows of a settings section: full width, one line each, a hairline between.
 *
 * It walks the registry the sidebar walks, in its groups and order and with
 * its glyphs, so the switches are laid out where the rows they decide are —
 * a list of sixteen names beside a picture of the rail was the same list
 * twice. Overview, Monitoring and Settings are drawn because every rail has
 * them, and say so instead of carrying a switch; Administration is a
 * row that holds three sections, so it is drawn as their heading.
 *
 * It was a framed box in the sidebar's ground beside the purposes, a picture
 * of the rail at the rail's width. In the column it is what a project's
 * settings draw for a run of switches, and nothing is framed.
 */
export function SidebarEditor({
  hidden,
  onChange,
}: {
  hidden: ReadonlySet<string>
  onChange: (hidden: Set<string>) => void
}) {
  const { can, sections } = useOffered()
  const keyOf = new Map<string, SectionKey>(sections.map(({ key, entry }) => [entry.href, key]))
  const toggle = (key: SectionKey) => {
    const next = new Set(hidden)
    if (next.has(key)) next.delete(key)
    else next.add(key)
    onChange(next)
  }
  const switchFor = (entry: NavEntry | NavChild, inner?: boolean) => {
    const key = entry.href === undefined ? undefined : keyOf.get(entry.href)
    return (
      key && (
        <SectionSwitch
          key={entry.title}
          entry={entry}
          inner={inner}
          on={!hidden.has(key)}
          onToggle={() => toggle(key)}
        />
      )
    )
  }
  const groups = NAV.map((group) => ({
    label: group.label,
    items: group.items.filter((item) => shownFor(item, NOTHING_HIDDEN, "", can)),
  })).filter((group) => group.items.length > 0)

  return (
    <div
      role="group"
      aria-label="Sidebar sections"
      data-slot="sidebar-editor"
      className="min-w-0 space-y-5"
    >
      {groups.map((group) => (
        <div key={group.label} className="min-w-0 space-y-1">
          <p className="eyebrow">{group.label}</p>
          <SwitchRows>
            {group.items.flatMap((item) => {
              const own = switchFor(item)
              if (own) return [own]
              const inner =
                item.href === undefined
                  ? item.children.filter((child) => shownFor(child, NOTHING_HIDDEN, "", can))
                  : []
              const held = inner.map((child) => switchFor(child, true)).filter(Boolean)
              if (held.length > 0) {
                return [
                  <FixedRow key={item.title} entry={item} muted />,
                  ...(held as React.ReactElement[]),
                ]
              }
              return [<FixedRow key={item.title} entry={item} word="Always" />]
            })}
          </SwitchRows>
        </div>
      ))}
    </div>
  )
}

const GLYPH = "size-4 shrink-0"

/** A row that carries no switch: one every rail has, or the heading of the ones under it. */
function FixedRow({
  entry,
  word,
  muted,
}: {
  entry: NavEntry | NavChild
  word?: string
  muted?: boolean
}) {
  return (
    <div className="min-w-0">
      <div
        className={cn(
          "flex min-h-11 min-w-0 items-center gap-2 text-sm leading-snug font-medium sm:min-h-10",
          muted && "text-muted-foreground",
        )}
      >
        <entry.icon aria-hidden className={GLYPH} />
        <span className="min-w-0 flex-1 truncate">{entry.title}</span>
        {word && <span className="text-hint font-normal text-muted-foreground">{word}</span>}
      </div>
    </div>
  )
}

function SectionSwitch({
  entry,
  inner,
  on,
  onToggle,
}: {
  entry: NavEntry | NavChild
  /** A section held by a row above it: set in, as the rail sets it in. */
  inner?: boolean
  on: boolean
  onToggle: () => void
}) {
  return (
    <SwitchRow
      className={cn(inner && "pl-6")}
      leading={
        <entry.icon
          aria-hidden
          className={cn(GLYPH, "transition-opacity", !on && "text-muted-foreground opacity-60")}
        />
      }
      title={
        <span className={cn("transition-colors", !on && "text-muted-foreground")}>
          {entry.title}
        </span>
      }
      checked={on}
      onCheckedChange={onToggle}
    />
  )
}
