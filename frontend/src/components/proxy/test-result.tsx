"use client"

import { useState } from "react"
import { CheckCircle, Copy, Warning } from "@/components/icons"
import { get, post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import type { ProxyTestRecord, ProxyValidation } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Disclosure } from "@/components/form"
import { Well } from "@/components/panel"
import { ProductLogo } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { useNow } from "@/components/deploy/vocabulary"
import { ConfigEditor } from "@/components/proxy/config-editor"
import { DiagnosticList, type ProxyPlace } from "@/components/proxy/diagnostic-list"
import { engineKind, type ProxyStatus } from "@/components/proxy/proxy-context"
import { engineProduct } from "@/components/proxy/marks"
import { openableFile } from "@/components/proxy/engine-lifecycle"
import {
  engineRoots,
  testedLabel,
  testMeaning,
  testVerdict,
  type TestOrigin,
} from "@/components/proxy/config-test"

/** A test the panel shows: its result, when it began, and what ran it. */
type Shown = { validation: ProxyValidation; checkedAt: number; origin: TestOrigin }

export type ConfigTest = {
  /** The engine as its name reads: nginx, or Caddy. */
  engine: string
  /**
   * The test the server kept last, for the overview's finding: none for an
   * account that may not run one, before the first test, or while it cannot
   * be read.
   */
  last: ProxyTestRecord | undefined
  running: boolean
  /** Runs the test and opens the panel on it. */
  run: () => void
  /** Opens the panel on the kept test, as it was, without running another. */
  showLast: () => void
  /** Opens the panel on a reload's own test: refused, or passed with warnings. */
  showReload: (validation: ProxyValidation) => void
  refreshLast: () => void
  /** The panel, and the editor a line opens, which closes back into it. */
  panel: React.ReactNode
}

/**
 * The engine's config test, in a panel of its own rather than a toast.
 *
 * Test config said "valid" in a toast whatever nginx warned, and cut a
 * failure to four hundred characters. The panel keeps the verdict in three
 * words, a row for each thing the test said with its file a press away, the
 * whole output to copy, and Test again. The server keeps the last test,
 * whichever command ran it, and the overview reads it back so a warning
 * stays in Needs attention until a test comes back clean.
 */
export function useConfigTest({
  status,
  admin,
}: {
  status: ProxyStatus | undefined
  /** Whether the account may run the test, which is also who may read it back. */
  admin: boolean
}): ConfigTest {
  const kind = status && (status.nginx || status.caddy) ? engineKind(status) : undefined
  const engine = status?.nginx ? "nginx" : "Caddy"
  const lastPoll = usePoll(
    (signal) => get<ProxyTestRecord | undefined>("/proxy/test/last", { kind }, signal),
    60_000,
    [kind],
    { enabled: admin && kind !== undefined },
  )
  const [open, setOpen] = useState(false)
  const [shown, setShown] = useState<Shown>()
  const [running, setRunning] = useState(false)
  const [error, setError] = useState<Error>()
  // The place whose file the editor opened, kept once it closes so the
  // panel comes back with the keyboard on its line; and whether the file was
  // saved there, which makes the result on show an old one.
  const [opened, setOpened] = useState<ProxyPlace>()
  const [editing, setEditing] = useState(false)
  const [saved, setSaved] = useState(false)

  const run = async () => {
    if (!kind || running) return
    setOpen(true)
    setRunning(true)
    setError(undefined)
    try {
      const validation = await post<ProxyValidation>("/proxy/test", { kind })
      setShown({ validation, checkedAt: Date.now(), origin: "test" })
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)))
    } finally {
      setRunning(false)
      lastPoll.refresh()
    }
  }

  const show = (next: Shown) => {
    setShown(next)
    setError(undefined)
    setOpen(true)
  }

  const last = lastPoll.error ? undefined : lastPoll.data
  const roots = engineRoots(status)

  const panel = (
    <>
      <TestPanel
        open={open}
        onOpenChange={setOpen}
        status={status}
        engine={engine}
        shown={shown}
        running={running}
        error={error}
        canOpen={(file) => openableFile(file, roots)}
        returnTo={opened}
        onTest={() => void run()}
        onOpenFile={(place) => {
          if (!place.file) return
          setOpen(false)
          setOpened(place)
          setSaved(false)
          setEditing(true)
        }}
      />
      <ConfigEditor
        open={editing}
        onOpenChange={(next) => {
          if (next) return
          setEditing(false)
          // A file saved there has changed what the result on show says
          // about it, so the panel comes back on a new test rather than an
          // old one with the keyboard on a line that may be gone.
          if (saved) void run()
          else setOpen(true)
        }}
        path={opened?.file ?? ""}
        kind={status?.nginx ? "nginx" : "caddy"}
        title={opened?.file?.split("/").pop() ?? "Configuration"}
        initialLine={opened?.line}
        onSaved={() => setSaved(true)}
      />
    </>
  )

  return {
    engine,
    last,
    running,
    run: () => void run(),
    showLast: () => {
      if (last) {
        show({
          validation: last.validation,
          checkedAt: Date.parse(last.checkedAt),
          origin: "last",
        })
      }
    },
    showReload: (validation) => {
      show({ validation, checkedAt: Date.now(), origin: "reload" })
      lastPoll.refresh()
    },
    refreshLast: lastPoll.refresh,
    panel,
  }
}

