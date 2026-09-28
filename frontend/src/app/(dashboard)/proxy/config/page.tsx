"use client"

import { Suspense, useMemo, useState } from "react"
import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { FileText } from "@/components/icons"
import { errorMessage, get } from "@/lib/api"
import type { ProxyConfigFiles, ProxyEffectiveConfig } from "@/lib/types"
import { useSessionState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useQuerySelection } from "@/hooks/use-query-selection"
import { Page, PageContext, PageState } from "@/components/page"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { tabClasses } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { useNow } from "@/components/deploy/vocabulary"
import { useProxy } from "@/components/proxy/proxy-context"
import { ConfigEditor } from "@/components/proxy/config-editor"
import { ConfigFilesView } from "@/components/proxy/config-files"
import { ConfigHistorySheet, ConfigHistoryView } from "@/components/proxy/config-history"
import { EffectiveConfigView } from "@/components/proxy/config-search"
import { ConfigSettingsView } from "@/components/proxy/settings-panel"
import { ConfigLintView } from "@/components/proxy/lint-panel"
import { useConfigTest } from "@/components/proxy/test-result"
import { testedLabel, warningCount } from "@/components/proxy/config-test"
import { authorship, folderOf, readCount, unreadCount } from "@/components/proxy/config-tree"
import { plural } from "@/lib/format"

type View = "files" | "effective" | "settings" | "lint" | "history"

export default function ProxyConfigPage() {
  return (
    <Suspense>
      <ConfigurationPage />
    </Suspense>
  )
}

/**
 * nginx's configuration as files: the directory as a tree, each file saying
 * whether nginx reads it and whether the dashboard wrote it, and — for an
 * administrator — what nginx actually loads, searchable by text, pattern or
 * directive. Every file opens in the config editor, which tests before it
 * writes; a reader's opens read-only.
 */
function ConfigurationPage() {
  const { status, error: statusError, refresh: refreshStatus } = useProxy()
  const admin = useAuth().can("system.admin")
  const nginx = Boolean(status?.nginx)
  const [view, setView] = useSessionState<View>("proxy.config.view", "files")
  const shownView: View = admin ? view : "files"

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
    { enabled: admin && nginx && shownView === "effective" },
  )
  const configTest = useConfigTest({ status, admin })

  // The file open in the editor is in the address bar, so a link can name
  // one and Back closes it; the line it opened at is kept beside it.
  const [selected, select] = useQuerySelection("file")
  // A file's history is in the address bar too, so a site's History verb
  // can link straight to it.
  const [historyOf, showHistory] = useQuerySelection("history")
  const params = useSearchParams()
  const [line, setLine] = useState<number | undefined>(
    () => Number(params.get("line")) || undefined,
  )
  const openFile = (path: string, at?: number) => {
    setLine(at)
    select(path)
  }
  const closeFile = () => {
    select(null)
    const url = new URL(window.location.href)
    if (url.searchParams.has("line")) {
      url.searchParams.delete("line")
      window.history.replaceState(null, "", `${url.pathname}${url.search}${url.hash}`)
    }
  }

  if (!status) {
    return (
      <PageState
        eyebrow="Proxy"
        title="Configuration"
        error={statusError}
        onRetry={refreshStatus}
      />
    )
  }

  const header = (
    <PageContext
      eyebrow="Proxy"
      title="Configuration"
      actions={
        admin &&
        nginx && (
          <Button size="sm" variant="outline" onClick={configTest.run} pending={configTest.running}>
            {configTest.running ? "Testing…" : "Test config"}
          </Button>
        )
      }
    />
  )

  if (!nginx) {
    return (
      <Page className="animate-rise">
        {header}
        <EmptyState
          icon={FileText}
          title="No nginx on this host"
          description={
            status.caddy
              ? "This host's proxy is Caddy, whose one Caddyfile opens from Sites."
              : status.ingressContainer || status.ingressState
                ? "The Docker Caddy ingress is configured by deployments, not by files on this host."
                : "Neither nginx nor Caddy was found on this host."
          }
          action={
            status.caddy && (
              <Button asChild size="sm" variant="outline">
                <Link href="/proxy/sites">Open Sites</Link>
              </Button>
            )
          }
        />
      </Page>
    )
  }

  const tree = files.data
  return (
    <Page className="animate-rise">
      {header}

      <Readings
        tree={files.error ? undefined : tree}
        error={files.error}
        loading={files.loading}
        admin={admin}
        test={configTest}
      />

      {admin && (
        <nav aria-label="Configuration views" className="flex gap-1 border-b border-hairline">
          {(
            [
              ["files", "Files"],
              ["effective", "What nginx loads"],
              ["settings", "Settings"],
              ["lint", "Lint"],
              ["history", "History"],
            ] as const
          ).map(([key, label]) => (
            <button
              key={key}
              type="button"
              aria-pressed={shownView === key}
              onClick={() => setView(key)}
              className={tabClasses(shownView === key, "h-10")}
            >
              {label}
            </button>
          ))}
        </nav>
      )}

      {shownView === "effective" ? (
        <EffectiveConfigView effective={effective} root={status.nginxDir} onOpen={openFile} />
      ) : shownView === "settings" ? (
        <ConfigSettingsView
          root={status.nginxDir}
          onOpen={openFile}
          onChanged={() => {
            files.refresh()
            effective.refresh()
            configTest.refreshLast()
          }}
        />
      ) : shownView === "lint" ? (
        <ConfigLintView root={status.nginxDir} onOpen={openFile} />
      ) : shownView === "history" ? (
        <ConfigHistoryView root={status.nginxDir} onOpen={showHistory} />
      ) : files.error ? (
        <ErrorState error={files.error} onRetry={files.refresh} />
      ) : !tree ? (
        <LoadingPanel />
      ) : (
        <ConfigFilesView tree={tree} admin={admin} onOpen={openFile} />
      )}

      <ConfigEditor
        open={selected !== null}
        onOpenChange={(open) => !open && closeFile()}
        path={selected ?? ""}
        kind="nginx"
        title={selected?.split("/").pop() ?? "Configuration"}
        readOnly={!admin}
        initialLine={line}
        onSaved={() => {
          files.refresh()
          effective.refresh()
          configTest.refreshLast()
        }}
      />
      {admin && (
        <ConfigHistorySheet
          path={historyOf}
          root={status.nginxDir}
          onOpenChange={(open) => !open && showHistory(null)}
          onRestored={() => {
            files.refresh()
            effective.refresh()
            configTest.refreshLast()
          }}
        />
      )}
      {configTest.panel}
    </Page>
  )
}

