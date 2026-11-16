"use client"

import Link from "next/link"
import { ChartActivity, Copy, Terminal } from "@/components/icons"
import { ApiError } from "@/lib/api"
import { asSentence } from "@/lib/format"
import { useAuth } from "@/hooks/use-auth"
import { copyText } from "@/lib/clipboard"
import { MissingThing } from "@/components/missing-thing"
import { ErrorState } from "@/components/state"
import { Button } from "@/components/ui/button"

/** The terminal the one command is typed in, for an account that has one. */
function OpenTerminal() {
  const { can } = useAuth()
  if (!can("terminal")) return null
  return (
    <Button variant="outline" asChild>
      <Link href="/terminal">
        <Terminal className="size-4" />
        Open a terminal
      </Link>
    </Button>
  )
}

/**
 * The facts under PM2's name or an application's, at one height whatever they
 * hold. A fact set in mono or led by a product's glyph makes the line a pixel
 * taller than plain words do, so the line the page draws while its list is
 * being read was a pixel shorter than the one that replaced it, and everything
 * under it stepped down as the list landed.
 */
export function PM2FactsLine({ children }: { children?: React.ReactNode }) {
  return (
    <span className="flex min-h-[17px] w-full min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
      {children ?? <span>&nbsp;</span>}
    </span>
  )
}

/**
 * A line to type, as a step: the button copies it, and the line stands beside
 * it, because the reader should see what they are about to paste.
 */
function CopyStep({ label, text }: { label: string; text: string }) {
  return (
    <span className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
      <Button size="lg" onClick={() => void copyText(text, "Copied")}>
        <Copy />
        {label}
      </Button>
      <code className="min-w-0 font-mono text-hint break-all text-muted-foreground">{text}</code>
    </span>
  )
}

/**
 * Where no account on this server has PM2. It is an npm package, installed
 * for the account whose applications it will run, so it is not something the
 * server's package manager can put in as it does git or WireGuard: the screen
 * is PM2 drawn as itself, the one line to copy and the terminal it is typed
 * in, the way a server without Docker or systemd is told (`MissingThing`).
 */
export function PM2Missing() {
  return (
    <MissingThing
      thing="pm2"
      icon={ChartActivity}
      title="PM2 is not installed"
      reason="not-installed"
      step={<CopyStep label="Copy the install line" text="npm install -g pm2" />}
      more={<OpenTerminal />}
    >
      It comes from npm, for the account whose applications it runs.
    </MissingThing>
  )
}

/**
 * Where PM2 is installed and no account's daemon is running. PM2 starts its
 * daemon with the first application, as the account that starts it, so there
 * is no daemon to start from here and nothing to list: the page drew an empty
 * band over an empty table that said PM2 "is running". The screen says what
 * is true and names the line that changes it.
 */
export function PM2Idle() {
  return (
    <MissingThing
      thing="pm2"
      icon={ChartActivity}
      title="No PM2 daemon is running"
      step={<CopyStep label="Copy the start line" text="pm2 start app.js" />}
      more={<OpenTerminal />}
    >
      PM2 starts its daemon with the first application, as the account that runs it.
    </MissingThing>
  )
}

/**
 * Where PM2 is installed and its list could not be read: `pm2 jlist` exited
 * with an error, or timed out behind a daemon that is wedged. The page's
 * headline was whatever the command printed ("pm2 jlist: exit status 1"); it
 * says what happened in the product's words, keeps PM2's own folded behind
 * "What the server said", and offers the read again. An answer that is not
 * PM2's — a refusal, a pause the server asked for — keeps the product's own
 * block for it.
 */
export function PM2Unanswered({ error, onRetry }: { error: Error; onRetry: () => void }) {
  if (error instanceof ApiError && error.status < 500) {
    return <ErrorState error={error} onRetry={onRetry} />
  }
  return (
    <div role="alert">
      <MissingThing
        thing="pm2"
        icon={ChartActivity}
        title="PM2 did not answer"
        raw={asSentence(error.message)}
        step={
          <Button size="lg" onClick={onRetry}>
            Try again
          </Button>
        }
      >
        The dashboard asked for its list of applications and got no list back.
      </MissingThing>
    </div>
  )
}
