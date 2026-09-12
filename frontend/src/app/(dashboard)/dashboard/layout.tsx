"use client"

import { SectionNav } from "@/components/tabs"

/**
 * The dashboard's own two pages: what version it is, and how it is configured.
 *
 * They are separated because they answer different questions and are used at
 * different moments — "what changed in 0.7" is read before an upgrade, "which
 * port does this answer on" is opened when something is wrong. The strip is
 * the same switcher Docker and Databases use, so the pattern is learned once.
 */
const TABS = [
  { title: "Version", href: "/dashboard" },
  { title: "Configuration", href: "/dashboard/configuration" },
]

export default function DashboardLayout({ children }: { children: React.ReactNode }) {
  return (
    <>
      <SectionNav tabs={TABS} />
      {children}
    </>
  )
}
