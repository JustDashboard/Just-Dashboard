"use client"

import { useRef } from "react"
import { GitHubMark } from "@/components/icons"
import { Status } from "@/components/status-dot"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { WireHost, WireMark, WirePlaceholder } from "@/components/deploy/wire"

/**
 * The GitHub App while it is not connected, as the Credentials page draws it,
 * small: the App and this server with the line between them — dotted before
 * the App exists, drawn and still once it does and no account has installed
 * it. It was a row of text that said "Disconnected" beside the word Connect,
 * on the one screen where the reader decides whether to connect it; the
 * picture says what would be connected to what before a word is read.
 *
 * The marks stand at the two ends with their words under them, so the line
 * crosses nothing at any width. Connected, the App is the account's own row
 * again, with its repositories counted.
 */
export function GitHubAppPicture({ stage }: { stage: "create" | "install" }) {
  const container = useRef<HTMLDivElement>(null)
  const app = useRef<HTMLDivElement>(null)
  const server = useRef<HTMLDivElement>(null)
  const created = stage === "install"
  return (
    <div
      ref={container}
      role="group"
      aria-label="GitHub App"
      data-slot="github-app-picture"
      className="relative max-w-sm py-3"
    >
      <AnimatedBeam containerRef={container} fromRef={app} toRef={server} still dashed={!created} />
      <div className="flex items-start justify-between gap-4">
        <div className="flex min-w-0 flex-col items-start gap-2.5">
          <div ref={app} className="relative z-10 flex">
            {created ? (
              <WireMark tone="logo" size="md">
                <GitHubMark />
              </WireMark>
            ) : (
              <WirePlaceholder size="md">
                <GitHubMark className="size-5" />
              </WirePlaceholder>
            )}
          </div>
          <div className="min-w-0">
            <p className="eyebrow">GitHub App</p>
            <Status
              tone={created ? "warning" : "unknown"}
              label={created ? "Uninstalled" : "Disconnected"}
            />
            <p className="text-hint leading-snug text-muted-foreground">
              {created ? "Waiting for an account to install it" : "Not set up on this server"}
            </p>
          </div>
        </div>
        <div className="flex min-w-0 flex-col items-end gap-2.5 text-right">
          <div ref={server} className="relative z-10 flex">
            <WireHost size="md" />
          </div>
          <div className="min-w-0">
            <p className="eyebrow">This server</p>
            <p className="text-hint leading-snug text-muted-foreground">Hears every push</p>
          </div>
        </div>
      </div>
    </div>
  )
}
