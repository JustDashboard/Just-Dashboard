"use client"

import { useEffect, useState } from "react"
import { useRouter } from "next/navigation"
import { get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { applySiteFix, fixFor, fixLabel, type SiteFix, type TLSFix } from "@/lib/tls-fixes"
import type {
  DirectiveUse,
  Job,
  ScanFinding,
  SiteResult,
  SiteSpec,
  TLSScan,
  VHost,
} from "@/lib/types"
import type { Finding } from "@/components/finding-list"
import { unifiedDiff } from "@/components/files/diff"
import { DiffView } from "@/components/files/diff-view"
import { JobConsole, useJobConsole } from "@/components/job-console"
import { Pane } from "@/components/panel"
import { RowList, Row } from "@/components/row-list"
import { SidePanel, SidePanelFooter } from "@/components/side-panel"
import { EmptyState, ErrorState, Notice, Spinner } from "@/components/state"
import { Tag } from "@/components/tag"
import { ConfigEditor } from "@/components/proxy/config-editor"
import { useNginxReload } from "@/components/proxy/tls-served-by"
import { Button } from "@/components/ui/button"

type SheetFix = Extract<TLSFix, { kind: "site" | "renew" | "directive" }>
type EditorTarget = { path: string; kind: VHost["kind"]; line?: number; title: string }
/** The grade and scan time a change was made over, so the rescan can say what it did. */
type Applied = { grade: string; at: string }

/**
 * The remedies on the TLS report's findings. A reload runs where it is
 * pressed and an issuance opens the Certificates page's own form; everything
 * that changes a file or spends a certificate opens a sheet first, showing
 * the exact change — the diff of the site file, the certbot run, the lines
 * behind a protocol finding — because a button that rewrites nginx without
 * showing what it writes is the kind of fix that causes the next outage.
 *
 * Each goes through an endpoint the dashboard already has (the site form's
 * preview and apply, certbot's renew job, the config editor, test-then-
 * reload), so nothing here can do what those pages cannot.
 */
export function useTLSFixes({
  scan,
  rescanning,
  onRescan,
}: {
  scan: TLSScan | null
  rescanning: boolean
  onRescan: () => void
}) {
  const router = useRouter()
  const { reloading, reload } = useNginxReload(onRescan)
  const [open, setOpen] = useState<{ finding: ScanFinding; fix: SheetFix; session: number }>()
  const [editor, setEditor] = useState<EditorTarget>()

  const actionFor = (finding: ScanFinding): Finding["action"] => {
    const fix = scan ? fixFor(finding, scan) : undefined
    if (!fix) return undefined
    const label = fixLabel(fix)
    switch (fix.kind) {
      case "reload":
        return { label: reloading ? "Reloading…" : label, onClick: () => void reload() }
      case "issue":
        return { label, onClick: () => router.push(fix.href) }
      default:
        return {
          label,
          onClick: () => setOpen({ finding, fix, session: Date.now() }),
        }
    }
  }

  const element = (
    <>
      {open && scan && (
        <FixSheet
          key={open.session}
          finding={open.finding}
          fix={open.fix}
          scan={scan}
          rescanning={rescanning}
          onRescan={onRescan}
          onEditor={(target) => {
            setOpen(undefined)
            setEditor(target)
          }}
          onClose={() => setOpen(undefined)}
        />
      )}
      <ConfigEditor
        open={editor !== undefined}
        onOpenChange={(o) => !o && setEditor(undefined)}
        path={editor?.path ?? ""}
        kind={editor?.kind ?? "nginx"}
        title={editor?.title ?? ""}
        initialLine={editor?.line}
        actions={(busy) => (
          <Button
            size="xs"
            variant="outline"
            onClick={() => void reload()}
            disabled={busy || reloading}
            pending={reloading}
          >
            Reload and scan again
          </Button>
        )}
      />
    </>
  )

  return { actionFor, element }
}

function FixSheet({
  finding,
  fix,
  scan,
  rescanning,
  onRescan,
  onEditor,
  onClose,
}: {
  finding: ScanFinding
  fix: SheetFix
  scan: TLSScan
  rescanning: boolean
  onRescan: () => void
  onEditor: (target: EditorTarget) => void
  onClose: () => void
}) {
  const [busy, setBusy] = useState(false)
  const [applied, setApplied] = useState<Applied>()
  const props = { scan, busy, setBusy, applied, setApplied, onRescan, onEditor }

  return (
    <SidePanel
      open
      onOpenChange={(o) => !o && !busy && onClose()}
      width={fix.kind === "renew" ? "lg" : "xl"}
      initialFocus="body"
      bodyClassName="flex min-h-0 flex-col p-4"
      title={fixLabel(fix)}
      description={finding.title}
      footer={
        <>
          <span className="mr-auto text-hint text-muted-foreground">
            {applied ? (
              <GradeChange applied={applied} scan={scan} rescanning={rescanning} />
            ) : (
              finding.title
            )}
          </span>
          <Button size="sm" variant="outline" onClick={onClose} disabled={busy}>
            {applied ? "Close" : "Cancel"}
          </Button>
        </>
      }
    >
      {fix.kind === "site" && <SiteFixBody fix={fix.fix} site={fix.site} {...props} />}
      {fix.kind === "renew" && <RenewBody certName={fix.certName} {...props} />}
      {fix.kind === "directive" && <DirectiveBody names={fix.names} {...props} />}
    </SidePanel>
  )
}

type BodyProps = {
  scan: TLSScan
  busy: boolean
  setBusy: (busy: boolean) => void
  applied: Applied | undefined
  setApplied: (applied: Applied) => void
  onRescan: () => void
  onEditor: (target: EditorTarget) => void
}

/** The grade before the change and the one the scan after it gave. */
function GradeChange({
  applied,
  scan,
  rescanning,
}: {
  applied: Applied
  scan: TLSScan
  rescanning: boolean
}) {
  if (rescanning || scan.checkedAt === applied.at)
    return (
      <span className="inline-flex items-center gap-1.5">
        <Spinner className="size-3" />
        Scanning again
      </span>
    )
  return (
    <span className="numeric font-medium text-foreground">
      {applied.grade} → {scan.grade}
    </span>
  )
}

type SiteRead = { spec: SiteSpec; managed: boolean; content: string }
type SitePlan =
  | { state: "loading" }
  | { state: "error"; error: Error }
  | { state: "unmanaged"; vhost?: VHost }
  | { state: "blocked"; reason: string; vhost?: VHost }
  | { state: "ready"; before: string; after: string; spec: SiteSpec; vhost?: VHost }

/**
 * A change to a site the dashboard wrote: the spec read back, one switch
 * turned, rendered by the server, and shown as the diff against the file on
 * disk — not against a fresh render of the old spec, so a line the form would
 * not keep shows up as removed here rather than vanishing on save.
 */
function SiteFixBody({
  fix,
  site,
  scan,
  busy,
  setBusy,
  applied,
  setApplied,
  onRescan,
  onEditor,
}: BodyProps & { fix: SiteFix; site: string }) {
  const [plan, setPlan] = useState<SitePlan>({ state: "loading" })

  useEffect(() => {
    const controller = new AbortController()
    const { signal } = controller
    Promise.all([
      get<SiteRead>(`/proxy/sites/${encodeURIComponent(site)}`, undefined, signal),
      get<VHost[]>("/proxy/vhosts", undefined, signal),
    ])
      .then(async ([read, vhosts]) => {
        const vhost = vhosts.find((v) => v.name === site)
        if (!read.managed || vhost?.kind === "caddy") return setPlan({ state: "unmanaged", vhost })
        const next = applySiteFix(read.spec, fix)
        if (next.reason !== undefined)
          return setPlan({ state: "blocked", reason: next.reason, vhost })
        const preview = await post<{ content: string }>(
          "/proxy/sites/preview",
          { spec: next.spec },
          { signal },
        )
        setPlan({
          state: "ready",
          before: read.content,
          after: preview.content,
          spec: next.spec,
          vhost,
        })
      })
      .catch((error: Error) => !signal.aborted && setPlan({ state: "error", error }))
    return () => controller.abort()
  }, [site, fix])

  const vhost = plan.state === "loading" || plan.state === "error" ? undefined : plan.vhost
  const openRaw = () =>
    vhost && onEditor({ path: vhost.path, kind: vhost.kind, title: `Edit ${vhost.name}` })

  const apply = async (spec: SiteSpec) => {
    setBusy(true)
    try {
      // Validated with nginx -t and rolled back if it fails, then reloaded:
      // the scan after it is the only proof the change reached visitors.
      const res = await post<SiteResult>("/proxy/sites/", {
        spec,
        enable: true,
        reload: true,
        overwrite: true,
      })
      if (!res.reloaded) {
        notify.error(`${spec.name} saved, nginx not reloaded`, undefined, {
          description: "The file is on disk and visitors still get the old configuration.",
        })
        return
      }
      notify.success(`${spec.name} changed and reloaded`, { description: "Scanning again." })
      setApplied({ grade: scan.grade, at: scan.checkedAt })
      onRescan()
    } catch (err) {
      notify.error("Not applied", err)
    } finally {
      setBusy(false)
    }
  }

  if (plan.state === "loading")
    return (
      <div className="flex items-center gap-2 text-body text-muted-foreground">
        <Spinner /> Reading {site}
      </div>
    )
  if (plan.state === "error") return <ErrorState error={plan.error} />
  if (plan.state === "unmanaged" || plan.state === "blocked")
    return (
      <div className="space-y-4">
        <Notice
          tone={plan.state === "blocked" ? "warning" : "default"}
          title={
            plan.state === "blocked"
              ? "The site form cannot make this change"
              : `${site} was written by hand`
          }
        >
          <p className="text-body">
            {plan.state === "blocked"
              ? plan.reason
              : "The dashboard changes only the sites it wrote, so a hand-written file is not rewritten from a form. Make the change in the file itself."}
          </p>
        </Notice>
        {vhost && (
          <Button size="xs" variant="outline" onClick={openRaw}>
            Open in raw editor
          </Button>
        )}
      </div>
    )

  const diff = unifiedDiff(plan.before, plan.after, vhost?.path ?? site)
  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3">
      {plan.before !== plan.after && diff !== null && !applied && (
        <SidePanelFooter>
          <Button size="sm" onClick={() => void apply(plan.spec)} disabled={busy} pending={busy}>
            Save, reload and scan again
          </Button>
        </SidePanelFooter>
      )}
      <p className="text-body text-muted-foreground">
        The file for <Tag mono>{site}</Tag> as it is on disk against what the site form writes with
        this change. It is tested with nginx -t before it takes effect and rolled back if the test
        fails.
      </p>
      {plan.before === plan.after ? (
        <Notice title="Nothing would change">
          <p className="text-body">The file already says this.</p>
        </Notice>
      ) : diff === null ? (
        <Notice tone="warning" title="Too many changes to align">
          <p className="text-body">
            The file on disk differs from the form&rsquo;s rendering in more places than can be
            shown. Open it in the raw editor instead.
          </p>
        </Notice>
      ) : (
        <Pane className="min-h-64 flex-1">
          <DiffView body={diff} singleFile lineNumbers className="min-h-0 flex-1" />
        </Pane>
      )}
    </div>
  )
}

