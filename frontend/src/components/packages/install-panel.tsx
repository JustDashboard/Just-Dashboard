"use client"

import { useEffect, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import { Check, Download, Information, MagnifyingGlass } from "@/components/icons"
import { errorMessage, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import type { Job, PackageSearchResult } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { PackageMark } from "@/components/packages/marks"
import { ProductGlyph, ProductLogos } from "@/components/product-logo"
import { Panel, PanelBody, PanelToolbar } from "@/components/panel"
import { SearchInput } from "@/components/page"
import { ROW_BLEED } from "@/components/row-list"
import { EmptyState, Notice, Spinner } from "@/components/state"
import { Status } from "@/components/status-dot"
import { FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"

/**
 * Finding software, which is the half of a package manager a panel like this
 * usually skips.
 *
 * The list updates as you type. That is not decoration: the reason people open
 * a terminal instead of a package page is that they do not know the name — it
 * is `postgresql-client`, not `psql`; `build-essential`, not `gcc` — and a form
 * where you type a guess and press a button to find out you were wrong is a
 * form you use once. The debounce is what keeps that honest, and the ranking
 * is on the server so the exact name somebody typed is row one rather than row
 * four hundred.
 *
 * Install is one press, on the row. There was a tray here — pick several,
 * install them in one transaction — and the argument for it was real: three
 * separate runs is three chances to be interrupted halfway. It cost a click
 * and a concept on every single-package install, which is almost all of them,
 * and the run it protects against is a job that survives the tab anyway.
 *
 * Plain, like the two lists beside it under the strip: the tab is the block's
 * name, so it carries no header of its own — a search box, a hairline, and
 * the rows starting on the page's own edge. Each result is drawn as the
 * software it is (§14), so "postgres" answers with PostgreSQL's elephant
 * beside the three packages that are its and a glyph beside the rest.
 */

/** Long enough that a word is typed before anything is asked; short enough to feel live. */
const DEBOUNCE_MS = 220

/**
 * What an empty search offers to look for, drawn as the software it finds:
 * words rather than package names, because `docker` is `docker.io` on apt
 * and `docker` on the rest, and the search reads names and descriptions.
 */
const SUGGESTIONS: { query: string; product: string }[] = [
  { query: "nginx", product: "nginx" },
  { query: "postgresql", product: "postgresql" },
  { query: "redis", product: "redis" },
  { query: "docker", product: "docker" },
  { query: "nodejs", product: "nodejs" },
  { query: "python3", product: "python" },
  { query: "git", product: "git" },
  { query: "fail2ban", product: "fail2ban" },
]

export function InstallPanel({
  onJob,
  onInspect,
  manager,
}: {
  onJob: (job: Job) => void
  onInspect: (name: string) => void
  manager?: string
}) {
  const { can } = useAuth()
  const [query, setQuery] = useSessionState("packages.install.query", "")
  const [results, setResults] = useState<PackageSearchResult[]>([])
  const [searching, setSearching] = useState(false)
  const [error, setError] = useState("")
  /** The row whose install is being started, so only that button spins. */
  const [starting, setStarting] = useState("")
  /** Rows whose install has been handed to a job, so the button stops offering. */
  const [started, setStarted] = useState<string[]>([])

  const needle = query.trim()
  // Nothing is *cleared* when the box empties, and nothing is set before the
  // timer fires: what a query shorter than the floor should show is derived
  // below instead. An effect that reset four pieces of state on every
  // keystroke is a cascade of renders to say something the render already
  // knows.
  useEffect(() => {
    if (needle.length < 2) return
    const controller = new AbortController()
    const timer = setTimeout(() => {
      setSearching(true)
      get<PackageSearchResult[]>("/packages/search", { q: needle }, controller.signal)
        .then((found) => {
          setResults(found)
          setError("")
        })
        .catch((err) => {
          if (controller.signal.aborted) return
          setResults([])
          setError(errorMessage(err))
        })
        .finally(() => {
          if (!controller.signal.aborted) setSearching(false)
        })
    }, DEBOUNCE_MS)
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
  }, [needle])

  // What the last request answered belongs to the query that asked for it, so
  // a half-typed box shows nothing rather than the results of two letters ago.
  const live = needle.length >= 2
  const shown = live ? results : []
  const shownError = live ? error : ""

  const install = async (name: string) => {
    setStarting(name)
    try {
      const job = await post<Job>("/packages/install", { packages: [name] })
      setStarted((prev) => [...prev, name])
      onJob(job)
      notify.success(`Installing ${name}`, {
        description: "The output is at the top of this page.",
      })
    } catch (err) {
      notify.error(`Could not install ${name}`, err)
    } finally {
      setStarting("")
    }
  }

  const canInstall = can("system.admin")
  const typing = needle.length > 0 && !live

  return (
    <Panel plain>
      <PanelToolbar>
        <SearchInput
          containerClassName="sm:w-96"
          value={query}
          autoFocus
          onChange={(e) => setQuery(e.target.value)}
          placeholder="What do you need? nginx, htop, postgres client…"
          trailing={
            live && searching ? <Spinner className="mr-1.5 size-3.5 text-muted-foreground" /> : null
          }
        />
        {shown.length > 0 && (
          <span className="text-xs text-muted-foreground">
            <span className="numeric">{shown.length}</span> match{shown.length === 1 ? "" : "es"}
            {shown.length >= 60 ? " — the closest ones" : ""}
          </span>
        )}
      </PanelToolbar>

      <PanelBody flush>
        {shownError && (
          <Notice tone="warning" title="The search failed" className="mt-4">
            {shownError}
          </Notice>
        )}

        {!shownError && shown.length === 0 && (
          <EmptyState
            icon={MagnifyingGlass}
            mark={
              needle ? undefined : (
                <ProductLogos ids={["nginx", "postgresql", "docker"]} size="md" />
              )
            }
            action={
              !needle && (
                <div className="flex max-w-lg flex-wrap justify-center gap-1.5">
                  {SUGGESTIONS.map((suggestion) => (
                    <FilterChip key={suggestion.query} onClick={() => setQuery(suggestion.query)}>
                      <ProductGlyph id={suggestion.product} />
                      {suggestion.query}
                    </FilterChip>
                  ))}
                </div>
              )
            }
            className="mt-4"
            title={
              typing
                ? "Keep typing"
                : needle
                  ? "Nothing matches that"
                  : "Search for something to install"
            }
            description={
              typing
                ? "Two letters is the shortest search worth running."
                : needle
                  ? "Try a shorter or more general word — every package this host can reach is searched by name first, and by description when the name finds nothing."
                  : 'Names are matched first, so typing what you actually want puts it at the top. If you only know what the software does, type that instead — "web server", "password manager" — and the descriptions are searched too.'
            }
          />
        )}

        {shown.length > 0 && (
          <ul className="min-w-0 divide-y divide-hairline">
            {shown.map((result) => {
              const queued = started.includes(result.name)
              return (
                <li
                  key={result.name}
                  className={cn(
                    "group flex min-w-0 items-center gap-3 py-2.5 transition-colors hover:bg-row-hover",
                    ROW_BLEED,
                  )}
                >
                  <PackageMark name={result.name} />
                  <button
                    type="button"
                    onClick={() => onInspect(result.name)}
                    className="min-w-0 flex-1 space-y-0.5 text-left"
                  >
                    <span className="flex min-w-0 items-baseline gap-2">
                      <span className="truncate font-mono text-body font-medium group-hover:underline">
                        {result.name}
                      </span>
                      {result.version && (
                        <span className="numeric shrink-0 text-hint text-muted-foreground">
                          {result.version}
                        </span>
                      )}
                    </span>
                    <span className="block truncate text-xs text-muted-foreground">
                      {result.summary || "No description published"}
                    </span>
                  </button>

                  {/* The row's fixed properties and its state at the edge,
                      before its verbs — never against the name. */}
                  <span className="hidden shrink-0 items-center gap-3 sm:flex">
                    {result.repository && <Tag>{result.repository}</Tag>}
                    {result.installed && <Status verdict="ok" icon={Check} label="Installed" />}
                  </span>

                  <div className="flex shrink-0 items-center gap-1">
                    <IconAction
                      label="What is this, and what will it give me?"
                      onClick={() => onInspect(result.name)}
                    >
                      <Information />
                    </IconAction>
                    {canInstall &&
                      !result.installed &&
                      (queued ? (
                        <Status verdict="ok" icon={Check} label="Started" className="px-2" />
                      ) : (
                        <Button
                          size="sm"
                          variant="outline"
                          pending={starting === result.name}
                          disabled={Boolean(starting)}
                          title={`${manager ?? "apt"} install ${result.name}`}
                          onClick={() => void install(result.name)}
                        >
                          <Download className="size-4" />
                          Install
                        </Button>
                      ))}
                  </div>
                </li>
              )
            })}
          </ul>
        )}
      </PanelBody>
    </Panel>
  )
}
