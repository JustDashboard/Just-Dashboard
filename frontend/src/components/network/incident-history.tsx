"use client"

import Link from "next/link"
import { useEffect, useState } from "react"
import type { NetworkIncident } from "@/lib/types"
import { Status } from "@/components/status-dot"
import { Notice } from "@/components/state"
import { incidentSpan } from "@/components/network/topology-reading"

/**
 * The attention list over time: when each finding was first seen, whether a
 * later reading no longer found it, and when its own reading failed — an
 * unobserved finding stays open rather than being called fixed. Findings that
 * began within two minutes of each other are named beside each other, which
 * is usually one event seen from several readings. History covers only the
 * moments the Overview was read.
 */
export function IncidentHistory({
  incidents,
  error,
}: {
  incidents: NetworkIncident[]
  error?: string
}) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 30_000)
    return () => clearInterval(timer)
  }, [])
  const titles = new Map(incidents.map((i) => [i.id, i.title]))
  return (
    <div className="mt-6 flex flex-col gap-3">
      <h3 className="eyebrow">History</h3>
      {error && (
        <Notice tone="warning" title="Incident history unavailable">
          {error}
        </Notice>
      )}
      {incidents.length === 0 ? (
        <p className="text-body text-muted-foreground">
          No finding has been recorded while this page was read.
        </p>
      ) : (
        <ol className="flex flex-col divide-y divide-hairline" aria-label="Incident history">
          {incidents.map((incident) => {
            const related = incident.related
              .map((id) => titles.get(id))
              .filter((t): t is string => !!t)
            return (
              <li key={incident.id} className="flex min-w-0 flex-col gap-1 py-2.5 text-body">
                <span className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
                  <Status
                    tone={
                      incident.resolvedAt
                        ? "stopped"
                        : incident.unobservedSince
                          ? "unknown"
                          : incident.level === "critical"
                            ? "danger"
                            : incident.level === "warning"
                              ? "warning"
                              : "notice"
                    }
                    label={
                      incident.resolvedAt
                        ? "Resolved"
                        : incident.unobservedSince
                          ? "Unobserved"
                          : "Open"
                    }
                    className="w-28 shrink-0"
                  />
                  <Link
                    href={incident.href}
                    className="min-w-0 truncate underline-offset-2 focus-ring hover:underline"
                  >
                    {incident.title}
                  </Link>
                </span>
                <span className="text-hint text-muted-foreground">
                  began{" "}
                  <time dateTime={incident.openedAt}>
                    {new Date(incident.openedAt).toLocaleString()}
                  </time>
                  {incident.resolvedAt ? (
                    <> · lasted {incidentSpan(incident, now)}</>
                  ) : (
                    <> · {incidentSpan(incident, now)} so far</>
                  )}
                  {incident.unobservedSince && (
                    <>
                      {" "}
                      · its reading has failed since{" "}
                      <time dateTime={incident.unobservedSince}>
                        {new Date(incident.unobservedSince).toLocaleTimeString()}
                      </time>
                      , so whether it is still true is unknown
                    </>
                  )}
                  {related.length > 0 && <> · began with {related.join("; ")}</>}
                </span>
              </li>
            )
          })}
        </ol>
      )}
    </div>
  )
}
