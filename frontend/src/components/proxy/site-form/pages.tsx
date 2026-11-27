"use client"

import { useState } from "react"
import { notify } from "@/lib/toast"
import { get } from "@/lib/api"
import type {
  ErrorPageCode,
  Exposure,
  SiteBotBlock,
  SiteMaintenance,
  SitePage,
  SitePageName,
  SiteSpec,
} from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { FormNote, OptionList, OptionRow } from "@/components/form"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { PAGE_LABEL, PageEditor } from "@/components/proxy/page-editor"
import { Field, Part, ListField } from "@/components/proxy/site-form/fields"

const ERROR_CODES: ErrorPageCode[] = [404, 502, 503, 504]

const ERROR_HINT: Record<ErrorPageCode, string> = {
  404: "A path with nothing behind it.",
  502: "The application is down or starting. The page shipped for it retries every 5 seconds.",
  503: "The application says it cannot take requests.",
  504: "The application took longer than the timeout to answer.",
}

export function CrawlerSection({
  spec,
  set,
}: {
  spec: SiteSpec
  set: <K extends keyof SiteSpec>(key: K, value: SiteSpec[K]) => void
}) {
  const bots = spec.blockBots
  const setBots = (patch: Partial<SiteBotBlock>) => {
    const next = { ...bots, ...patch }
    set("blockBots", next.ai || next.scanners || next.custom?.length ? next : undefined)
  }
  const hotlink = spec.hotlink

  return (
    <Part
      title="Crawlers & hotlinking"
      hint="Refused with 403 by what a request says about itself. Anything can claim to be a browser, so this stops the honest ones."
    >
      <OptionList>
        <OptionRow
          title="Block AI crawlers"
          hint="GPTBot, ClaudeBot, CCBot, PerplexityBot, Bytespider, Amazonbot, meta-externalagent and the other AI crawlers and fetchers that name themselves."
          checked={bots?.ai ?? false}
          onCheckedChange={(v) => setBots({ ai: v || undefined })}
        />
        <OptionRow
          title="Block scanners"
          hint="sqlmap, Nikto, Nmap, masscan, zgrab, Nuclei, WPScan, DirBuster, gobuster and ffuf, as they announce themselves by default."
          checked={bots?.scanners ?? false}
          onCheckedChange={(v) => setBots({ scanners: v || undefined })}
        />
      </OptionList>
      <ListField
        id="site-bots"
        label="Also block these user agents"
        placeholder="SemrushBot"
        values={bots?.custom ?? []}
        onChange={(custom) => setBots({ custom: custom.length ? custom : undefined })}
        hint="Matched anywhere in the User-Agent, regardless of case."
      />
      {spec.kind !== "redirect" && (
        <>
          <OptionList>
            <OptionRow
              title="Hotlink protection"
              hint="Images, video and audio asked for from a page on another site get 403. A visit with no Referer, or one a firewall blanked, still gets the file."
              checked={Boolean(hotlink)}
              onCheckedChange={(on) => set("hotlink", on ? {} : undefined)}
            />
          </OptionList>
          {hotlink && (
            <ListField
              id="site-hotlink"
              label="Other sites that may embed them"
              placeholder="*.example.org"
              values={hotlink.allow ?? []}
              onChange={(allow) => set("hotlink", { allow: allow.length ? allow : undefined })}
              hint="The site's own names always may. A leading *. covers subdomains."
            />
          )}
        </>
      )}
    </Part>
  )
}

/** The pages served as files at a fixed address rather than on an error. */
const WELL_KNOWN_PAGES: { page: SitePageName; key: "securityTxt" | "robotsTxt"; hint: string }[] = [
  {
    page: "security",
    key: "securityTxt",
    hint: "Served at /.well-known/security.txt: where to report a vulnerability. A save writes one with a contact at the site's first domain and a year's Expires; check both.",
  },
  {
    page: "robots",
    key: "robotsTxt",
    hint: "Served at /robots.txt in place of whatever the application or folder has there.",
  },
]

/**
 * What is wrong with a security.txt's Expires, if anything. RFC 9116 tells
 * readers to ignore a file past it, so a lapsed one is as good as none.
 */
function securityTxtWarning(content: string, now: number): string | null {
  const line = content
    .split("\n")
    .map((l) => l.trim())
    .find((l) => /^expires:/i.test(l))
  if (!line) return "security.txt has no Expires line, which it needs to be read at all."
  const expires = Date.parse(line.slice("expires:".length).trim())
  if (Number.isNaN(expires)) return "security.txt's Expires is not a date."
  if (expires <= now) return "security.txt has expired, so researchers are told to ignore it."
  if (expires - now < 30 * 86_400_000) {
    return `security.txt expires on ${new Date(expires).toISOString().slice(0, 10)}. Move Expires on before then.`
  }
  return null
}

/**
 * The maintenance switch and the error pages. The pages themselves are files
 * beside the site's configuration, edited once the site exists; until then a
 * save gives it the ones the dashboard ships.
 */
