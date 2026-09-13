"use client"

import { useEffect, useState } from "react"
import { Check, Download, Information, MagnifyingGlass } from "@/components/icons"
import { errorMessage, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import type { Job, PackageSearchResult } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { SearchInput } from "@/components/page"
import { EmptyState, Notice, Spinner } from "@/components/state"
import { Status } from "@/components/status-dot"
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
 */

/** Long enough that a word is typed before anything is asked; short enough to feel live. */
const DEBOUNCE_MS = 220

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
  const [query, setQuery] = useState("")
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
    <Panel>
      <PanelHeader title="Add software" />
      <PanelToolbar>
        <div className="relative w-full sm:w-96">
          <SearchInput
            containerClassName="w-full sm:w-96"
            value={query}
            autoFocus
            onChange={(e) => setQuery(e.target.value)}
            placeholder="What do you need? nginx, htop, postgres client…"
          />
          {live && searching && (
            <Spinner className="absolute top-1/2 right-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
          )}
        </div>
        {shown.length > 0 && (
          <span className="text-xs text-muted-foreground">
            {shown.length} match{shown.length === 1 ? "" : "es"}
            {shown.length >= 60 ? " — the closest ones" : ""}
          </span>
        )}
      </PanelToolbar>

      <PanelBody flush>
        {shownError && (
          <div className="p-4">
            <Notice tone="warning" title="The search failed">
              {shownError}
            </Notice>
          </div>
        )}

        {!shownError && shown.length === 0 && (
          <EmptyState
            icon={MagnifyingGlass}
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
          <ul className="divide-y divide-hairline">
            {shown.map((result) => {
              const queued = started.includes(result.name)
              return (
                <li
                  key={result.name}
                  className={cn(
                    "flex min-w-0 items-start gap-3 px-4 py-2.5 transition-colors hover:bg-row-hover",
                    queued && "bg-wash-primary",
                  )}
                >
                  <button
                    type="button"
                    onClick={() => onInspect(result.name)}
                    className="min-w-0 flex-1 space-y-0.5 text-left"
                  >
                    <span className="flex min-w-0 flex-wrap items-center gap-2">
                      <span className="truncate font-mono text-body font-medium">
                        {result.name}
                      </span>
                      {result.version && (
                        <span className="numeric text-hint text-muted-foreground">
                          {result.version}
                        </span>
                      )}
                      {result.installed && <Status verdict="ok" icon={Check} label="Installed" />}
                      {result.repository && <Tag>{result.repository}</Tag>}
                    </span>
                    <span className="block truncate text-xs text-muted-foreground">
                      {result.summary || "No description published"}
                    </span>
                  </button>

                  <div className="flex shrink-0 items-center gap-1">
                    <IconAction
                      label="What is this, and what will it give me?"
                      onClick={() => onInspect(result.name)}
                    >
                      <Information />
                    </IconAction>
                    {canInstall && !result.installed && (
                      <Button
                        size="sm"
                        variant={queued ? "secondary" : "default"}
                        disabled={Boolean(starting) || queued}
                        title={`${manager ?? "apt"} install ${result.name}`}
                        onClick={() => void install(result.name)}
                      >
                        {starting === result.name ? (
                          <Spinner className="size-4" />
                        ) : queued ? (
                          <Check className="size-4" />
                        ) : (
                          <Download className="size-4" />
                        )}
                        {queued ? "Started" : "Install"}
                      </Button>
                    )}
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
