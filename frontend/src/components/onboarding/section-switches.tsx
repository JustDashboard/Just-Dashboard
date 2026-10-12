"use client"

import { ChevronRight } from "@/components/icons"
import { useAuth } from "@/hooks/use-auth"
import { SECTION_HINTS, type SectionKey } from "@/lib/sidebar-sections"
import { cn } from "@/lib/utils"
import { NAV, hideableSections, shownFor } from "@/components/nav"
import { OptionList, OptionRow } from "@/components/form"

/**
 * One switch per section the account may keep out of its sidebar, under the
 * rail's own group names and in its order — so the list reads as the sidebar
 * it is deciding rather than as a settings form.
 *
 * Only the sections the role can see are offered: a switch for Terminal on an
 * account that cannot open a terminal would decide nothing.
 */
export function SectionSwitches({
  hidden,
  onChange,
}: {
  hidden: ReadonlySet<string>
  onChange: (hidden: Set<string>) => void
}) {
  const { can } = useAuth()
  const toggle = (key: SectionKey, shown: boolean) => {
    const next = new Set(hidden)
    if (shown) next.delete(key)
    else next.add(key)
    onChange(next)
  }

  return (
    <div className="space-y-5">
      {hideableSections(can).map((group) => (
        <section key={group.label} aria-label={group.label} className="space-y-1.5">
          <p className="eyebrow">{group.label}</p>
          <OptionList>
            {group.sections.map(({ key, entry }) => (
              <OptionRow
                key={key}
                title={
                  <span className="flex items-center gap-2">
                    <entry.icon className="size-4 shrink-0 text-muted-foreground" />
                    {entry.title}
                  </span>
                }
                hint={SECTION_HINTS[key]}
                checked={!hidden.has(key)}
                onCheckedChange={(shown) => toggle(key, shown)}
              />
            ))}
          </OptionList>
        </section>
      ))}
    </div>
  )
}

/** How many of the role's sections a set of hidden ones leaves in the rail. */
export function useSectionCount(hidden: ReadonlySet<string>) {
  const { can } = useAuth()
  const keys = hideableSections(can).flatMap((group) => group.sections.map(({ key }) => key))
  return { shown: keys.filter((key) => !hidden.has(key)).length, total: keys.length }
}

/**
 * The rail as it will be drawn, beside the switches that decide it.
 *
 * It walks the same registry the sidebar does with the same rule, so what
 * this shows is what the account will get — Overview, Monitoring and
 * Settings included, because they are always there and a preview without
 * them would promise a shorter rail than the one that arrives. A row that a
 * switch brings back rises into place, which is what ties the two together.
 */
export function SidebarPreview({
  hidden,
  className,
}: {
  hidden: ReadonlySet<string>
  className?: string
}) {
  const { can } = useAuth()
  const groups = NAV.map((group) => ({
    label: group.label,
    items: group.items.filter((item) => shownFor(item, hidden, "", can)),
  })).filter((group) => group.items.length > 0)

  return (
    <nav
      aria-label="Sidebar preview"
      data-slot="sidebar-preview"
      className={cn("min-w-0 rounded-xl border bg-sidebar px-2 py-1.5", className)}
    >
      {groups.map((group) => (
        <div key={group.label} className="py-1.5">
          <p className="eyebrow flex h-6 items-center px-2">{group.label}</p>
          <ul className="grid gap-0.5">
            {group.items.map((item) => (
              <li
                key={item.href ?? item.title}
                className="flex h-8 min-w-0 animate-rise items-center gap-2 rounded-md px-2 text-body text-sidebar-foreground"
              >
                <item.icon aria-hidden className="size-4 shrink-0" />
                <span className="min-w-0 flex-1 truncate">{item.title}</span>
                {item.children && (
                  <ChevronRight aria-hidden className="size-4 shrink-0 opacity-70" />
                )}
              </li>
            ))}
          </ul>
        </div>
      ))}
    </nav>
  )
}
