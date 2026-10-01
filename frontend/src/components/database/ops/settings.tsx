"use client"

import { AreaStub } from "@/components/database/shell/area-stub"

/** Connection, reachability, server parameters, extensions and the danger zone. */
export function Settings() {
  return (
    <AreaStub section="settings" area="ops">
      The connection itself: how it is dialled, where it is reachable from, the server&apos;s
      parameters, and removing it.
    </AreaStub>
  )
}
