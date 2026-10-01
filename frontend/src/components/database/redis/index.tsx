"use client"

import { AreaStub } from "@/components/database/shell/area-stub"

/** The key browser: a namespace tree beside typed editors for each kind of value. */
export function RedisKeys() {
  return (
    <AreaStub section="data" area="redis">
      The key browser: a namespace tree, each key&apos;s type, size and expiry, and an editor for
      each kind of value.
    </AreaStub>
  )
}

/** The command console. */
export function RedisConsole() {
  return (
    <AreaStub section="query" area="redis">
      The console: commands run against the server, each classified before it is sent.
    </AreaStub>
  )
}

/** Memory, clients, the slow log and command statistics. */
export function RedisPerformance() {
  return (
    <AreaStub section="performance" area="redis">
      What the server is doing: memory by type and namespace, clients, the slow log and command
      statistics.
    </AreaStub>
  )
}
