"use client"

import { Suspense, useCallback, useEffect, useMemo, useState } from "react"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import { Box, FileText, ListOrdered, Play, RefreshClockwise } from "@/components/icons"
import { errorMessage, get } from "@/lib/api"
import { plural } from "@/lib/format"
import type { ProxyConfigFiles, ProxyEffectiveConfig } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useQuerySelection } from "@/hooks/use-query-selection"
import { useSessionState } from "@/lib/view-state"
import { Page, PageContext, PageState } from "@/components/page"
import { ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { tabClasses } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu, type Verb } from "@/components/verbs"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { useNow } from "@/components/deploy/vocabulary"
import { useProxy } from "@/components/proxy/proxy-context"
import { ConfigEditor, type EditorView } from "@/components/proxy/config-editor"
import { ConfigFilesView } from "@/components/proxy/config-files"
import { ConfigHistoryView } from "@/components/proxy/config-history"
import { ConfigPicture } from "@/components/proxy/config-picture"
import { EffectiveConfigView } from "@/components/proxy/config-search"
import { ConfigSettingsView } from "@/components/proxy/settings-panel"
import { ConfigLintView } from "@/components/proxy/lint-panel"
import { EngineSetup } from "@/components/proxy/engine-setup"
import { EmptyMark, OverviewEmpty } from "@/components/proxy/overview-empty"
import type { ConfigTest } from "@/components/proxy/test-result"
import { testedLabel, warningCount } from "@/components/proxy/config-test"
import {
  authorship,
  folderOf,
  includeMap,
  readCount,
  unreadCount,
} from "@/components/proxy/config-tree"

const VIEWS = [
  { key: "files", label: "Files" },
  { key: "effective", label: "What nginx loads" },
  { key: "settings", label: "Settings" },
  { key: "lint", label: "Lint" },
  { key: "history", label: "History" },
] as const

type View = (typeof VIEWS)[number]["key"]

function isView(value: string | null): value is View {
  return VIEWS.some((view) => view.key === value)
}

export default function ProxyConfigPage() {
  return (
    <Suspense>
      <ConfigurationPage />
    </Suspense>
  )
}

/**
 * nginx's configuration as files. It opens on what it is about — nginx's
 * directory, what it holds, what nginx reads of it and the last config test,
 * with Test config as the line's command — and then on the directory as
 * nginx walks it: the main file, the folders it includes, the folders their
 * files include, and the folders it reads nothing in. Under the picture is
 * every file by folder.
 *
 * An administrator has four more views, each in the address as `?tab=`:
 * what nginx loads (searchable), its server-wide settings as a settings
 * column with one save bar, the linter, and the history of every file the
 * dashboard changed. Every file opens in the config editor, whose History
 * view holds its revisions and restore; a reader's opens read-only.
 *
 * It opened on a lone outline button over four tiles that restated the
 * filter chips, its views were buttons held in session state, and a file's
 * history was a second sheet.
 */
