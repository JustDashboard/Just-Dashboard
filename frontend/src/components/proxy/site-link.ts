"use client"

import { useEffect, useEffectEvent } from "react"
import { useSearchParams } from "next/navigation"
import { forgetSessionState, useSessionState } from "@/lib/view-state"
import type { SiteSpec } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { useProxy } from "@/components/proxy/proxy-context"
import { BLANK, LINK_PARAMS, linkedSite } from "@/components/proxy/site-presets"
import type { DraftBase } from "@/components/proxy/site-draft"

/** Where a new site's draft is kept for the tab; an existing one's is keyed on its name. */
export const NEW_SITE_DRAFT = "proxy.site.form.new"

/**
 * Opens the form on a new site from a link elsewhere in the dashboard —
 * /proxy/sites?new=1&upstream=<url>&domain=<name>, from a port or a
 * container that has nothing in front of it yet.
 *
 * The query is read once and taken off the address, so a reload or the back
 * button does not open the form again over what was typed since; the draft
 * it starts is the form's own, kept for the tab like any other. It opens only
 * for an account that may write sites, on a host with nginx, and waits until
 * both are known.
 */
export function useNewSiteLink(open: () => void) {
  const params = useSearchParams()
  const { loading, can } = useAuth()
  const { status } = useProxy()
  const [, setSpec] = useSessionState<SiteSpec>(`${NEW_SITE_DRAFT}.spec`, BLANK)
  const [, setDomainText] = useSessionState(`${NEW_SITE_DRAFT}.domains`, "")
  const [, setUpstreamSet] = useSessionState(`${NEW_SITE_DRAFT}.upstreamSet`, false)
  // What the link filled in is where the draft starts, not an edit to it:
  // closing the form untouched does not ask about discarding it.
  const [, setBase] = useSessionState<DraftBase | null>(`${NEW_SITE_DRAFT}.base`, null)
  const asked = params.get("new") === "1"

  const follow = useEffectEvent(() => {
    const link = linkedSite(new URLSearchParams(window.location.search))
    const url = new URL(window.location.href)
    for (const key of LINK_PARAMS) url.searchParams.delete(key)
    window.history.replaceState(null, "", `${url.pathname}${url.search}${url.hash}`)
    if (!link || !can("system.admin") || !status?.nginx) return
    forgetSessionState("proxy.site.form.")
    setSpec(link.spec)
    setBase({ digest: "", spec: link.spec })
    setDomainText(link.domains)
    setUpstreamSet(link.upstreamGiven)
    open()
  })

  useEffect(() => {
    if (asked && !loading && status) follow()
  }, [asked, loading, status])
}
