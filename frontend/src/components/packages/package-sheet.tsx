"use client"

import { useCallback, useState } from "react"
import {
  ArrowCircleUp,
  Copy,
  Download,
  External,
  Puzzle,
  Terminal,
  Trash,
} from "@/components/icons"
import { errorMessage, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { Job, PackageDetail, PackageUsage } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { LiveBytes, HUE } from "@/components/overview/readings"
import { ShellWords } from "@/components/deploy/run-evidence"
import { PackageMark, OriginFact, VersionTo } from "@/components/packages/marks"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { SidePanel } from "@/components/side-panel"
import { Button } from "@/components/ui/button"
import { ROW_REVEAL } from "@/components/icon-action"
import { cn } from "@/lib/utils"
import { copyText } from "@/lib/clipboard"

/**
 * One package, and the question every panel in this class leaves unanswered.
 *
 * A version, a size and a dependency list describe a package; they do not tell
 * you that installing `postgresql-client-16` gave you a command called `psql`,
 * that it registered no service, and that its manual is one keystroke away.
 * That is what the readout opens on, and it is read from the package's own file
 * list — nothing here is a hand-written table of blurbs that goes stale, and
 * nothing here runs the package's binaries to find out (see internal/updates/
 * usage.go for why that is a rule rather than an omission).
 */
export function PackageSheet({
  name,
  onOpenChange,
  onJob,
  canPurge,
  onInspect,
}: {
  /** The package to show, or null when the sheet is closed. */
  name: string | null
  onOpenChange: (open: boolean) => void
  /** Hands a started install or removal to the page's console. */
  onJob: (job: Job) => void
  canPurge: boolean
  onInspect: (name: string) => void
}) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [busy, setBusy] = useState(false)

  // The two reads are fired together rather than the usage one waiting for the
  // detail: they are independent on the server and the manual is the slower of
  // the two, so serialising them would show a package's own name a second
  // before anything else on the page.
  const load = useCallback(
    async (signal: AbortSignal) => {
      const target = name ?? ""
      const [d, u] = await Promise.allSettled([
        get<PackageDetail>(`/packages/${encodeURIComponent(target)}`, undefined, signal),
        get<PackageUsage>(`/packages/${encodeURIComponent(target)}/usage`, undefined, signal),
      ])
      if (d.status === "rejected") throw d.reason
      // A package that is not installed owns no files, so an unreadable usage
      // is the normal case rather than a failure worth reporting.
      return {
        name: target,
        detail: d.value,
        usage: u.status === "fulfilled" ? u.value : null,
        usageError: u.status === "rejected" ? errorMessage(u.reason) : undefined,
      }
    },
    [name],
  )

  // Fetched once rather than polled: a package's description and its manual do
  // not change while somebody is reading them, and re-rendering a forty
  // kilobyte man page every five seconds would scroll it out from under them.
  const poll = usePoll(load, 0, [name], { enabled: Boolean(name) })
  // usePoll keeps the previous answer across a deps change, which is right for
  // a table that should not blank between refreshes and wrong here: it would
  // render the last package's manual under this one's name.
  const current = poll.data?.name === name ? poll.data : undefined
  const detail = current?.detail
  const usage = current?.usage ?? null
  const error = current ? undefined : poll.error
  const loading = Boolean(name) && !current && !error

  const act = useCallback(
    async (path: string, body: object) => {
      setBusy(true)
      try {
        const job = await post<Job>(path, body)
        onJob(job)
        onOpenChange(false)
      } finally {
        setBusy(false)
      }
    },
    [onJob, onOpenChange],
  )

  const install = () =>
    void act("/packages/install", { packages: [name] }).catch((err) =>
      notify.error("Could not start the install", err),
    )

  const remove = (purge: boolean) =>
    confirm({
      title: purge ? `Remove ${name} and its configuration` : `Remove ${name}`,
      confirmLabel: "Remove",
      description: purge ? (
        <>
          <p>
            Removes the package <b>and deletes the files it put in /etc</b>. Anything you edited
            there — a config file you spent an afternoon on — goes with it.
          </p>
          <p className="text-muted-foreground">
            Dependencies that were only installed for this are removed too.
          </p>
        </>
      ) : (
        <>
          <p>
            Removes the package and the dependencies that were only there for it. Configuration in
            /etc is left where it is.
          </p>
          <p className="text-muted-foreground">
            Installing it again from the same repository puts it back.
          </p>
        </>
      ),
      action: async () => {
        await act("/packages/remove", { packages: [name], purge })
      },
    })

  const canManage = can("system.admin")
  const upgradable = detail?.upgradable

  return (
    <>
      {dialog}
      <SidePanel
        open={Boolean(name)}
        onOpenChange={onOpenChange}
        width="md"
        initialFocus="body"
        // The sheet opens on the thing itself: its mark, then its name.
        title={
          <>
            {name && <PackageMark name={name} section={detail?.section} />}
            <span className="min-w-0 truncate font-mono">{name}</span>
          </>
        }
        description={detail?.summary || undefined}
        actions={
          detail && (
            <>
              <Status
                verdict={detail.installed ? "ok" : "notice"}
                label={detail.installed ? "Installed" : "Not installed"}
              />
              {upgradable && <Status verdict="notice" label="Update available" />}
              {detail.section && (
                <span className="text-hint text-muted-foreground">{detail.section}</span>
              )}
              {detail.arch && (
                <span className="font-mono text-hint text-muted-foreground">{detail.arch}</span>
              )}
            </>
          )
        }
        footer={
          detail &&
          canManage && (
            <>
              {!detail.installed && (
                <Button size="sm" onClick={install} pending={busy}>
                  <Download className="size-4" />
                  Install
                </Button>
              )}
              {upgradable && (
                <Button size="sm" disabled={busy} onClick={install}>
                  <ArrowCircleUp className="size-4" />
                  Update to {upgradable}
                </Button>
              )}
              {detail.installed && !detail.protected && (
                <>
                  {canPurge && (
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={busy}
                      onClick={() => remove(true)}
                    >
                      <Trash className="size-4" />
                      Remove and purge
                    </Button>
                  )}
                  <Button
                    variant="destructive"
                    size="sm"
                    disabled={busy}
                    onClick={() => remove(false)}
                  >
                    <Trash className="size-4" />
                    Remove
                  </Button>
                </>
              )}
            </>
          )
        }
      >
        {error && <ErrorState error={error} />}
        {loading && <LoadingRows rows={6} />}

        {detail && (
          <div data-slot="package-readout" className="animate-rise space-y-6">
            <div className="grid min-w-0 grid-cols-2 gap-4 border-b border-hairline pb-5 sm:grid-cols-3">
              <div className="min-w-0 space-y-1.5">
                <p className="eyebrow">
                  {detail.installed ? "Installed version" : "Available version"}
                </p>
                <p
                  className="truncate font-mono text-title"
                  title={detail.installedVersion ?? detail.version}
                >
                  {detail.installedVersion ?? detail.version ?? "—"}
                </p>
              </div>
              {upgradable && (
                <div className="min-w-0 space-y-1.5">
                  <p className="eyebrow">Update to</p>
                  <VersionTo
                    from={detail.installedVersion}
                    to={upgradable}
                    className="block truncate text-title"
                  />
                </div>
              )}
              <div className="min-w-0 space-y-1.5">
                <p className="eyebrow">{detail.installed ? "On disk" : "Installed size"}</p>
                <p className="numeric text-2xl font-semibold" style={{ color: HUE.disk }}>
                  {detail.size ? <LiveBytes value={detail.size} /> : "—"}
                </p>
              </div>
            </div>
            {detail.protected && (
              <Notice title="This one cannot be removed from here">{detail.protected}</Notice>
            )}
            <UsageView
              usage={usage}
              installed={detail.installed}
              error={current?.usageError}
              onRetry={poll.refresh}
            />
            {detail.dependencies && detail.dependencies.length > 0 && (
              <UsageSection title="Depends on">
                <div className="flex flex-wrap gap-1.5">
                  {detail.dependencies.slice(0, 40).map((dep) => (
                    <button
                      key={dep}
                      type="button"
                      onClick={() => onInspect(dep.split(/[ (|<>=]/)[0])}
                      disabled={busy}
                      aria-label={`Inspect dependency ${dep}`}
                      className="inline-flex min-h-8 max-w-full min-w-0 items-center gap-1.5 rounded-md border border-hairline bg-control px-2 text-left font-mono text-hint focus-ring transition-colors hover:border-border-strong"
                    >
                      <PackageMark name={dep} className="size-4" />
                      <span className="truncate">{dep}</span>
                    </button>
                  ))}
                  {detail.dependencies.length > 40 && (
                    <span className="numeric self-center text-hint text-muted-foreground">
                      +{detail.dependencies.length - 40} more
                    </span>
                  )}
                </div>
              </UsageSection>
            )}
            <UsageSection title="About this package">
              {(detail.description || detail.summary) && (
                <p className="text-body leading-relaxed whitespace-pre-line text-muted-foreground">
                  {detail.description || detail.summary}
                </p>
              )}
              <DetailList>
                {detail.repository && (
                  <Detail label="Repository">
                    <OriginFact origin={detail.repository} />
                  </Detail>
                )}
                {detail.license && <Detail label="Licence">{detail.license}</Detail>}
                {detail.maintainer && (
                  <Detail label="Maintainer">
                    <span className="break-all">{detail.maintainer}</span>
                  </Detail>
                )}
                {detail.homepage && (
                  <Detail label="Homepage">
                    <a
                      href={detail.homepage}
                      target="_blank"
                      rel="noreferrer noopener"
                      className="inline-flex max-w-full min-w-0 items-center gap-1 text-primary hover:underline"
                    >
                      <span className="truncate">{detail.homepage}</span>
                      <External className="size-3 shrink-0" />
                    </a>
                  </Detail>
                )}
              </DetailList>
            </UsageSection>
          </div>
        )}
      </SidePanel>
    </>
  )
}

/** Copies a command, because the next thing anybody does with one is run it. */
function CommandChip({ command }: { command: string }) {
  return (
    <button
      type="button"
      aria-label={`Copy ${command}`}
      onClick={() => void copyText(command, `Copied ${command}`)}
      className="group inline-flex min-h-9 max-w-full min-w-0 items-center gap-1.5 rounded-md border border-hairline bg-control px-2 py-1 font-mono text-body focus-ring transition-colors hover:bg-control-hover active:bg-control-active"
    >
      <Terminal className="size-3 text-muted-foreground" />
      <span className="min-w-0 truncate">
        <ShellWords command={command} />
      </span>
      <Copy className={cn("size-3 text-muted-foreground", ROW_REVEAL)} />
    </button>
  )
}

/**
 * Usage and metadata share the page’s section heads and hairlines.
 */
function UsageSection({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <Panel plain>
      <PanelHeader title={title} />
      <PanelBody className="space-y-3">{children}</PanelBody>
    </Panel>
  )
}

function UsageView({
  usage,
  installed,
  error,
  onRetry,
}: {
  usage: PackageUsage | null
  installed: boolean
  error?: string
  onRetry: () => void
}) {
  if (!installed) {
    return (
      <EmptyState
        icon={Puzzle}
        title="Not installed yet"
        description="Install it to see the commands, services, configuration files and manual it provides."
      />
    )
  }
  if (!usage)
    return (
      <ErrorState
        error={new Error(error || "Could not read this package’s commands and files")}
        onRetry={onRetry}
      />
    )
  if (usage.empty) {
    return (
      <EmptyState
        icon={Puzzle}
        title="Nothing to run"
        description="This package ships no commands, manual pages or services — it is a library that other packages are built on, and there is nothing here to use directly."
      />
    )
  }

  return (
    <div className="space-y-5">
      {usage.commands && usage.commands.length > 0 && (
        <UsageSection title="Commands">
          <div className="flex flex-wrap gap-1.5">
            {usage.commands.map((command) => (
              <CommandChip key={command} command={command} />
            ))}
          </div>
        </UsageSection>
      )}

      {usage.services && usage.services.length > 0 && (
        <UsageSection title="Services">
          <ChoiceList>
            {usage.services.map((service) => (
              <ChoiceRow
                key={service}
                leading={<PackageMark name={service} />}
                title={<span className="font-mono">{service}</span>}
                verb={`Open ${service}`}
                href={`/processes/services?unit=${encodeURIComponent(service)}`}
              >
                <CommandChip command={`systemctl start ${service}`} />
              </ChoiceRow>
            ))}
          </ChoiceList>
        </UsageSection>
      )}

      {usage.configFiles && usage.configFiles.length > 0 && (
        <UsageSection title="Configuration">
          <ChoiceList>
            {usage.configFiles.map((file) => (
              <ChoiceRow
                key={file}
                title={<span className="font-mono">{file}</span>}
                verb={`Open ${file}`}
                href={`/files?path=${encodeURIComponent(file)}`}
              />
            ))}
          </ChoiceList>
        </UsageSection>
      )}

      {usage.docs && usage.docs.length > 0 && (
        <UsageSection title="Documentation on this machine">
          <ChoiceList>
            {usage.docs.map((file) => (
              <ChoiceRow
                key={file}
                title={<span className="font-mono">{file}</span>}
                verb={`Open ${file}`}
                href={`/files?path=${encodeURIComponent(file)}`}
              />
            ))}
          </ChoiceList>
        </UsageSection>
      )}

      {usage.manUnavailable && (
        <Notice title="The manual could not be read">{usage.manUnavailable}</Notice>
      )}

      {usage.manual && (
        <UsageSection title={`Manual — ${usage.manualFor}`}>
          {/* whitespace-pre, not pre-wrap: a man page's two columns are made
              of spaces, and wrapping them folds the description under the flag
              it belongs to. It scrolls sideways inside the well instead, which
              is the rule every wide block in this product follows. */}
          <Well className="max-h-[26rem] overflow-auto p-3">
            <pre className="font-mono text-hint leading-relaxed whitespace-pre">{usage.manual}</pre>
          </Well>
          {usage.truncated && (
            <p className="text-hint text-muted-foreground">
              Cut off here — the rest is in `man {usage.manualFor}` from a terminal.
            </p>
          )}
        </UsageSection>
      )}

      {usage.manPages && usage.manPages.length > 1 && (
        <UsageSection title="Other manual pages">
          <div className="flex flex-wrap gap-1">
            {usage.manPages.map((page) => (
              <Tag key={page.path} mono title={page.path}>
                {page.name}({page.section})
              </Tag>
            ))}
          </div>
        </UsageSection>
      )}
    </div>
  )
}
