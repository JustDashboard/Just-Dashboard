"use client"

import { useState } from "react"
import { CheckCircle, RefreshClockwise, Warning } from "@/components/icons"
import { ApiError, get, post } from "@/lib/api"
import { plural, relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { PendingFile, ProxyPending, ProxyReload, ProxyValidation } from "@/lib/types"
import { Disclosure } from "@/components/form"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { failureHeadline } from "@/components/proxy/site-outcome"
import {
  KEPT_LOAD,
  changeVerb,
  configName,
  keptLoad,
  loadKnown,
  loadedSince,
  outputHeadline,
  pendingTitle,
} from "@/components/proxy/site-pending"

/** How many files the strip names before folding the rest. */
const SHOWN = 5

/**
 * What on disk nginx has not loaded, with the two commands that answer it:
 * Test config, which says whether a reload would be taken, and Reload nginx,
 * which says whether it was. A reload is only sent: nginx loads what it reads
 * or keeps what it had, so the strip reads nginx again after it and says
 * which happened.
 */
export function PendingStrip({
  pending,
  nginxDir,
  admin,
  onChanged,
  onOutput,
}: {
  pending: ProxyPending
  nginxDir: string | undefined
  admin: boolean
  /** Read the list and what is pending again, after a test or a reload. */
  onChanged: () => void
  /** Show the whole of what nginx printed. */
  onOutput: (title: string, output: string) => void
}) {
  const [busy, setBusy] = useState<"test" | "reload" | "">("")
  if (!loadKnown(pending)) return null
  const count = pending.files.length
  if (count === 0 && !pending.problem) return null
  const loaded = pending.lastReload
  const showOutput = (output: string) => ({
    label: "Show nginx output",
    onClick: () => onOutput("nginx -t", output),
  })

  const test = async () => {
    setBusy("test")
    try {
      const res = await post<ProxyValidation>("/proxy/test", { kind: "nginx" })
      if (res.valid) {
        notify.success("nginx accepts the configuration on disk", {
          description:
            count > 0
              ? `A reload would put ${count === 1 ? "the change" : `the ${count} changes`} live.`
              : undefined,
          action: res.warnings ? showOutput(res.output) : undefined,
        })
      } else {
        notify.error("nginx refuses the configuration on disk", undefined, {
          description: failureHeadline(res),
          action: showOutput(res.output),
        })
      }
    } catch (err) {
      notify.error("Could not test the configuration", err)
    } finally {
      setBusy("")
      // What nginx refuses, or has not loaded, may have moved on since the
      // strip was read.
      onChanged()
    }
  }

  const reload = async () => {
    setBusy("reload")
    const before = pending.generation
    try {
      await post<ProxyReload>("/proxy/reload", { kind: "nginx" })
    } catch (err) {
      // A refused reload answers with nginx's whole test output.
      if (err instanceof ApiError && err.code === "invalid_config") {
        notify.error("nginx refused the reload", undefined, {
          description: outputHeadline(err.message),
          action: showOutput(err.message),
        })
      } else {
        notify.error("Could not reload nginx", err)
      }
      setBusy("")
      return
    }
    try {
      const after = await get<ProxyPending>("/proxy/pending", { after: before })
      if (loadedSince(before, after)) {
        const left = after.files.length
        notify.success("nginx reloaded", {
          description:
            left === 0
              ? count === 1
                ? "The change is live."
                : "The changes are live."
              : `${plural(left, "change")} on disk ${left === 1 ? "is" : "are"} still not live.`,
        })
      } else {
        notify.warning(KEPT_LOAD, {
          description: keptLoad(after.lastReload ?? loaded),
          duration: 12_000,
        })
      }
    } catch {
      notify.success("nginx reloaded", {
        description: "Whether it loaded the changes could not be read back.",
      })
    } finally {
      setBusy("")
      onChanged()
    }
  }

  const shown = count > SHOWN + 1 ? pending.files.slice(0, SHOWN) : pending.files
  const folded = pending.files.slice(shown.length)
  return (
    <div role="status">
      <Notice
        tone="warning"
        icon={Warning}
        title={count > 0 ? pendingTitle(count) : "nginx refuses the configuration on disk"}
      >
        <p>
          nginx is running the configuration it loaded{" "}
          <time dateTime={loaded} title={timestamp(loaded)}>
            {relativeTime(loaded)}
          </time>
          .
        </p>
        {pending.problem && (
          <p className="break-words">
            It refuses the configuration on disk, so a reload is turned away until this is fixed:{" "}
            <span className="font-mono text-foreground">{pending.problem}</span>
          </p>
        )}
        {count > 0 && (
          <ul aria-label="Changes not live" className="flex min-w-0 flex-col gap-0.5">
            {shown.map((file) => (
              <PendingLine key={file.path} file={file} nginxDir={nginxDir} />
            ))}
          </ul>
        )}
        {folded.length > 0 && (
          <Disclosure quiet summary={`${folded.length} more`}>
            <ul aria-label="More changes not live" className="flex min-w-0 flex-col gap-0.5">
              {folded.map((file) => (
                <PendingLine key={file.path} file={file} nginxDir={nginxDir} />
              ))}
            </ul>
          </Disclosure>
        )}
        {admin && (
          <div className="mt-1.5 flex flex-wrap gap-2">
            <Button
              size="xs"
              variant="outline"
              onClick={test}
              pending={busy === "test"}
              disabled={busy !== ""}
            >
              <CheckCircle className="size-3.5" />
              {busy === "test" ? "Testing…" : "Test config"}
            </Button>
            {!pending.problem && (
              <Button
                size="xs"
                variant="outline"
                onClick={reload}
                pending={busy === "reload"}
                disabled={busy !== ""}
              >
                <RefreshClockwise className="size-3.5" />
                {busy === "reload" ? "Reloading…" : "Reload nginx"}
              </Button>
            )}
          </div>
        )}
      </Notice>
    </div>
  )
}

/** One file nginx has not loaded: where it is, and what happened to it when. */
function PendingLine({ file, nginxDir }: { file: PendingFile; nginxDir: string | undefined }) {
  return (
    <li className="min-w-0 break-all">
      <span className="font-mono text-foreground">{configName(file.path, nginxDir)}</span>{" "}
      {changeVerb(file)}
      {file.modified && (
        <>
          {" "}
          <time dateTime={file.modified} title={timestamp(file.modified)}>
            {relativeTime(file.modified)}
          </time>
        </>
      )}
      {file.change === "removed" && ", still served until nginx reloads"}
    </li>
  )
}