function ConfigurationPage() {
  const { status, error: statusError, refresh: refreshStatus, configTest } = useProxy()
  const admin = useAuth().can("system.admin")
  const nginx = Boolean(status?.nginx)
  const router = useRouter()
  const params = useSearchParams()

  const requested = params.get("tab")
  const tab: View = admin && isView(requested) ? requested : "files"
  const setTab = useCallback((next: string) => {
    // Replaced rather than pushed: a view is not a place Back steps through.
    const url = new URL(window.location.href)
    if (next === "files") url.searchParams.delete("tab")
    else url.searchParams.set("tab", next)
    window.history.replaceState(null, "", `${url.pathname}${url.search}${url.hash}`)
  }, [])

  // The old address of a file's history (`?history=`, which a site's
  // History verb and older links carry) opened a sheet of its own. It is the
  // file's History view now, over the History tab.
  useEffect(() => {
    const historyOf = params.get("history")
    if (!historyOf) return
    const url = new URL(window.location.href)
    url.searchParams.delete("history")
    url.searchParams.set("file", historyOf)
    url.searchParams.set("view", "history")
    url.searchParams.set("tab", "history")
    window.history.replaceState(null, "", `${url.pathname}${url.search}${url.hash}`)
  }, [params])

  const files = usePoll(
    (signal) => get<ProxyConfigFiles>("/proxy/files", undefined, signal),
    60_000,
    [],
    { enabled: nginx },
  )
  // Read when its view is shown, and again on Read again or after a save:
  // nginx -T runs the host's binary, which a poll has no reason to repeat.
  const effective = usePoll(
    (signal) => get<ProxyEffectiveConfig>("/proxy/effective", undefined, signal),
    0,
    [],
    { enabled: admin && nginx && tab === "effective" },
  )
  const changed = () => {
    files.refresh()
    effective.refresh()
    configTest.refreshLast()
  }

  // The file open in the editor is in the address bar, so a link can name
  // one and Back closes it; the line it opened at is kept beside it, and the
  // view it is on (its file or its history) too.
  const [selected, select] = useQuerySelection("file")
  const [line, setLine] = useState<number | undefined>(
    () => Number(params.get("line")) || undefined,
  )
  const fileView: EditorView = params.get("view") === "history" ? "history" : "file"
  const openFile = useCallback(
    (path: string, at?: number, view: EditorView = "file") => {
      setLine(at)
      const url = new URL(window.location.href)
      if (view === "history") url.searchParams.set("view", "history")
      else url.searchParams.delete("view")
      if (!at) url.searchParams.delete("line")
      window.history.replaceState(null, "", `${url.pathname}${url.search}${url.hash}`)
      select(path)
    },
    [select],
  )
  const closeFile = () => {
    select(null)
    const url = new URL(window.location.href)
    if (url.searchParams.has("line") || url.searchParams.has("view")) {
      url.searchParams.delete("line")
      url.searchParams.delete("view")
      window.history.replaceState(null, "", `${url.pathname}${url.search}${url.hash}`)
    }
  }
  const setFileView = (view: EditorView) => {
    const url = new URL(window.location.href)
    if (view === "history") url.searchParams.set("view", "history")
    else url.searchParams.delete("view")
    window.history.replaceState(null, "", `${url.pathname}${url.search}${url.hash}`)
  }

  // The list's search, shared with the picture: pressing a folder narrows
  // the list to it.
  const [query, setQuery] = useSessionState("proxy.config.query", "")

  const header = (
    <PageContext
      eyebrow={
        <Link href="/proxy" className="rounded-sm focus-ring hover:underline">
          Proxy & TLS
        </Link>
      }
      title="Configuration"
    />
  )

  if (!status) {
    if (statusError) {
      return (
        <PageState
          eyebrow="Proxy"
          title="Configuration"
          error={statusError}
          onRetry={refreshStatus}
        />
      )
    }
    return (
      <Page>
        {header}
        <ConfigIdentity loading admin={admin} test={configTest} />
        <LoadingRows rows={6} />
      </Page>
    )
  }

  if (!nginx) {
    return (
      <Page className="animate-rise">
        {header}
        {status.caddy ? (
          <OverviewEmpty
            mark={<EmptyMark product="caddy" corner={FileText} />}
            title="This server's proxy is Caddy"
            action={
              <Button asChild size="sm" variant="outline">
                <Link href="/proxy/sites">Open Sites</Link>
              </Button>
            }
          >
            Its one Caddyfile opens from Sites; this page reads nginx&apos;s directory.
          </OverviewEmpty>
        ) : status.ingressContainer || status.ingressState ? (
          <OverviewEmpty
            mark={<EmptyMark product="docker" corner={FileText} />}
            title="Deployments configure the Caddy ingress"
            action={
              <Button asChild size="sm" variant="outline">
                <Link href="/deploy">Deployments</Link>
              </Button>
            }
          >
            It has no files on this server to read; each project&apos;s Domains settings write it.
          </OverviewEmpty>
        ) : (
          <EngineSetup status={status} admin={admin} onInstalled={refreshStatus} />
        )}
      </Page>
    )
  }

  const tree = files.error ? undefined : files.data
  return (
    <Page>
      {header}
      <ConfigIdentity
        root={status.nginxDir}
        tree={tree}
        error={files.error}
        admin={admin}
        test={configTest}
        onReadAgain={changed}
        onOpenMain={() => tree && openFile(tree.main)}
        onShowEngine={() => router.push("/proxy")}
      />

      <Tabs value={tab} onValueChange={setTab} className="min-w-0 gap-0">
        {admin && (
          <TabsPrimitive.List
            aria-label="Configuration views"
            className="flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
          >
            {VIEWS.map((view) => (
              <TabsPrimitive.Trigger
                key={view.key}
                value={view.key}
                className={tabClasses(tab === view.key, "h-10")}
              >
                {view.label}
              </TabsPrimitive.Trigger>
            ))}
          </TabsPrimitive.List>
        )}

        <TabsContent value="files" className="min-w-0 space-y-6 pt-6">
          {files.error ? (
            <ErrorState error={files.error} onRetry={files.refresh} />
          ) : !tree ? (
            <LoadingRows rows={6} />
          ) : (
            <>
              <FilesPicture
                tree={tree}
                onFolder={(folder) => setQuery(folder ? `${folder}/` : "")}
                onOpenMain={() => openFile(tree.main)}
              />
              <ConfigFilesView
                tree={tree}
                admin={admin}
                query={query}
                onQueryChange={setQuery}
                onOpen={openFile}
              />
            </>
          )}
        </TabsContent>
        {admin && (
          <>
            <TabsContent value="effective" className="min-w-0 pt-6">
              <EffectiveConfigView effective={effective} root={status.nginxDir} onOpen={openFile} />
            </TabsContent>
            <TabsContent value="settings" className="min-w-0 pt-8">
              <ConfigSettingsView root={status.nginxDir} onOpen={openFile} onChanged={changed} />
            </TabsContent>
            <TabsContent value="lint" className="min-w-0 pt-6">
              <ConfigLintView root={status.nginxDir} onOpen={openFile} />
            </TabsContent>
            <TabsContent value="history" className="min-w-0 pt-6">
              <ConfigHistoryView
                root={status.nginxDir}
                onOpen={(path) => openFile(path, undefined, "history")}
              />
            </TabsContent>
          </>
        )}
      </Tabs>

      <ConfigEditor
        open={selected !== null}
        onOpenChange={(open) => !open && closeFile()}
        path={selected ?? ""}
        kind="nginx"
        title={selected?.split("/").pop() ?? "Configuration"}
        readOnly={!admin}
        initialLine={line}
        history={admin ? { root: status.nginxDir, onRestored: changed } : undefined}
        view={fileView}
        onViewChange={setFileView}
        onSaved={changed}
      />
    </Page>
  )
}

