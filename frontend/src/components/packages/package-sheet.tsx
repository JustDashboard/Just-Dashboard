"use client"

import { useCallback, useState } from "react"
import Link from "next/link"
import {
  ArrowCircleUp,
  Copy,
  Download,
  External,
  Puzzle,
  Terminal,
  Trash,
} from "@/components/icons"
import { get, post } from "@/lib/api"
import { bytes } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { Job, PackageDetail, PackageUsage } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { PackageMark } from "@/components/packages/marks"
import { Detail, DetailList } from "@/components/page"
import { Well } from "@/components/panel"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { SidePanel } from "@/components/side-panel"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { ROW_REVEAL } from "@/components/icon-action"
import { cn } from "@/lib/utils"
import { copyText } from "@/lib/clipboard"

/**
 * One package, and the question every panel in this class leaves unanswered.
 *
 * A version, a size and a dependency list describe a package; they do not tell
 * you that installing `postgresql-client-16` gave you a command called `psql`,
 * that it registered no service, and that its manual is one keystroke away.
 * That is what the second tab is, and it is read from the package's own file
 * list — nothing here is a hand-written table of blurbs that goes stale, and
 * nothing here runs the package's binaries to find out (see internal/updates/
 * usage.go for why that is a rule rather than an omission).
 */