/**
 * certbot's renewal of the lineage the scanned file belongs to, streamed. A
 * renewal is written to disk and served only after nginx reads it again, so
 * success offers the reload and the rescan that proves it.
 */
function RenewBody({
  certName,
  scan,
  busy,
  setBusy,
  setApplied,
  onRescan,
}: BodyProps & { certName: string }) {
  const [renewed, setRenewed] = useState(false)
  const console_ = useJobConsole({ onSuccess: () => setRenewed(true) })
  const { reloading, reload } = useNginxReload(() => {
    setApplied({ grade: scan.grade, at: scan.checkedAt })
    onRescan()
  })

  const renew = async () => {
    setBusy(true)
    try {
      const job = await post<Job>("/certificates/renew", {
        name: certName,
        dryRun: false,
        force: false,
      })
      console_.attach(job)
    } catch (err) {
      notify.error("Renewal refused", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3">
      <SidePanelFooter>
        {renewed ? (
          <Button size="sm" onClick={() => void reload()} disabled={reloading} pending={reloading}>
            Reload nginx and scan again
          </Button>
        ) : (
          console_.job === null && (
            <Button size="sm" onClick={() => void renew()} disabled={busy} pending={busy}>
              Renew {certName}
            </Button>
          )
        )}
      </SidePanelFooter>
      <p className="text-body text-muted-foreground">
        Runs <Tag mono>certbot renew --cert-name {certName}</Tag> on the host. certbot renews only a
        certificate inside its renewal window, and the new one is served after nginx reloads.
      </p>
      <JobConsole
        job={console_.job}
        lines={console_.lines}
        onCancel={console_.cancel}
        className="min-h-0 flex-1"
      />
    </div>
  )
}

/**
 * Every place the directives behind a protocol finding are set, each opening
 * the editor at its line. Which one applies depends on the server block that
 * answered, so all of them are listed with the names of their server.
 */
function DirectiveBody({ names, scan, onEditor }: BodyProps & { names: string[] }) {
  const [uses, setUses] = useState<{ name: string; list: DirectiveUse[] }[]>()
  const [error, setError] = useState<Error>()

  useEffect(() => {
    const controller = new AbortController()
    Promise.all(
      names.map((name) =>
        get<DirectiveUse[]>("/proxy/tools/directive", { name }, controller.signal).then((list) => ({
          name,
          list,
        })),
      ),
    )
      .then(setUses)
      .catch((err: Error) => !controller.signal.aborted && setError(err))
    return () => controller.abort()
  }, [names])

  if (error) return <ErrorState error={error} />
  if (!uses)
    return (
      <div className="flex items-center gap-2 text-body text-muted-foreground">
        <Spinner /> Reading the configuration nginx loads
      </div>
    )
  return (
    <div className="space-y-6">
      {uses.map(({ name, list }) => (
        <section key={name} className="space-y-2">
          <h3 className="text-title font-medium">
            <Tag mono>{name}</Tag>
          </h3>
          {list.length === 0 ? (
            <EmptyState
              title={`${name} is not set in any file under the proxy directory`}
              description={
                name === "ssl_protocols"
                  ? "nginx then uses its default, TLSv1.2 TLSv1.3 since 1.23.4. A file outside the proxy directory — certbot's options-ssl-nginx.conf is the usual one — is not searched and may set it."
                  : name === "server_tokens"
                    ? "nginx then uses its default, on, which puts its version in the Server header and on its error pages. Add server_tokens off; to the http block of nginx.conf, or to this site's server block, then test and reload."
                    : "nginx then uses its default. A file outside the proxy directory — certbot's options-ssl-nginx.conf is the usual one — is not searched and may set it."
              }
            />
          ) : (
            <RowList>
              {list.map((use) => {
                const answers = use.server?.some((n) => n === scan.domain)
                return (
                  <Row
                    key={`${use.path}:${use.line}`}
                    onClick={() =>
                      onEditor({
                        path: use.path,
                        kind: "nginx",
                        line: use.line,
                        title: `${use.path}:${use.line}`,
                      })
                    }
                    title={<span className="font-mono">{use.value}</span>}
                    subtitle={`${use.path}:${use.line} · ${use.context || "main"}${
                      use.server?.length ? ` · ${use.server.join(" ")}` : ""
                    }`}
                    mono
                    trailing={answers ? <Tag>this name</Tag> : undefined}
                  />
                )
              })}
            </RowList>
          )}
        </section>
      ))}
    </div>
  )
}
