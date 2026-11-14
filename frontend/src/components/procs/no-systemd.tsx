"use client"

import { Logs, Servers, Stopwatch } from "@/components/icons"
import { MissingScreen, type MissingGain } from "@/components/missing-thing"
import { missingFromError, noSystemd, type SystemdReader } from "@/components/missing-thing-model"
import { ErrorState } from "@/components/state"

/** What systemd's pages draw on a server that has it. */
const GAINS: MissingGain[] = [
  {
    icon: Servers,
    title: "Services",
    text: "Every unit, what it is using and what just happened to it, started and stopped from here.",
  },
  {
    icon: Stopwatch,
    title: "Timers",
    text: "What runs on a schedule, when it last ran and when it runs next.",
  },
  {
    icon: Logs,
    title: "The journal",
    text: "Each unit's own lines, live and searched.",
  },
]

/**
 * Where there is no systemd to ask: the Services list, a service's page and
 * Scheduled's Timers say it the same way, with the thing drawn as itself.
 * They were an icon over one grey sentence, a red box with systemctl's
 * refusal, and a grey note, on three pages that share the one cause.
 *
 * Nothing here can start systemd, so the steps are the page that does work
 * without it (Processes) and the mount the dashboard's own container needs
 * where the host has systemd and the container was started without it.
 */
export function NoSystemd({
  reader,
  size = "page",
  raw,
  className,
}: {
  /** What this page reads from systemd, for the sentence. */
  reader: SystemdReader
  size?: "page" | "block"
  /** systemctl's own words, where a read carried them. */
  raw?: string
  className?: string
}) {
  return (
    <MissingScreen
      missing={noSystemd(reader, raw)}
      size={size}
      gains={size === "page" ? GAINS : undefined}
      gainsLabel="What these pages show on a server with systemd"
      className={className}
    />
  )
}

/**
 * A read of systemd that failed: the same screen where the answer is that
 * there is no systemd (a host with systemctl and another init answers the
 * list with systemctl's own refusal), and the failure it is otherwise.
 */
export function NoSystemdOrError({
  error,
  reader,
  size = "page",
  onRetry,
  className,
  errorClassName,
}: {
  error: Error
  reader: SystemdReader
  size?: "page" | "block"
  onRetry?: () => void
  /** The screen's. */
  className?: string
  /** The failure box's, which is spaced as the box it replaced was. */
  errorClassName?: string
}) {
  const missing = missingFromError(error, { systemd: reader })
  if (missing?.reason !== "no-systemd") {
    return <ErrorState error={error} onRetry={onRetry} className={errorClassName} />
  }
  return <NoSystemd reader={reader} size={size} raw={missing.raw} className={className} />
}