/** The picture of the directory, worked out once per answer. */
function FilesPicture({
  tree,
  onFolder,
  onOpenMain,
}: {
  tree: ProxyConfigFiles
  onFolder: (folder: string) => void
  onOpenMain: () => void
}) {
  const map = useMemo(() => includeMap(tree.files, tree.root, tree.main), [tree])
  if (map.reached.length === 0 && map.unread.length === 0) return null
  return (
    <ConfigPicture
      map={map}
      root={tree.root}
      main={tree.main}
      hint="the main file"
      onFolder={onFolder}
      onOpenMain={onOpenMain}
    />
  )
}

/**
 * What the page is about, as the line every overhauled page opens on: nginx's
 * mark, its directory, then what the directory holds, what nginx reads of
 * it, what the dashboard wrote and — for an administrator — the last config
 * test, with Test config as the line's one command. While the list is read
 * the line is drawn at its final height with the facts still coming.
 */
function ConfigIdentity({
  root,
  tree,
  error,
  loading,
  admin,
  test,
  onReadAgain,
  onOpenMain,
  onShowEngine,
}: {
  /** The directory the status names, so the line has its title before the list lands. */
  root?: string
  tree?: ProxyConfigFiles
  error?: Error
  loading?: boolean
  admin: boolean
  test: ConfigTest
  onReadAgain?: () => void
  onOpenMain?: () => void
  onShowEngine?: () => void
}) {
  const now = useNow(10_000)
  const counts = useMemo(() => {
    if (!tree) return undefined
    return {
      files: tree.files.length,
      folders: new Set(tree.files.map((f) => folderOf(f.path, tree.root))).size,
      read: readCount(tree.files),
      unread: unreadCount(tree.files),
      ...authorship(tree.files),
    }
  }, [tree])

  const verbs: Verb[] = []
  if (onReadAgain) {
    verbs.push({ key: "read", label: "Read again", icon: RefreshClockwise, run: onReadAgain })
  }
  if (tree && onOpenMain) {
    verbs.push({
      key: "main",
      label: `${admin ? "Edit" : "View"} ${tree.main.split("/").pop()}`,
      icon: FileText,
      run: onOpenMain,
    })
  }
  if (admin && test.last) {
    verbs.push({ key: "last", label: "Show the last test", icon: ListOrdered, run: test.showLast })
  }
  if (onShowEngine) {
    verbs.push({ key: "engine", label: "Engine overview", icon: Box, run: onShowEngine })
  }

  return (
    <HostIdentity
      mark="nginx"
      title={<span className="font-mono">{tree?.root ?? root ?? "nginx configuration"}</span>}
      facts={
        error ? (
          <span className="font-medium text-warning" title={errorMessage(error)}>
            couldn&apos;t read the directory
          </span>
        ) : !counts || !tree || loading ? (
          <TextShimmer>Reading the directory…</TextShimmer>
        ) : (
          <>
            <span title={`${counts.files} files in ${plural(counts.folders, "folder")}`}>
              <span className="numeric font-medium text-foreground">{counts.files}</span>{" "}
              {counts.files === 1 ? "file" : "files"} in {plural(counts.folders, "folder")}
            </span>
            <FactDot />
            <HostFact product="nginx">
              <span className="numeric font-medium text-foreground">{counts.read}</span> read
              {!tree.includesKnown ? (
                <span className="font-medium text-warning">, not every include followed</span>
              ) : counts.unread > 0 ? (
                `, ${counts.unread} not`
              ) : (
                ", every file"
              )}
            </HostFact>
            <FactDot />
            <span title={`${counts.hand} written by hand`}>
              <span className="numeric font-medium text-foreground">{counts.managed}</span> by the
              dashboard
            </span>
            {admin && (
              <>
                <FactDot />
                <TestFact test={test} now={now} />
              </>
            )}
          </>
        )
      }
      aside={
        <div className="flex flex-wrap items-center gap-2">
          {admin && (
            <Button
              size="sm"
              onClick={test.run}
              pending={test.running}
              disabled={loading || test.running}
            >
              <Play />
              {test.running ? "Testing…" : "Test config"}
            </Button>
          )}
          {verbs.length > 0 && <VerbMenu verbs={verbs} label="More configuration actions" />}
        </div>
      }
    />
  )
}

/** The engine's last config test in a few words, coloured by its verdict. */
function TestFact({ test, now }: { test: ConfigTest; now: number }) {
  if (test.running) return <Status state="activating" label="testing…" />
  if (test.lastError) {
    return (
      <span className="font-medium text-warning" title={errorMessage(test.lastError)}>
        couldn&apos;t read the last test
      </span>
    )
  }
  if (!test.lastRead) return <TextShimmer>reading the last test…</TextShimmer>
  const last = test.last
  if (!last) return <span>no test since the dashboard started</span>
  const at = Date.parse(last.checkedAt)
  const when = testedLabel(at, Math.max(now, at))
  const warnings = warningCount(last.validation)
  if (!last.validation.valid) return <Status tone="danger" label={`test fails · ${when}`} />
  if (warnings > 0) {
    return <Status tone="warning" label={`passes with ${plural(warnings, "warning")} · ${when}`} />
  }
  return <Status tone="running" label={`test passes · ${when}`} />
}