export function PagesSection({
  spec,
  set,
  site,
}: {
  spec: SiteSpec
  set: <K extends keyof SiteSpec>(key: K, value: SiteSpec[K]) => void
  /** The saved site's name, when there is one to hold page files. */
  site: string | null
}) {
  const [editingPage, setEditingPage] = useState<SitePageName | null>(null)
  const [page, setPage] = useState<SitePageName>("maintenance")
  const [finding, setFinding] = useState(false)
  const maintenance = spec.maintenance
  const codes = spec.errorPages ?? []
  const setMaintenance = (patch: Partial<SiteMaintenance>) =>
    set("maintenance", { on: false, retryAfter: 300, bypassFrom: [], ...maintenance, ...patch })
  const addMyAddress = async () => {
    setFinding(true)
    try {
      const { client } = await get<Exposure>("/exposure")
      if (!client) {
        notify.error("The dashboard could not tell which address you come from")
        return
      }
      const bypass = maintenance?.bypassFrom ?? []
      if (!bypass.includes(client)) setMaintenance({ bypassFrom: [...bypass, client] })
    } catch (err) {
      notify.error("Could not read your address", err)
    } finally {
      setFinding(false)
    }
  }
  const wellKnown = spec.kind !== "redirect"
  const pages: SitePageName[] = [
    ...(maintenance ? (["maintenance"] as const) : []),
    ...codes.map((code) => String(code) as SitePageName),
    ...(wellKnown ? WELL_KNOWN_PAGES.filter((p) => spec[p.key]).map((p) => p.page) : []),
  ]
  const securityWarning = usePoll<string | null>(
    (signal) =>
      get<SitePage>(
        `/proxy/sites/${encodeURIComponent(site ?? "")}/pages/security`,
        undefined,
        signal,
      ).then((p) => securityTxtWarning(p.content, Date.now())),
    0,
    [site],
    { enabled: Boolean(site && wellKnown && spec.securityTxt) },
  )
  const edit = (name: SitePageName) => {
    setPage(name)
    setEditingPage(name)
  }

  return (
    <Part title="Pages & files">
      <OptionList>
        <OptionRow
          title="Maintenance"
          hint="Answers every visitor with the maintenance page and a 503, except the addresses below. Certificate renewals keep working."
          tone={maintenance?.on ? "warning" : "default"}
          checked={maintenance?.on ?? false}
          onCheckedChange={(on) => setMaintenance({ on })}
        />
      </OptionList>
      {maintenance && (
        <>
          <Field
            label="Retry after"
            htmlFor="site-retry-after"
            tip="Seconds clients and crawlers are told to wait. 0 sends no Retry-After."
          >
            <Input
              id="site-retry-after"
              value={String(maintenance.retryAfter ?? 0)}
              inputMode="numeric"
              onChange={(e) => setMaintenance({ retryAfter: Number(e.target.value) || 0 })}
              className="font-mono text-xs"
            />
          </Field>
          <ListField
            id="site-bypass"
            label="Let these addresses past"
            placeholder="203.0.113.7"
            values={maintenance.bypassFrom}
            onChange={(bypassFrom) => setMaintenance({ bypassFrom })}
            hint="They reach the site as usual while maintenance is on."
          />
          <div>
            <Button size="sm" variant="outline" onClick={addMyAddress} pending={finding}>
              Add my address
            </Button>
            <FormNote className="mt-1.5">
              The address this browser reaches the dashboard from, which is the one the site sees
              only when both are reached the same way.
            </FormNote>
          </div>
        </>
      )}
      <OptionList>
        {ERROR_CODES.map((code) => (
          <OptionRow
            key={code}
            title={`Own ${PAGE_LABEL[String(code) as SitePageName]} page`}
            hint={ERROR_HINT[code]}
            checked={codes.includes(code)}
            onCheckedChange={(on) =>
              set(
                "errorPages",
                ERROR_CODES.filter((c) => (c === code ? on : codes.includes(c))),
              )
            }
          />
        ))}
        {spec.kind === "proxy" && codes.length > 0 && (
          <OptionRow
            title="Replace the application's own error pages"
            hint="Without it only the errors nginx produces itself get these pages; with it, the application's responses with those codes do too."
            checked={spec.interceptErrors ?? false}
            disabled={Boolean(spec.forwardAuth) && !spec.interceptErrors}
            onCheckedChange={(v) => set("interceptErrors", v)}
          />
        )}
        {wellKnown &&
          WELL_KNOWN_PAGES.map(({ page: name, key, hint }) => (
            <OptionRow
              key={name}
              title={`Serve ${PAGE_LABEL[name]}`}
              hint={hint}
              checked={spec[key] ?? false}
              onCheckedChange={(v) => set(key, v || undefined)}
            />
          ))}
      </OptionList>
      {spec.securityTxt && securityWarning.data && (
        <FormNote tone="warning">{securityWarning.data}</FormNote>
      )}
      {pages.length > 0 &&
        (site ? (
          <div className="flex flex-wrap gap-2">
            {pages.map((name) => (
              <Button key={name} size="sm" variant="outline" onClick={() => edit(name)}>
                Edit {PAGE_LABEL[name]}
              </Button>
            ))}
          </div>
        ) : (
          <FormNote>
            A save gives the site the pages the dashboard ships. Edit them here once it is saved.
          </FormNote>
        ))}
      {site && (
        <PageEditor
          key={`${site}:${page}`}
          open={editingPage !== null}
          onOpenChange={(open) => {
            if (open) return
            setEditingPage(null)
            securityWarning.refresh()
          }}
          site={site}
          page={page}
        />
      )}
    </Part>
  )
}
