"use client"

import { AreaStub } from "@/components/database/shell/area-stub"

export { Logs } from "@/components/database/ops/logs"

/** Sessions, locks, statements, table statistics and maintenance on a SQL server. */
export function SqlPerformance() {
  return (
    <AreaStub section="performance" area="ops">
      What the server is doing: sessions, locks, the statements that cost the most, table statistics
      and maintenance.
    </AreaStub>
  )
}

/** Findings with the statement that fixes each. */
export function Advisor() {
  return (
    <AreaStub section="advisor" area="ops">
      What is worth fixing, each finding with the statement that fixes it.
    </AreaStub>
  )
}

/** Roles, users and grants. */
export function Access() {
  return (
    <AreaStub section="access" area="ops">
      Who can sign in and what each may do: roles, their attributes and their grants.
    </AreaStub>
  )
}

/** Dumps, restore and the schedule. */
export function Backups() {
  return (
    <AreaStub section="backups" area="ops">
      Dumps of this database: take one, restore one, and the schedule that takes them.
    </AreaStub>
  )
}

/** Connection, reachability, server parameters, extensions and the danger zone. */
export function Settings() {
  return (
    <AreaStub section="settings" area="ops">
      The connection itself: how it is dialled, where it is reachable from, the server&apos;s
      parameters, and removing it.
    </AreaStub>
  )
}
