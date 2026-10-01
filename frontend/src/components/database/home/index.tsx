"use client"

import { Fragment } from "react"
import Link from "next/link"
import { Warning } from "@/components/icons"
import { relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import { ChoiceList, ChoiceRow, GroupRule } from "@/components/flow"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Notice } from "@/components/state"
import { sectionGroups } from "@/components/database/engine"
import { EngineMark, EnvironmentTag, ProtectedTag, SectionFrame } from "@/components/database/kit"
import { ConnectPopover } from "@/components/database/shell/connect-popover"
import { ConnectionSwitcher } from "@/components/database/shell/connection-switcher"
import { useDatabase } from "@/components/database/shell/database-context"
import { DatabaseVerbs } from "@/components/database/shell/database-verbs"
import { connectionFacts } from "@/components/database/shell/facts"
import { DatabaseStatusMark } from "@/components/database/shell/status"

/**
 * A database's front page.
 *
 * It opens on the identity line the host Overview and a project open on
 * (`HostIdentity`): the engine as the mark, the name — which is also the
 * control that goes to another database — what it is and where, and at the
 * line's end whether it answers, how to connect to it, and its verbs. The
 * strip the other pages carry is this line made small, so the home draws the
 * line and no strip.
 *
 * Under it are the database's pages as things you take, in the rail's own
 * groups and the engine's own words. The readings a front page is for — what
 * it holds, what is using it, whether it is backed up — are the home area's
 * to add, above these.
 */
export function DatabaseHome() {
  const { conn, summary, engine, status, readOnly, href } = useDatabase()
  const down = status.state === "unreachable" || status.state === "broken"
  return (
    <SectionFrame section="home">
      <HostIdentity
        logo={<EngineMark engine={engine} size="lg" />}
        title={<ConnectionSwitcher className="text-title font-semibold tracking-tight" />}
        facts={
          <>
            {connectionFacts(conn, engine, summary).map((fact, index) => (
              <Fragment key={fact.key}>
                {index > 0 && <FactDot />}
                <span title={fact.title} className={cn("truncate", fact.mono && "font-mono")}>
                  {fact.text}
                </span>
              </Fragment>
            ))}
            {conn.user && (
              <>
                <FactDot />
                <span>as {conn.user}</span>
              </>
            )}
            <FactDot />
            <span>added {relativeTime(conn.createdAt)}</span>
          </>
        }
        aside={
          <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
            <EnvironmentTag environment={conn.environment} />
            {readOnly && <ProtectedTag />}
            <DatabaseStatusMark status={status} />
            <ConnectPopover />
            <DatabaseVerbs />
          </div>
        }
      />

      {down && (
        <Notice tone="danger" icon={Warning} title="This database is not answering">
          {status.error ? <p className="font-mono wrap-anywhere">{status.error}</p> : null}
          <p>
            The stored address and password are under{" "}
            <Link href={href("settings")} className="underline">
              Settings
            </Link>
            .
          </p>
        </Notice>
      )}

      <div className="grid gap-8">
        {sectionGroups(engine)
          .filter((group) => group.id !== "home")
          .map((group) => (
            <section key={group.id} className="flex min-w-0 flex-col gap-3">
              <GroupRule label={group.label ?? ""} />
              {/* A list of lit cards sits on the page, never in a frame (§12). */}
              <ChoiceList className="grid gap-2 space-y-0 md:grid-cols-2 xl:grid-cols-3">
                {group.sections.map((section, index) => (
                  <ChoiceRow
                    key={section.id}
                    index={index}
                    href={href(section.id)}
                    verb={`Open ${section.title}`}
                    title={section.title}
                    leading={<section.icon className="size-4 text-muted-foreground" />}
                  />
                ))}
              </ChoiceList>
            </section>
          ))}
      </div>
    </SectionFrame>
  )
}
