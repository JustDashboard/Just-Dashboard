"use client"

import { useRef, useState } from "react"
import Link from "next/link"
import { ArrowRight, Box } from "@/components/icons"
import type { ContainerDetail } from "@/lib/types"
import { cn } from "@/lib/utils"
import { Pane, PaneFooter } from "@/components/panel"
import { ProductGlyph, containerProduct, hasProductLogo } from "@/components/product-logo"
import { XtermPane, type XtermActions } from "@/components/xterm-pane"
import { Button } from "@/components/ui/button"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { execQuery, runsAsRoot } from "@/components/deploy/console-session"

/**
 * The first things anybody types in a container they did not build: who am I,
 * what is here, what is running, how full is it, what is it built on. Each is
 * read-only, and each is typed into the shell as though from the keyboard, so
 * its output stays in the scrollback with the rest of the session.
 *
 * `env` is deliberately not one of them. The Environment tab keeps
 * credential-shaped values behind a Reveal, and a button that printed every
 * one of them into a terminal — and into its saved scrollback — would undo
 * that with one press.
 */
const LOOK_AROUND = [
  { command: "id", hint: "Which account the shell runs as, and its groups" },
  { command: "ls -la", hint: "What is in the working directory" },
  { command: "ps aux", hint: "What is running inside the container" },
  { command: "df -h", hint: "How full each filesystem it sees is" },
  { command: "cat /etc/os-release", hint: "What the image is built on" },
] as const

/**
 * A shell *inside* the container — which is the whole point of it, and the
 * thing most easily mistaken for the Terminal page.
 *
 * The two answer different questions. This one lands wherever the image says:
 * as the image's USER, in its WORKDIR. For most images that is root in `/` or
 * `/app`, and that is not a bug to be fixed — a container shell that quietly
 * became a host login would leave no way to look inside a container at all.
 * The Terminal page is the host; this is the box running on it.
 *
 * Docker already honours the image's user by default, so "default" sends no
 * user at all rather than guessing one. Root is offered because the common
 * reason to open this at all is that something needs installing or reading in
 * an image that deliberately runs unprivileged.
 *
 * It is drawn the way a project's Console draws the same shell: one working
 * region with a single strip that says where you are — the container as its
 * product, `account@container` with root in the warning hue, since root inside
 * the box is the one identity worth noticing, and the working directory — over
 * the terminal, and a foot holding the commands worth typing first beside the
 * reminder that this is not the host. The grey sentence and the loose toggle
 * that stood above the terminal were chrome outside the region they described.
 */
export function ContainerShellTab({ detail }: { detail: ContainerDetail }) {
  const [asRoot, setAsRoot] = useState(false)
  const actions = useRef<XtermActions | null>(null)

  // Most images declare no USER, so the container already runs as root and
  // there is no second account to offer. The toggle used to be drawn anyway,
  // from `detail.user || "root"` — which rendered two buttons both labelled
  // "root" that switched between a request with no user and a request for
  // root, i.e. between the same thing twice.
  const imageUser = detail.user.trim()
  const imageRoot = runsAsRoot("default", imageUser)
  const root = asRoot || imageRoot
  const account = root ? "root" : imageUser.split(":")[0]
  const product = containerProduct(detail)

  return (
    <Pane className="h-full min-h-[28rem]">
      <XtermPane
        // Keyed on the account, so switching it opens a new exec rather than
        // leaving you in the previous one with a stale label above it.
        key={account}
        flush
        path={`/docker/containers/${detail.id}/exec`}
        // No user at all when the image's own is wanted: Docker already
        // honours it, and naming it would override a `user:group` form with
        // just the user half.
        query={execQuery({ shell: "auto", runAs: asRoot && !imageRoot ? "root" : "default" })}
        className="min-h-0 flex-1"
        actionsRef={actions}
        headerContent={
          <>
            {hasProductLogo(product) ? (
              <ProductGlyph id={product} className="mx-1" />
            ) : (
              <Box aria-hidden className="mx-1 size-3.5 shrink-0 text-muted-foreground" />
            )}
            <span
              className="min-w-0 truncate font-mono text-xs"
              title={
                imageRoot
                  ? "root — this image sets no other user"
                  : `${account} inside ${detail.name}`
              }
            >
              <span className={cn("font-medium", root ? "text-warning" : "text-foreground")}>
                {account}
              </span>
              <span className="text-muted-foreground">@{detail.name}</span>
            </span>
            <span
              className="min-w-0 truncate font-mono text-xs text-muted-foreground max-sm:hidden"
              title="The image's working directory, where the shell starts"
            >
              {detail.workingDir || "/"}
            </span>
            {/* Only an image that runs as someone else has a second account
                to offer; the choice sits at the strip's end, beside the
                terminal's own controls. */}
            {!imageRoot && (
              <ToggleGroup
                type="single"
                size="sm"
                variant="outline"
                aria-label="Run as"
                className="mr-1 ml-auto shrink-0"
                value={asRoot ? "root" : "default"}
                onValueChange={(value) => value && setAsRoot(value === "root")}
              >
                <ToggleGroupItem value="default" className="h-7 px-2 font-mono text-hint">
                  {imageUser.split(":")[0]}
                </ToggleGroupItem>
                <ToggleGroupItem value="root" className="h-7 px-2 font-mono text-hint">
                  root
                </ToggleGroupItem>
              </ToggleGroup>
            )}
          </>
        }
      />
      <PaneFooter className="gap-x-3">
        {/* Words, as the control keys above them are: the command itself is
            the label, so what a press types is read before it is pressed. */}
        <div className="flex min-w-0 flex-1 [scrollbar-width:none] items-center gap-0.5 overflow-x-auto [&::-webkit-scrollbar]:hidden">
          {LOOK_AROUND.map(({ command, hint }) => (
            <Tooltip key={command}>
              <TooltipTrigger asChild>
                <Button
                  size="xs"
                  variant="ghost"
                  aria-label={`Run ${command}`}
                  className="h-6 shrink-0 rounded-sm px-1.5 font-mono text-hint font-normal text-muted-foreground hover:text-foreground"
                  onClick={() => actions.current?.run(command)}
                >
                  {command}
                </Button>
              </TooltipTrigger>
              <TooltipContent>{hint}</TooltipContent>
            </Tooltip>
          ))}
        </div>
        <p className="flex shrink-0 items-center gap-1.5 text-hint text-muted-foreground">
          Inside <span className="font-mono text-foreground">{detail.name}</span>, not the host
          <Link
            href="/terminal"
            className="inline-flex items-center gap-1 rounded-sm font-medium focus-ring hover:text-foreground"
          >
            Terminal <ArrowRight className="size-3" />
          </Link>
        </p>
      </PaneFooter>
    </Pane>
  )
}