/**
 * The verdict, what it means for the engine, a row per diagnostic, and the
 * engine's own output folded under them. While a test runs the last one is
 * not left on show under a new heading: the panel says it is testing.
 */
function TestPanel({
  open,
  onOpenChange,
  status,
  engine,
  shown,
  running,
  error,
  canOpen,
  returnTo,
  onTest,
  onOpenFile,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  status: ProxyStatus | undefined
  engine: string
  shown: Shown | undefined
  running: boolean
  error: Error | undefined
  canOpen: (file: string | undefined) => boolean
  returnTo: ProxyPlace | undefined
  onTest: () => void
  onOpenFile: (place: ProxyPlace) => void
}) {
  const result = running || error ? undefined : shown
  const output = result?.validation.output ?? ""
  return (
    <SidePanel
      open={open}
      onOpenChange={onOpenChange}
      width="lg"
      title={
        <>
          <ProductLogo id={engineProduct(status)} size="sm" />
          {`${engine} config test`}
        </>
      }
      description={`What ${engine}'s own config test says about the configuration on disk.`}
      footer={
        <>
          {result && <TestedAt checkedAt={result.checkedAt} live={open} />}
          <Button
            size="sm"
            variant="outline"
            disabled={!output}
            onClick={() => void copyText(output, "Output copied")}
          >
            <Copy className="size-3.5" />
            Copy output
          </Button>
          <Button size="sm" onClick={onTest} pending={running}>
            {running ? "Testing…" : "Test again"}
          </Button>
        </>
      }
    >
      {running ? (
        <div className="space-y-4" aria-busy>
          <Status state="activating" label="Testing…" />
          <LoadingRows rows={3} />
        </div>
      ) : error ? (
        <ErrorState error={error} onRetry={onTest} />
      ) : (
        result && (
          <TestResult
            key={result.checkedAt}
            engine={engine}
            shown={result}
            canOpen={canOpen}
            returnTo={returnTo}
            onOpenFile={onOpenFile}
          />
        )
      )}
    </SidePanel>
  )
}

function TestResult({
  engine,
  shown,
  canOpen,
  returnTo,
  onOpenFile,
}: {
  engine: string
  shown: Shown
  canOpen: (file: string | undefined) => boolean
  returnTo: ProxyPlace | undefined
  onOpenFile: (place: ProxyPlace) => void
}) {
  const { validation, origin } = shown
  const verdict = testVerdict(validation)
  const diagnostics = validation.diagnostics ?? []
  return (
    <div className="animate-rise space-y-4">
      <Notice
        tone={verdict.tone}
        icon={verdict.tone === "success" ? CheckCircle : Warning}
        title={verdict.label}
      >
        {testMeaning(engine, validation, origin)}
      </Notice>
      {diagnostics.length > 0 && (
        <DiagnosticList
          diagnostics={diagnostics}
          canOpen={canOpen}
          returnTo={returnTo}
          onOpen={onOpenFile}
        />
      )}
      {validation.output && (
        <Disclosure quiet summary={`${validation.command || "Test"} output`}>
          <Well className="max-h-64 break-words whitespace-pre-wrap">{validation.output}</Well>
        </Disclosure>
      )}
    </div>
  )
}

/** "tested 14s ago", kept current while the panel is open. */
function TestedAt({ checkedAt, live }: { checkedAt: number; live: boolean }) {
  const now = useNow(1000, live)
  return (
    <span className="mr-auto text-hint text-muted-foreground">
      {testedLabel(checkedAt, Math.max(now, checkedAt))}
    </span>
  )
}
