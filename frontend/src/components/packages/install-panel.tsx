"use client"

import { useEffect, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import { Check, Download, MagnifyingGlass } from "@/components/icons"
import { errorMessage, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { Job, PackageSearchResult } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { PackageMark } from "@/components/packages/marks"
import { ProductLogo } from "@/components/product-logo"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Panel, PanelBody, PanelToolbar } from "@/components/panel"
import { SearchInput } from "@/components/page"
import { EmptyState, Notice, Spinner } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
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
 * choices starting on the page's own edge. The suggested products and results
 * carry the lit edge of things you take; each result is drawn as the
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
const SUGGESTIONS: { query: string; product: string; use: string }[] = [
  { query: "nginx", product: "nginx", use: "Web server" },
  { query: "postgresql", product: "postgresql", use: "Relational database" },
  { query: "redis", product: "redis", use: "Cache & queues" },
  { query: "docker", product: "docker", use: "Container engine" },
  { query: "nodejs", product: "nodejs", use: "JavaScript runtime" },
  { query: "python3", product: "python", use: "Python runtime" },
  { query: "git", product: "git", use: "Version control" },
  { query: "fail2ban", product: "fail2ban", use: "Intrusion prevention" },
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
  const [answer, setAnswer] = useState<{
    query: string
    results: PackageSearchResult[]
    error?: string
  }>({ query: "", results: [] })
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
      get<PackageSearchResult[]>("/packages/search", { q: needle }, controller.signal)
        .then((found) => {
          if (!controller.signal.aborted) setAnswer({ query: needle, results: found })
        })
        .catch((err) => {
          if (controller.signal.aborted) return
          setAnswer({ query: needle, results: [], error: errorMessage(err) })
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
  const settled = answer.query === needle
  const shown = live && settled ? answer.results : []
  const shownError = live && settled ? answer.error : ""
  const searching = live && !settled

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
          aria-label="Search available packages"
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

        {!needle && (
          <div className="py-5">
            <ChoiceGrid columns={2} className="lg:grid-cols-4">
              {SUGGESTIONS.map((suggestion) => (
                <ChoiceCard
                  key={suggestion.query}
                  onClick={() => setQuery(suggestion.query)}
                  aria-label={suggestion.query}
                  className="min-h-0 flex-row items-center gap-3 py-3"
                >
                  <ProductLogo id={suggestion.product} size="sm" />
                  <span className="min-w-0">
                    <span className="block truncate text-body font-medium">{suggestion.query}</span>
                    <span className="block text-hint text-muted-foreground">{suggestion.use}</span>
                  </span>
                </ChoiceCard>
              ))}
            </ChoiceGrid>
          </div>
        )}
        {searching && (
          <div
            role="status"
            className="flex items-center gap-2 py-8 text-body text-muted-foreground"
          >
            <Spinner className="size-4" />
            Searching the repositories…
          </div>
        )}
        {!shownError && needle && !searching && shown.length === 0 && (
          <EmptyState
            icon={MagnifyingGlass}
            className="mt-4"
            title={typing ? "Keep typing" : "Nothing matches that"}
            description={
              typing
                ? "Type at least two letters to search."
                : "Try a shorter name or describe what the software does."
            }
          />
        )}

        {shown.length > 0 && (
          <ChoiceList className="pt-3">
            {shown.map((result) => {
              const queued = started.includes(result.name)
              return (
                <ChoiceRow
                  key={result.name}
                  workspaceItem={{ id: result.name, name: result.name }}
                  leading={<PackageMark name={result.name} />}
                  title={<span className="font-mono">{result.name}</span>}
                  verb={result.name}
                  description={result.summary || "No description published"}
                  onSelect={() => onInspect(result.name)}
                  trailing={
                    <span className="hidden min-w-0 items-center gap-3 sm:flex">
                      {result.version && (
                        <span
                          className="max-w-40 truncate font-mono text-hint text-muted-foreground"
                          title={result.version}
                        >
                          {result.version}
                        </span>
                      )}
                      {result.repository && <Tag>{result.repository}</Tag>}
                      {result.installed && <Status verdict="ok" icon={Check} label="Installed" />}
                    </span>
                  }
                  actions={
                    canInstall && !result.installed ? (
                      queued ? (
                        <Status verdict="ok" icon={Check} label="Started" className="px-2" />
                      ) : (
                        <Button
                          size="sm"
                          variant="outline"
                          pending={starting === result.name}
                          disabled={Boolean(starting)}
                          title={`${manager ?? "Package manager"} install ${result.name}`}
                          onClick={() => void install(result.name)}
                        >
                          <Download className="size-4" />
                          Install
                        </Button>
                      )
                    ) : undefined
                  }
                />
              )
            })}
          </ChoiceList>
        )}
      </PanelBody>
    </Panel>
  )
}
