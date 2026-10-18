"use client"

import { Fragment } from "react"
import { Check } from "@/components/icons"
import { useAuth } from "@/hooks/use-auth"
import { PURPOSES, pressPurpose, purposeShown, type SectionKey } from "@/lib/sidebar-sections"
import { cn } from "@/lib/utils"
import { NAV, hideableSections, shownFor, type NavChild, type NavEntry } from "@/components/nav"
import { ChoiceCard } from "@/components/choice-card"
import { ProductLogo } from "@/components/product-logo"
import { Tag } from "@/components/tag"
import { Switch } from "@/components/ui/switch"

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
    <div role="group" aria-label="Purposes" className="grid min-w-0 gap-2.5 2xl:grid-cols-2">
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
            className="min-h-18 flex-row items-center gap-3.5 px-4 py-3"
          >
            <span className="flex shrink-0 gap-1.5">
              {purpose.products.map((product) => (
                <ProductLogo key={product} id={product} size="sm" />
              ))}
            </span>
            <span className="grid min-w-0 flex-1 gap-1">
              <span className="flex min-w-0 items-baseline gap-2.5">
                <span className="shrink-0 text-title leading-tight font-semibold">
                  {purpose.title}
                </span>
                {purpose.stage === "experimental" && <ExperimentalMark />}
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
 * The rail itself, with a switch on every row the account may hide.
 *
 * It walks the registry the sidebar walks, in its groups and order and with
 * its glyphs, so the switches are laid out where the rows they decide are —
 * a list of sixteen names beside a picture of the rail was the same list
 * twice. Overview, Monitoring and Settings are drawn because every rail has
 * them, and say so instead of carrying a switch; Server configuration is a
 * row that holds three sections, so it is drawn as their heading.
 *
 * Framed in the sidebar's own ground, because it is a picture of the rail.
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
  const switchFor = (entry: NavEntry | NavChild) => {
    const key = entry.href === undefined ? undefined : keyOf.get(entry.href)
    return key && <SectionSwitch entry={entry} on={!hidden.has(key)} onToggle={() => toggle(key)} />
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
      className="min-w-0 rounded-xl border bg-sidebar px-2 py-1.5"
    >
      {groups.map((group) => (
        <div key={group.label} className="py-1.5">
          <p className="eyebrow flex h-6 items-center px-2">{group.label}</p>
          <ul className="grid gap-0.5">
            {group.items.map((item) => {
              const own = switchFor(item)
              if (own) return <li key={item.title}>{own}</li>
              const inner =
                item.href === undefined
                  ? item.children.filter((child) => shownFor(child, NOTHING_HIDDEN, "", can))
                  : []
              const held = inner.map((child) => ({ child, row: switchFor(child) }))
              if (held.some(({ row }) => row)) {
                return (
                  <li key={item.title}>
                    <span className="flex h-8 min-w-0 items-center gap-2 px-2 text-body text-muted-foreground">
                      <item.icon aria-hidden className="size-4 shrink-0" />
                      <span className="min-w-0 flex-1 truncate">{item.title}</span>
                    </span>
                    <ul className="ml-4 grid gap-0.5 border-l border-sidebar-border pl-2">
                      {held.map(({ child, row }) => (
                        <li key={child.title}>{row}</li>
                      ))}
                    </ul>
                  </li>
                )
              }
              return (
                <li
                  key={item.title}
                  className="flex h-8 min-w-0 items-center gap-2 px-2 text-body text-sidebar-foreground"
                >
                  <item.icon aria-hidden className="size-4 shrink-0" />
                  <span className="min-w-0 flex-1 truncate">{item.title}</span>
                  <span className="text-hint text-muted-foreground">Always</span>
                </li>
              )
            })}
          </ul>
        </div>
      ))}
    </div>
  )
}

function SectionSwitch({
  entry,
  on,
  onToggle,
}: {
  entry: NavEntry | NavChild
  on: boolean
  onToggle: () => void
}) {
  return (
    <label className="flex h-8 min-w-0 cursor-pointer items-center gap-2 rounded-md px-2 text-body transition-colors hover:bg-sidebar-accent">
      <entry.icon
        aria-hidden
        className={cn(
          "size-4 shrink-0 transition-opacity",
          on ? "text-sidebar-foreground" : "text-muted-foreground opacity-60",
        )}
      />
      <span
        className={cn(
          "min-w-0 flex-1 truncate transition-colors",
          on ? "text-sidebar-foreground" : "text-muted-foreground",
        )}
      >
        {entry.title}
      </span>
      <Switch size="sm" checked={on} onCheckedChange={onToggle} aria-label={entry.title} />
    </label>
  )
}
