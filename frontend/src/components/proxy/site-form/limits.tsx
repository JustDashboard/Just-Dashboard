"use client"

import { useState } from "react"
import { notify } from "@/lib/toast"
import { get, post } from "@/lib/api"
import type {
  CloudflareRanges,
  CloudflareRefresh,
  ProxyCache,
  SiteLimits,
  SiteSpec,
  StaticCache,
} from "@/lib/types"
import { plural, relativeTime } from "@/lib/format"
import { usePoll } from "@/hooks/use-poll"
import { FieldRow, FormNote, OptionList, OptionRow } from "@/components/form"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { Field, Part, ListField } from "@/components/proxy/site-form/fields"
import { RequestLimitFields } from "@/components/proxy/site-form/locations"

const REAL_IP_HEADERS = ["X-Forwarded-For", "X-Real-IP"]

/**
 * Where the visitor's address comes from behind Cloudflare or a load
 * balancer. Cloudflare's ranges are one shared file every such site
 * includes, so the refresh here updates all of them at once.
 */
export function RealIPSection({
  spec,
  set,
  open,
}: {
  spec: SiteSpec
  set: <K extends keyof SiteSpec>(key: K, value: SiteSpec[K]) => void
  open: boolean
}) {
  const realIp = spec.realIp
  const cloudflare = realIp?.source === "cloudflare"
  const ranges = usePoll<CloudflareRanges>(
    (signal) => get("/proxy/realip/cloudflare", undefined, signal),
    0,
    [],
    { enabled: open && cloudflare },
  )
  const [refreshing, setRefreshing] = useState(false)
  const refresh = async () => {
    setRefreshing(true)
    try {
      const res = await post<CloudflareRefresh>("/proxy/realip/cloudflare/refresh")
      if (res.reloadError) {
        notify.error("Ranges saved, but nginx did not reload", res.reloadError)
      } else {
        notify.success(`${plural(res.ranges.ranges.length, "Cloudflare range")} in use`)
      }
      ranges.refresh()
    } catch (err) {
      notify.error("Could not refresh Cloudflare's ranges", err)
    } finally {
      setRefreshing(false)
    }
  }
  const header = realIp?.header ?? "X-Forwarded-For"
  const rangeNote = (() => {
    const data = ranges.data
    if (!data) return null
    if (!data.source) return "Written from the built-in list on save, until the first refresh."
    const count = plural(data.ranges.length, "range")
    if (data.source === "built-in") {
      return `${count} from the built-in list, written ${relativeTime(data.fetched)}.`
    }
    return `${count} from cloudflare.com, fetched ${relativeTime(data.fetched)}.`
  })()

  return (
    <Part
      title="Visitor address"
      hint="Behind Cloudflare or a load balancer every request comes from the proxy. This puts the visitor's address back for the log, the address lists and the limits."
    >
      <OptionList>
        <OptionRow
          title="Behind a proxy"
          hint="The address is taken from a header, believed only from the proxy's own addresses."
          checked={Boolean(realIp)}
          onCheckedChange={(on) => set("realIp", on ? { source: "cloudflare" } : undefined)}
        >
          {realIp && (
            <ToggleGroup
              type="single"
              aria-label="Which proxy"
              value={realIp.source}
              onValueChange={(v) => {
                if (v === "cloudflare") set("realIp", { source: "cloudflare" })
                if (v === "proxies")
                  set("realIp", { source: "proxies", trusted: [], header: "X-Forwarded-For" })
              }}
              variant="outline"
              size="sm"
              className="w-full"
            >
              <ToggleGroupItem value="cloudflare" className="flex-1 text-hint">
                Cloudflare
              </ToggleGroupItem>
              <ToggleGroupItem value="proxies" className="flex-1 text-hint">
                My own proxies
              </ToggleGroupItem>
            </ToggleGroup>
          )}
        </OptionRow>
        {cloudflare && (
          <OptionRow
            title="Only Cloudflare may reach this site"
            hint="Closes every connection from outside Cloudflare's ranges without an answer. The domain has to be proxied through Cloudflare, certificate checks included."
            tone={realIp.cloudflareOnly ? "warning" : "default"}
            checked={realIp.cloudflareOnly ?? false}
            onCheckedChange={(cloudflareOnly) =>
              set("realIp", { source: "cloudflare", cloudflareOnly })
            }
          />
        )}
      </OptionList>
      {cloudflare && (
        <div className="flex items-center justify-between gap-3">
          <p className="text-hint text-muted-foreground">
            {rangeNote ?? "Reading Cloudflare's ranges…"}
          </p>
          <Button variant="outline" size="sm" disabled={refreshing} onClick={refresh}>
            {refreshing ? "Refreshing…" : "Refresh ranges"}
          </Button>
        </div>
      )}
      {realIp?.source === "proxies" && (
        <>
          <ListField
            id="site-realip-trusted"
            label="Believe the header from"
            placeholder="10.0.0.2"
            values={realIp.trusted ?? []}
            onChange={(trusted) => set("realIp", { ...realIp, trusted })}
            hint="The load balancer's addresses. The header from anyone else is ignored."
          />
          <Field
            label="Address header"
            htmlFor="site-realip-header"
            tip="With X-Forwarded-For the visitor is the last address that is not one of yours."
          >
            <Select value={header} onValueChange={(v) => set("realIp", { ...realIp, header: v })}>
              <SelectTrigger id="site-realip-header" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {[...new Set([...REAL_IP_HEADERS, header])].map((h) => (
                  <SelectItem key={h} value={h}>
                    {h}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        </>
      )}
    </Part>
  )
}

/**
 * Request rates and a connection cap, counted per client address. The
 * exemptions and log-only mode apply to every limit of the site, a path's own
 * included, so they are drawn whenever any of them is on.
 */
export function LimitsSection({
  spec,
  set,
}: {
  spec: SiteSpec
  set: <K extends keyof SiteSpec>(key: K, value: SiteSpec[K]) => void
}) {
  const limits = spec.limits
  const setLimits = (patch: Partial<SiteLimits>) =>
    set("limits", { exemptFrom: [], ...limits, ...patch })
  const pathLimited =
    spec.kind === "proxy" && spec.locations.some((loc) => loc.rateLimit !== undefined)
  const limited = Boolean(limits?.request) || (limits?.connPerIp ?? 0) > 0 || pathLimited

  return (
    <Part
      title="Limits"
      hint="How much one client address may ask of the site. Past a limit it gets a 429."
    >
      <OptionList>
        <OptionRow
          title="Limit the request rate"
          hint="Slows scrapers and password guessing. A path below can have a rate of its own."
          checked={Boolean(limits?.request)}
          onCheckedChange={(on) =>
            setLimits({ request: on ? { rate: "10r/s", burst: 20, noDelay: true } : undefined })
          }
        >
          {limits?.request && (
            <RequestLimitFields
              id="site-limit"
              value={limits.request}
              onChange={(request) => setLimits({ request })}
            />
          )}
        </OptionRow>
        <OptionRow
          title="Cap connections per address"
          hint="How many connections one address may hold open at once."
          checked={(limits?.connPerIp ?? 0) > 0}
          onCheckedChange={(on) => setLimits({ connPerIp: on ? 20 : 0 })}
        >
          {(limits?.connPerIp ?? 0) > 0 && (
            <Input
              aria-label="Connections per address"
              value={String(limits?.connPerIp ?? 0)}
              inputMode="numeric"
              onChange={(e) => setLimits({ connPerIp: Number(e.target.value) || 0 })}
              className="font-mono text-xs"
            />
          )}
        </OptionRow>
        {limited && (
          <OptionRow
            title="Log only"
            hint='Refuses nothing: what would have been refused is written to the error log as "dry run". For trying a limit on live traffic.'
            tone={limits?.dryRun ? "warning" : "default"}
            checked={limits?.dryRun ?? false}
            onCheckedChange={(dryRun) => setLimits({ dryRun })}
          />
        )}
      </OptionList>
      {limited && (
        <ListField
          id="site-limit-exempt"
          label="Never limit these addresses"
          placeholder="203.0.113.7"
          values={limits?.exemptFrom ?? []}
          onChange={(exemptFrom) => setLimits({ exemptFrom })}
          hint="A monitor, an office, another server of yours."
        />
      )}
    </Part>
  )
}

/**
 * What browsers and nginx keep. The proxy cache stores nothing nginx does not
 * buffer, so turning it on turns buffering on with it.
 */
export function CachingSection({
  spec,
  set,
}: {
  spec: SiteSpec
  set: <K extends keyof SiteSpec>(key: K, value: SiteSpec[K]) => void
}) {
  const asset = spec.staticCache
  const proxy = spec.kind === "proxy" ? spec.proxyCache : undefined
  const setAsset = (patch: Partial<StaticCache>) =>
    set("staticCache", { maxAge: "30d", ...asset, ...patch })
  const setProxy = (patch: Partial<ProxyCache>) =>
    set("proxyCache", { maxSize: "1g", valid: "10m", ...proxy, ...patch })

  return (
    <Part
      title="Caching"
      hint="Browsers keep static files, and nginx can keep the application's answers."
    >
      <OptionList>
        <OptionRow
          title="Cache static files in browsers"
          hint="Styles, scripts, images and fonts, chosen by their Content-Type, so pages are never kept."
          checked={Boolean(asset)}
          onCheckedChange={(on) => set("staticCache", on ? { maxAge: "30d" } : undefined)}
        >
          {asset && (
            <div className="space-y-2">
              <Field
                label="Kept for"
                htmlFor="site-cache-age"
                tip="A time in nginx's units: 12h, 30d, 1y."
              >
                <Input
                  id="site-cache-age"
                  value={asset.maxAge}
                  onChange={(e) => setAsset({ maxAge: e.target.value.trim() })}
                  placeholder="30d"
                  className="font-mono text-xs"
                />
              </Field>
              <label className="flex items-center gap-2 text-hint text-muted-foreground">
                <Checkbox
                  checked={asset.immutable ?? false}
                  onCheckedChange={(v) => setAsset({ immutable: Boolean(v) })}
                />
                Immutable: browsers do not ask again even on a reload. Only for file names that
                change with their content.
              </label>
            </div>
          )}
        </OptionRow>
        {spec.kind === "proxy" && (
          <OptionRow
            title="Cache the application's responses"
            hint="nginx answers repeat requests from disk and adds an X-Cache-Status header. Requests with an Authorization header always reach the application."
            checked={Boolean(proxy)}
            onCheckedChange={(on) => {
              if (on) set("buffering", true)
              set("proxyCache", on ? { maxSize: "1g", valid: "10m", serveStale: true } : undefined)
            }}
          >
            {proxy && (
              <div className="space-y-2">
                <FieldRow>
                  <Field label="Size on disk" htmlFor="site-cache-size" tip="512m, 2g.">
                    <Input
                      id="site-cache-size"
                      value={proxy.maxSize}
                      onChange={(e) => setProxy({ maxSize: e.target.value.trim() })}
                      placeholder="1g"
                      className="font-mono text-xs"
                    />
                  </Field>
                  <Field
                    label="Kept for"
                    htmlFor="site-cache-valid"
                    tip="When the application's own Cache-Control does not say."
                  >
                    <Input
                      id="site-cache-valid"
                      value={proxy.valid}
                      onChange={(e) => setProxy({ valid: e.target.value.trim() })}
                      placeholder="10m"
                      className="font-mono text-xs"
                    />
                  </Field>
                </FieldRow>
                <label className="flex items-center gap-2 text-hint text-muted-foreground">
                  <Checkbox
                    checked={proxy.serveStale ?? false}
                    onCheckedChange={(v) => setProxy({ serveStale: Boolean(v) })}
                  />
                  Serve the last copy while the application is down
                </label>
                <label className="flex items-center gap-2 text-hint text-muted-foreground">
                  <Checkbox
                    checked={proxy.cacheCookies ?? false}
                    onCheckedChange={(v) => setProxy({ cacheCookies: Boolean(v) })}
                  />
                  Cache requests with cookies too
                </label>
                {!spec.buffering && (
                  <FormNote tone="warning">
                    The cache needs Buffer responses on, under Behaviour.
                  </FormNote>
                )}
              </div>
            )}
          </OptionRow>
        )}
      </OptionList>
    </Part>
  )
}
