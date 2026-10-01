"use client"

import { Fragment } from "react"
import { usePathname } from "next/navigation"
import { cn } from "@/lib/utils"
import { FactDot } from "@/components/metrics/host-identity"
import { EngineMark } from "@/components/database/kit/engine-mark"
import { EnvironmentTag, ProtectedTag } from "@/components/database/kit/environment-tag"
import { ConnectPopover } from "@/components/database/shell/connect-popover"
import { ConnectionSwitcher } from "@/components/database/shell/connection-switcher"
import { useDatabase } from "@/components/database/shell/database-context"
import { DatabaseVerbs } from "@/components/database/shell/database-verbs"
import { connectionFacts } from "@/components/database/shell/facts"
import { useDatabaseNavScope } from "@/components/database/shell/nav-scope"
import { databasePlace } from "@/components/database/shell/routes"
import { DatabaseStatusMark } from "@/components/database/shell/status"

/**
 * The frame around every page of one database.
 *
 * It publishes the database's pages to the rail, draws the identity strip,
 * and hands the rest of the window to the page. The strip is a fixed row and
 * the page's region below it is a definite height with its own scroll, so a
 * reading page scrolls inside it and a workbench (`<Page fill>`) is exactly
 * the space that is left — the strip never pushes a grid past the window.
 *
 * The page region is keyed on the connection and on what it is: every read
 * under it names the connection in its own dependencies, and a switch that
 * kept the old page mounted would draw one database's rows under another's
 * name for a poll. A server that turns out to be another product — one that
 * was stopped when the page opened, and answered as MariaDB once started —
 * gets its pages mounted afresh for the same reason: the page that was there
 * was built for an engine this is not.
 */
export function DatabaseShell({ children }: { children: React.ReactNode }) {
  const { conn, engine } = useDatabase()
  const pathname = usePathname()
  useDatabaseNavScope()
  const place = databasePlace(pathname)
  return (
    <div className="flex h-full min-h-0 flex-col">
      {/* The home opens on its own identity line, large; the strip under it
          would be the same facts twice. */}
      {place?.section !== "home" && <IdentityStrip />}
      <div key={`${conn.id}:${engine.id}`} className="min-h-0 min-w-0 flex-1 overflow-y-auto">
        {children}
      </div>
    </div>
  )
}

/**
 * Which database this is, on every page under it: its engine, its name as
 * the control that goes to another, what it is and where, what it is for,
 * whether it answers, and the two things asked of a database from any page —
 * how to connect to it, and its verbs.
 *
 * It is one compact row, not a header: the page's name is the rail's to say
 * (§14), and the facts are muted text rather than a second line of chrome.
 * On a workbench it takes the workbench's narrow gutters so its edge is the
 * frame's edge.
 *
 * What fits is decided by the strip's own width, not the window's (§12): the
 * rail beside it takes a fifth of a laptop. The name gives way before
 * anything is drawn over anything — it truncates, and the facts go first.
 * Below the width where the labels fit beside the status they take a line of
 * their own under the name rather than leave: "production" and "protected"
 * are the two words a workbench must not lose on a phone.
 */
function IdentityStrip() {
  const { conn, summary, engine, status, readOnly } = useDatabase()
  const pathname = usePathname()
  const section = databasePlace(pathname)?.section
  const workbench = Boolean(section && engine.section(section)?.workbench)
  const labelled = Boolean(conn.environment?.trim()) || readOnly
  return (
    <div data-slot="database-strip" className="@container shrink-0 border-b border-hairline">
      <div
        className={cn(
          "mx-auto flex w-full max-w-[1440px] min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5 py-2",
          workbench ? "px-2 md:px-3" : "px-5 md:px-8",
        )}
      >
        <div className="flex min-w-0 flex-1 items-center gap-x-3">
          <EngineMark engine={engine} size="sm" />
          <ConnectionSwitcher />
          {/* What it is and where, for a strip with the width to say it.
              The home says the same in full, so a narrow one loses nothing
              it cannot reach. It shrinks long before the name does. */}
          <span className="hidden min-w-0 shrink-[1000] items-center gap-x-2 overflow-hidden text-body text-muted-foreground @4xl:flex">
            {connectionFacts(conn, engine, summary).map((fact, index) => (
              <Fragment key={fact.key}>
                {index > 0 && <FactDot />}
                <span
                  title={fact.title}
                  className={cn("min-w-0 truncate", fact.mono && "font-mono text-xs")}
                >
                  {fact.text}
                </span>
              </Fragment>
            ))}
          </span>
        </div>
        {labelled && (
          <div
            data-slot="database-labels"
            className="order-last flex min-w-0 basis-full items-center gap-x-2.5 @max-3xl:pl-11 @3xl:order-none @3xl:basis-auto"
          >
            <EnvironmentTag environment={conn.environment} />
            {readOnly && <ProtectedTag />}
          </div>
        )}
        <div className="flex shrink-0 items-center gap-2">
          <DatabaseStatusMark status={status} />
          <ConnectPopover />
          <DatabaseVerbs />
        </div>
      </div>
    </div>
  )
}
