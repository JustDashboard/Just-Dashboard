"use client"

import { useState } from "react"
import { Copy, Download, Linked, Warning } from "@/components/icons"
import { notify } from "@/lib/toast"
import { ApiError, get, post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import type { Job, StreamIncludePlan, StreamIncludeResult, StreamStatus } from "@/lib/types"
import {
  connectChange,
  includedPlace,
  moduleLabel,
  moduleMissing,
  moduleRemedy,
  streamOutage,
} from "@/lib/streams"
import { usePoll } from "@/hooks/use-poll"
import { CodeEditor } from "@/components/code-editor"
import { Disclosure, FormFact, FormFacts, FormNote, OptionList, OptionRow } from "@/components/form"
import { JobConsole, useJobConsole } from "@/components/job-console"
import { Pane, Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { SidePanel } from "@/components/side-panel"
import { ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"

/**
 * What stands between the stream directory and a forwarded port, and the two
 * fixes the page makes itself.
 *
 * In the order they have to be made: nginx needs the stream module — Debian
 * and Ubuntu ship it as a package of its own, and a stream block without it
 * stops nginx reloading at all — and then a top-level stream block has to
 * include the directory, or the files are written and ignored. The first is a
 * package install run as a job the operator watches; the second is a change
 * to nginx's configuration shown in full before it is made, tested by nginx
 * before it stays, and taken out again from the page's menu.
 *
 * The outages come first and stay manual: an include in the wrong block is
 * the operator's own line, and it comes out by hand.
 */
export function StreamSetup({
  status,
  admin,
  onChanged,
}: {
  status: StreamStatus
  admin: boolean
  /** The listing is out of date: a module went in, or the directory was connected. */
  onChanged: () => void
}) {
  // The listing is only right once the package manager has finished, so it
  // is read again when the job ends rather than when it starts.
  const console_ = useJobConsole({ onSuccess: onChanged })
  const [starting, setStarting] = useState(false)
  const [sheet, setSheet] = useState({ open: false, session: 0 })
  const pkg = status.module.package

  const install =
    admin && pkg && status.module.state === "not-installed"
      ? {
          label: pkg,
          busy: starting || console_.running,
          run: async () => {
            setStarting(true)
            try {
              console_.attach(await post<Job>("/packages/install", { packages: [pkg] }))
            } catch (err) {
              notify.error(`Could not start installing ${pkg}`, err)
            } finally {
              setStarting(false)
            }
          },
        }
      : undefined

  return (
    <>
      <Readiness
        status={status}
        install={install}
        onConnect={
          admin ? () => setSheet((s) => ({ open: true, session: s.session + 1 })) : undefined
        }
      />
      <JobConsole
        job={console_.job}
        lines={console_.lines}
        onDismiss={console_.dismiss}
        onCancel={console_.cancel}
      />
      {admin && (
        <ConnectSheet
          key={sheet.session}
          open={sheet.open}
          dir={status.dir}
          onOpenChange={(open) => setSheet((s) => ({ ...s, open }))}
          onConnected={onChanged}
        />
      )}
    </>
  )
}

type Install = { label: string; busy: boolean; run: () => void }

/** The install, as the one command where the module is what is missing. */
function InstallButton({ install }: { install: Install }) {
  return (
    <Button size="sm" onClick={install.run} pending={install.busy} disabled={install.busy}>
      <Download className="size-4" />
      {install.busy ? `Installing ${install.label}…` : `Install ${install.label}`}
    </Button>
  )
}

function Readiness({
  status,
  install,
  onConnect,
}: {
  status: StreamStatus
  install?: Install
  onConnect?: () => void
}) {
  const { module } = status
  const missing = moduleMissing(module)
  const outage = streamOutage(status)
  const place = status.includedIn ? includedPlace(status.includedIn) : ""
  const unchecked =
    module.state === "unknown"
      ? ` Whether this nginx has the stream module could not be checked (${module.detail ?? "no answer"}); if nginx then reports an unknown directive "stream", it does not.`
      : ""
  if (outage === "module") {
    return (
      <Notice
        tone="danger"
        icon={Warning}
        title="nginx.conf has a stream block this nginx cannot read"
      >
        <div className="space-y-2">
          <p>
            nginx has no stream module, so its configuration test fails on the{" "}
            <code className="font-mono">stream</code> block and every reload is refused — for every
            site on this host, not only the streams. {moduleRemedy(module)}{" "}
            {status.connection
              ? "Or disconnect the stream directory from this page's menu."
              : "Or take the stream block out of nginx.conf."}
          </p>
          {install && <InstallButton install={install} />}
        </div>
      </Notice>
    )
  }
  if (outage === "misplaced") {
    // nginx does read these files, as whatever block the include sits in, and
    // refuses them there: "not reading these" would be the opposite of it.
    return (
      <Notice tone="danger" icon={Warning} title="These files stop every nginx reload">
        <div className="space-y-2">
          <p>
            nginx.conf includes <code className="font-mono">{status.dir}</code> {place}, where a
            stream is not allowed, so nginx&rsquo;s configuration test fails on these files and
            every reload is refused — for every site on this host, not only the streams.
          </p>
          <p>
            {missing
              ? `Take out that include, which ends the refusals. ${moduleRemedy(module)} Then add this at the top level of nginx.conf — beside the http block, not inside it:`
              : "Move the include into a stream block of its own at the top level of nginx.conf — beside the http block, not inside it:"}
          </p>
          <Well className="whitespace-pre">{status.snippet}</Well>
          {!missing && <p>Deleting the files below also ends the refusals.{unchecked}</p>}
        </div>
      </Notice>
    )
  }
  if (status.includedIn) {
    return (
      <Notice
        tone="warning"
        icon={Warning}
        title={
          missing
            ? "This nginx cannot forward streams yet"
            : "This directory is included in the wrong place"
        }
      >
        <div className="space-y-2">
          {missing ? (
            <>
              <p>
                nginx has no stream module, and a stream block in nginx.conf would fail its
                configuration test until it does. {moduleRemedy(module)}
              </p>
              <p>
                nginx.conf includes <code className="font-mono">{status.dir}</code> {place} instead,
                where its test refuses any stream file, so nothing can be saved here until that
                include comes out.
              </p>
              {install && <InstallButton install={install} />}
            </>
          ) : (
            <>
              <p>
                nginx.conf includes <code className="font-mono">{status.dir}</code> {place}, where
                nginx does not read the files as streams: its test refuses any file there, so
                nothing can be saved here until the include moves into a stream block of its own at
                the top level:
              </p>
              <Well className="whitespace-pre">{status.snippet}</Well>
              {unchecked && <p>{unchecked.trim()}</p>}
            </>
          )}
        </div>
      </Notice>
    )
  }
  if (status.included) return null
  return <SetupSteps status={status} install={install} onConnect={onConnect} />
}

/**
 * The two steps as a list the operator works down: each with its state, what
 * it means, and the button that does it where the page can.
 */
function SetupSteps({
  status,
  install,
  onConnect,
}: {
  status: StreamStatus
  install?: Install
  onConnect?: () => void
}) {
  const { module } = status
  const missing = moduleMissing(module)
  const outage = "Without it, a stream block stops nginx reloading — for every site on this host."
  return (
    // A plain block, not a notice: these are steps with their own states,
    // and the one that needs doing carries the page's command.
    <Panel plain>
      <PanelHeader
        title={missing ? "This nginx cannot forward streams yet" : "nginx is not reading these yet"}
      />
      <PanelBody>
        <ol className="min-w-0 divide-y divide-hairline" aria-label="Before a stream can forward">
          <SetupStep
            index={1}
            title="The stream module"
            status={
              <Status
                verdict={module.usable ? "ok" : missing ? "warning" : "notice"}
                label={moduleLabel(module)}
              />
            }
          >
            {module.state === "static" ? (
              <p>Compiled into this nginx.</p>
            ) : module.state === "loaded" ? (
              <p>Loaded by the configuration.</p>
            ) : module.state === "unknown" ? (
              <p>
                nginx could not be asked ({module.detail ?? "no answer"}). If connecting then fails
                on an unknown directive &ldquo;stream&rdquo;, this nginx has no stream module.
              </p>
            ) : install ? (
              <>
                <p>Debian and Ubuntu ship it as a package of its own. {outage}</p>
                <InstallButton install={install} />
              </>
            ) : (
              <p>
                {moduleRemedy(module)} {outage}
              </p>
            )}
          </SetupStep>
          <SetupStep
            index={2}
            title="Connect this directory"
            status={
              missing ? (
                <Status tone="stopped" label="after the module" />
              ) : (
                <Status
                  verdict="warning"
                  label={status.includeError ? "could not tell" : "not read"}
                />
              )
            }
          >
            {missing ? (
              <p>
                nginx.conf has no stream block including{" "}
                <code className="font-mono break-all">{status.dir}</code>. Once the module is in,
                the dashboard can add one; before it, a stream block would stop nginx reloading.
              </p>
            ) : status.includeError ? (
              <>
                <p>
                  nginx.conf could not be read to tell whether it includes{" "}
                  <code className="font-mono break-all">{status.dir}</code>: {status.includeError}.
                </p>
                <ManualInclude status={status} />
              </>
            ) : (
              <>
                <p>
                  nginx.conf has no stream block including{" "}
                  <code className="font-mono break-all">{status.dir}</code>, so what is saved here
                  is written and ignored.
                </p>
                {onConnect ? (
                  <>
                    <Button size="sm" onClick={onConnect}>
                      <Linked className="size-4" />
                      Connect
                    </Button>
                    <Disclosure quiet summary="Or add it by hand">
                      <ManualInclude status={status} />
                    </Disclosure>
                  </>
                ) : (
                  <ManualInclude status={status} />
                )}
              </>
            )}
          </SetupStep>
        </ol>
      </PanelBody>
    </Panel>
  )
}

function SetupStep({
  index,
  title,
  status,
  children,
}: {
  index: number
  title: string
  status: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <li className="flex min-w-0 gap-3 py-3 first:pt-0 last:pb-0">
      {/* The list says the order to a screen reader; the figure says it to the eye. */}
      <span aria-hidden className="numeric w-3 shrink-0 text-body text-muted-foreground">
        {index}
      </span>
      <div className="min-w-0 flex-1 space-y-2">
        <div className="flex min-w-0 flex-wrap items-center justify-between gap-x-3 gap-y-1">
          <span className="text-body font-medium">{title}</span>
          {status}
        </div>
        <div className="space-y-2 text-xs leading-relaxed text-muted-foreground">{children}</div>
      </div>
    </li>
  )
}

/** The include to add by hand, where it goes, and a way to take it. */
function ManualInclude({ status }: { status: StreamStatus }) {
  return (
    <div className="min-w-0 space-y-1.5">
      <div className="flex min-h-6 min-w-0 flex-wrap items-center justify-between gap-x-3">
        <p className="min-w-0 text-hint text-muted-foreground">
          {status.streamBlock ? (
            <>
              Inside the stream block in{" "}
              <code className="font-mono break-all">{status.streamBlock}</code> — nginx refuses a
              second one:
            </>
          ) : (
            <>
              At the top level of nginx.conf, beside the <code className="font-mono">http</code>{" "}
              block, not inside it:
            </>
          )}
        </p>
        <Button
          size="xs"
          variant="ghost"
          onClick={() => void copyText(status.snippet, "Include copied")}
        >
          <Copy />
          Copy
        </Button>
      </div>
      <Well className="whitespace-pre">{status.snippet}</Well>
    </div>
  )
}

/** Where the change goes, in a word, for the facts line. */
const MODE_FACT: Record<StreamIncludePlan["mode"], string> = {
  dropin: "a new file",
  "nginx.conf": "an edit to nginx.conf",
  "stream-block": "one line in the stream block",
}

/**
 * The connect, shown whole before it is made: the file it creates or the
 * lines it adds, the streams that start forwarding, and anything that would
 * stop the reload. nginx tests the whole configuration before the change
 * stays, and a test that fails puts every byte back.
 */
function ConnectSheet({
  open,
  dir,
  onOpenChange,
  onConnected,
}: {
  open: boolean
  dir: string
  onOpenChange: (open: boolean) => void
  onConnected: () => void
}) {
  const plan = usePoll<StreamIncludePlan>(
    (signal) => get("/proxy/streams/include/plan", undefined, signal),
    0,
    [],
    { enabled: open },
  )
  const [view, setView] = useState<"after" | "before">("after")
  const [reload, setReload] = useState(true)
  const [busy, setBusy] = useState(false)
  // nginx's own words when its test refused the change, which was undone.
  const [refused, setRefused] = useState("")
  const data = plan.data
  const blocked = !data || data.conflicts.length > 0

  const connect = async () => {
    if (!data) return
    setBusy(true)
    setRefused("")
    try {
      const res = await post<StreamIncludeResult>("/proxy/streams/include", {
        mode: data.mode,
        path: data.path,
        reload,
      })
      const kept = res.backup ? ` The file as it was is kept as ${res.backup}.` : ""
      const count = `${res.streams} stream${res.streams === 1 ? "" : "s"}`
      if (res.reloadError) {
        notify.warning("Connected, reload failed", {
          description: `The change passed nginx's test and is on disk, but nginx did not reload, so nothing forwards yet: ${res.reloadError}`,
        })
      } else if (res.reloaded) {
        notify.success("Stream directory connected", {
          description: `nginx reloaded and reads ${dir}${res.streams > 0 ? `: ${count} forwarding` : ""}.${kept}`,
        })
      } else {
        notify.success("Stream directory connected, not reloaded", {
          description: `The change passed nginx's test; ${res.streams > 0 ? `${count} start` : "streams start"} at the next reload.${kept}`,
        })
      }
      onConnected()
      onOpenChange(false)
    } catch (err) {
      if (err instanceof ApiError && err.code === "invalid_config") {
        setRefused(err.message)
      }
      // The configuration moved since the plan was read: show the new one.
      if (err instanceof ApiError && (err.code === "plan_changed" || err.code === "port_in_use")) {
        plan.refresh()
      }
      notify.error("Not connected", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <SidePanel
      open={open}
      onOpenChange={(o) => !busy && onOpenChange(o)}
      width="lg"
      title="Connect the stream directory"
      description="The change that has nginx read the streams on this page"
      initialFocus="body"
      bodyClassName="flex min-h-0 flex-1 flex-col gap-4 p-4"
      footer={
        <>
          <span className="mr-auto text-hint text-muted-foreground">
            Tested with nginx&rsquo;s own parser first, and put back as it was if the test fails.
          </span>
          <Button
            size="sm"
            onClick={connect}
            disabled={blocked || busy || plan.loading}
            pending={busy}
          >
            {busy ? "Connecting…" : "Connect"}
          </Button>
        </>
      }
    >
      {plan.loading && !data ? (
        <LoadingPanel />
      ) : plan.error && !data ? (
        <ErrorState error={plan.error} onRetry={plan.refresh} />
      ) : data ? (
        <>
          <div className="space-y-3">
            <FormFacts>
              <FormFact label="Change">{MODE_FACT[data.mode]}</FormFact>
              <FormFact label="File" mono>
                {data.path}
              </FormFact>
              <FormFact label="Streams">
                {data.streams.length === 0 ? "none yet" : data.streams.length}
              </FormFact>
            </FormFacts>
            <p className="text-xs leading-relaxed text-muted-foreground">
              {connectChange(data.mode, data.path)}
            </p>
            {data.conflicts.length > 0 && (
              <Notice tone="danger" icon={Warning} title="nginx could not bind every stream">
                <div className="space-y-2">
                  <p>
                    nginx&rsquo;s test does not bind ports, so it would pass this — and then the
                    reload would fail inside nginx, and every reload after it. Change or delete
                    these streams first:
                  </p>
                  <ul className="list-disc space-y-0.5 pl-4">
                    {data.conflicts.map((conflict) => (
                      <li key={conflict}>{conflict}</li>
                    ))}
                  </ul>
                </div>
              </Notice>
            )}
            {data.warnings.map((warning) => (
              <FormNote key={warning} tone="warning">
                {warning}
              </FormNote>
            ))}
            {refused && (
              <Notice
                tone="danger"
                icon={Warning}
                title="nginx’s test refused it, so nothing changed"
              >
                <Well className="max-h-40 text-hint whitespace-pre-wrap">{refused}</Well>
              </Notice>
            )}
            <OptionList>
              <OptionRow
                title="Reload nginx after"
                hint={
                  reload
                    ? data.streams.length > 0
                      ? data.streams.length === 1
                        ? "The stream here starts forwarding once the test passes."
                        : `The ${data.streams.length} streams here start forwarding once the test passes.`
                      : "nginx reads the directory from now on; a new stream forwards when it is saved."
                    : "Written and tested now; nginx reads the directory at its next reload."
                }
                checked={reload}
                onCheckedChange={setReload}
              />
            </OptionList>
            {data.exists && (
              <ToggleGroup
                type="single"
                value={view}
                onValueChange={(v) => v && setView(v as "after" | "before")}
                variant="outline"
                size="sm"
                aria-label="Show the file"
              >
                <ToggleGroupItem value="after" className="text-hint">
                  After
                </ToggleGroupItem>
                <ToggleGroupItem value="before" className="text-hint">
                  Before
                </ToggleGroupItem>
              </ToggleGroup>
            )}
          </div>
          {/* The file itself, with the added line marked: what nginx will read. */}
          <Pane className="min-h-48 flex-1">
            <CodeEditor
              key={view}
              className="h-full"
              language="ini"
              value={view === "after" ? data.after : data.before}
              revealLine={view === "after" && data.exists ? data.line : undefined}
              readOnly
            />
          </Pane>
        </>
      ) : null}
    </SidePanel>
  )
}
