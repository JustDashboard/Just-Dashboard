"use client"

import { useEffect } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import { AddressSection } from "@/components/database/ops/settings-address"
import { ConnectionForm } from "@/components/database/ops/settings-connection"
import { SettingsFrame } from "@/components/database/ops/settings-frame"
import { useDatabase } from "@/components/database/shell/database-context"
import { formerSettingsPage, settingsPageHref } from "@/components/database/shell/settings-pages"

/**
 * General: the address the dashboard dials, drawn as the way it goes and
 * changed in its fields, then what it calls the connection and whether it may
 * change what is in it.
 *
 * It is also where an address from the one-page Settings lands: `#danger`,
 * `#reachability` or a parameter search (`?q=`) is sent on, in place, to the
 * page that holds it now, and `#databases` to Home, whose list of what else
 * the server holds is the one that section was.
 */
export function Settings() {
  const { href } = useDatabase()
  const router = useRouter()
  const search = useSearchParams()
  useEffect(() => {
    if (window.location.hash === "#databases") {
      router.replace(href("home"))
      return
    }
    const page = formerSettingsPage(window.location.hash, search)
    if (!page) return
    const query = page === "parameters" ? search.toString() : ""
    const to = settingsPageHref(href("settings"), { path: page })
    router.replace(query ? `${to}?${query}` : to)
  }, [href, router, search])
  return (
    <SettingsFrame page="general">
      {() => (
        <>
          <AddressSection />
          <ConnectionForm />
        </>
      )}
    </SettingsFrame>
  )
}