export function PackageSheet({
  name,
  onOpenChange,
  onJob,
  canPurge,
}: {
  /** The package to show, or null when the sheet is closed. */
  name: string | null
  onOpenChange: (open: boolean) => void
  /** Hands a started install or removal to the page's console. */
  onJob: (job: Job) => void
  canPurge: boolean
}) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [busy, setBusy] = useState(false)
  const [tab, setTab] = useViewState("packages.sheet.tab", "about")

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
      return { name: target, detail: d.value, usage: u.status === "fulfilled" ? u.value : null }
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
    async (path: string, body: object, confirmPhrase?: string) => {
      setBusy(true)
      try {
        const job = await post<Job>(path, body, confirmPhrase ? { confirm: confirmPhrase } : {})
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
      // The phrase only guards the purge, and only because that half has no
      // way back: an ordinary removal is undone by installing the package
      // again from the repository it came from.
      phrase: purge ? `purge ${name}` : undefined,
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
      action: async (phrase) => {
        await act("/packages/remove", { packages: [name], purge }, purge ? phrase : undefined)
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
        // The sheet opens on the thing itself: its mark, then its name.
        title={
          <>
            {name && <PackageMark name={name} section={detail?.section} />}
            <span className="min-w-0 truncate font-mono">{name}</span>
            {detail?.installed && <Status verdict="ok" label="Installed" />}
            {upgradable && <Status verdict="warning" label={`${upgradable} available`} />}
          </>
        }
        description={detail?.summary || undefined}
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
          <Tabs value={tab} onValueChange={setTab} className="gap-3">
            <TabsList>
              <TabsTrigger value="about">About</TabsTrigger>
              <TabsTrigger value="usage">How to use it</TabsTrigger>
            </TabsList>

            <TabsContent value="about" className="space-y-4">
              {detail.description && (
                <p className="text-body leading-relaxed whitespace-pre-line text-muted-foreground">
                  {detail.description}
                </p>
              )}

              <DetailList>
                <Detail label="Version">
                  <span className="font-mono">
                    {detail.installedVersion ?? detail.version ?? "—"}
                  </span>
                </Detail>
                {detail.installedVersion &&
                  detail.version &&
                  detail.version !== detail.installedVersion && (
                    <Detail label="In the repository">
                      <span className="font-mono">{detail.version}</span>
                    </Detail>
                  )}
                {detail.repository && <Detail label="Repository">{detail.repository}</Detail>}
                {detail.section && <Detail label="Section">{detail.section}</Detail>}
                {detail.arch && <Detail label="Architecture">{detail.arch}</Detail>}
                {detail.size ? <Detail label="Size">{bytes(detail.size)}</Detail> : null}
                {detail.license && <Detail label="Licence">{detail.license}</Detail>}
                {detail.maintainer && (
                  <Detail label="Maintainer" className="truncate">
                    {detail.maintainer}
                  </Detail>
                )}
                {detail.homepage && (
                  <Detail label="Homepage">
                    <a
                      href={detail.homepage}
                      target="_blank"
                      rel="noreferrer noopener"
                      className="inline-flex min-w-0 items-center gap-1 truncate text-primary hover:underline"
                    >
                      <span className="truncate">{detail.homepage}</span>
                      <External className="size-3 shrink-0" />
                    </a>
                  </Detail>
                )}
              </DetailList>

              {detail.protected && (
                <Notice title="This one cannot be removed from here">{detail.protected}</Notice>
              )}

              {detail.dependencies && detail.dependencies.length > 0 && (
                <div className="space-y-1.5">
                  <p className="eyebrow">Depends on</p>
                  <div className="flex flex-wrap gap-1">
                    {detail.dependencies.slice(0, 40).map((dep) => (
                      <Tag key={dep} mono>
                        {dep}
                      </Tag>
                    ))}
                    {detail.dependencies.length > 40 && (
                      <span className="numeric self-center text-hint text-muted-foreground">
                        +{detail.dependencies.length - 40} more
                      </span>
                    )}
                  </div>
                </div>
              )}
            </TabsContent>

            <TabsContent value="usage">
              <UsageView usage={usage} installed={detail.installed} />
            </TabsContent>
          </Tabs>
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
      className="group inline-flex items-center gap-1.5 rounded-md border border-hairline bg-control px-2 py-1 font-mono text-xs transition-colors hover:bg-control-hover active:bg-control-active"
    >
      <Terminal className="size-3 text-muted-foreground" />
      {command}
      <Copy className={cn("size-3 text-muted-foreground", ROW_REVEAL)} />
    </button>
  )
}

/**
 * One part of the answer, opened by its eyebrow alone. Each used to carry a
 * glyph in front of the word — a terminal before "Commands", a book before
 * "Manual" — which was the label twice (design-system §14).
 */
function UsageSection({
  title,
  hint,
  children,
}: {
  title: string
  hint?: string
  children: React.ReactNode
}) {
  return (
    <section className="space-y-1.5">
      <p className="eyebrow">{title}</p>
      {hint && <p className="text-hint leading-relaxed text-muted-foreground">{hint}</p>}
      {children}
    </section>
  )
}

function UsageView({ usage, installed }: { usage: PackageUsage | null; installed: boolean }) {
  if (!installed) {
    return (
      <EmptyState
        icon={Puzzle}
        title="Not installed yet"
        description="Install it and this tab will say which commands it gave you, which service it registered and what its manual says."
      />
    )
  }
  if (!usage) return <LoadingRows rows={4} />
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
        <UsageSection
          title="Commands"
          hint="What this package actually put on your path — which is very often not its own name."
        >
          <div className="flex flex-wrap gap-1.5">
            {usage.commands.map((command) => (
              <CommandChip key={command} command={command} />
            ))}
          </div>
        </UsageSection>
      )}

      {usage.services && usage.services.length > 0 && (
        <UsageSection
          title="Services"
          hint="Installing a package rarely starts it. These are the units it registered."
        >
          <div className="space-y-1">
            {usage.services.map((service) => (
              <div key={service} className="flex flex-wrap items-center gap-2">
                <span className="font-mono text-xs">{service}</span>
                <CommandChip command={`systemctl start ${service}`} />
              </div>
            ))}
          </div>
        </UsageSection>
      )}

      {usage.configFiles && usage.configFiles.length > 0 && (
        <UsageSection
          title="Configuration"
          hint="What it put in /etc. Each opens in the file manager."
        >
          <ul className="space-y-0.5">
            {usage.configFiles.map((file) => (
              <li key={file}>
                <Link
                  href={`/files?path=${encodeURIComponent(file)}`}
                  className="font-mono text-xs text-primary hover:underline"
                >
                  {file}
                </Link>
              </li>
            ))}
          </ul>
        </UsageSection>
      )}

      {usage.docs && usage.docs.length > 0 && (
        <UsageSection title="Documentation on this machine">
          <ul className="space-y-0.5">
            {usage.docs.map((file) => (
              <li key={file}>
                <Link
                  href={`/files?path=${encodeURIComponent(file)}`}
                  className="font-mono text-xs text-primary hover:underline"
                >
                  {file}
                </Link>
              </li>
            ))}
          </ul>
        </UsageSection>
      )}

      {usage.manUnavailable && (
        <Notice title="The manual could not be read">{usage.manUnavailable}</Notice>
      )}

      {usage.manual && (
        <UsageSection
          title={`Manual — ${usage.manualFor}`}
          hint={`The same page as \`man ${usage.manualFor}\`, rendered here so you do not have to open a shell to read it.`}
        >
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
