"use client"

import { SectionNav } from "@/components/tabs"

/**
 * Processes is four pages, not one screen of tabs: what is running, what PM2
 * runs, what systemd runs, and what runs on a schedule. Each has its own
 * readings, its own filters and its own detail surface, and a tab strip in a
 * panel gave them one page title and one scroll position between them. The
 * sidebar entry expands to the four; this strip is the switcher for when it
 * is collapsed to the icon rail. There is no reachability check here — the
 * process table exists on every host, and the pages that depend on an
 * optional manager say so themselves.
 */
const TABS = [
  { title: "Live", href: "/processes" },
  { title: "PM2", href: "/processes/pm2" },
  { title: "Services", href: "/processes/services" },
  { title: "Scheduled", href: "/processes/scheduled" },
]

export default function ProcessesLayout({ children }: { children: React.ReactNode }) {
  return (
    <>
      <SectionNav tabs={TABS} />
      {children}
    </>
  )
}