/**
 * The four readings: how many files, how many nginx reads, how many the
 * dashboard wrote, and the engine's last config test. A list that could not
 * be read says so on its tiles rather than counting nothing.
 */
function Readings({
  tree,
  error,
  loading,
  admin,
  test,
}: {
  tree: ProxyConfigFiles | undefined
  error: Error | undefined
  loading: boolean
  admin: boolean
  test: ReturnType<typeof useConfigTest>
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
  const unreadHint = error ? <span title={errorMessage(error)}>{"couldn't read"}</span> : undefined
  const figure = (value: number | undefined) =>
    loading ? <Skeleton className="my-1.5 h-5 w-12" /> : (value ?? "—")
  const last = test.last
  const lastWarnings = last ? warningCount(last.validation) : 0
  const tested =
    last && testedLabel(Date.parse(last.checkedAt), Math.max(now, Date.parse(last.checkedAt)))

  return (
    <StatGrid columns={4} dense>
      <StatTile
        label="Files"
        value={figure(counts?.files)}
        hint={unreadHint ?? (counts && `in ${plural(counts.folders, "folder")}`)}
        tone={error ? "warning" : "default"}
      />
      <StatTile
        label="Read by nginx"
        value={figure(counts?.read)}
        hint={
          unreadHint ??
          (tree &&
            counts &&
            (!tree.includesKnown
              ? "not every include followed"
              : counts.unread > 0
                ? `${counts.unread} not read`
                : "every file"))
        }
        tone={error || (tree && !tree.includesKnown) ? "warning" : "default"}
      />
      <StatTile
        label="Managed"
        value={figure(counts?.managed)}
        hint={unreadHint ?? (counts && `${counts.hand} written by hand`)}
        tone={error ? "warning" : "default"}
      />
      <StatTile
        label="Last test"
        value={
          !admin ? (
            "—"
          ) : test.running || !test.lastRead ? (
            <Skeleton className="my-1.5 h-5 w-12" />
          ) : last ? (
            last.validation.valid ? (
              "Valid"
            ) : (
              "Fails"
            )
          ) : (
            "—"
          )
        }
        hint={
          !admin ? (
            "administrators only"
          ) : test.running ? (
            "Testing…"
          ) : test.lastError ? (
            <span title={errorMessage(test.lastError)}>{"couldn't read"}</span>
          ) : last ? (
            lastWarnings > 0 ? (
              `${plural(lastWarnings, "warning")} · ${tested}`
            ) : (
              tested
            )
          ) : test.lastRead ? (
            "none since the dashboard started"
          ) : undefined
        }
        tone={
          !admin || !last
            ? test.lastError
              ? "warning"
              : "default"
            : !last.validation.valid
              ? "danger"
              : lastWarnings > 0
                ? "warning"
                : "success"
        }
      />
    </StatGrid>
  )
}
