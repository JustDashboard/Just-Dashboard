"use client"

import { useEffect, useRef } from "react"
import { usePathname, useRouter } from "next/navigation"
import { useAuth } from "@/hooks/use-auth"
import { MetricsStream, useHostName } from "@/hooks/use-metrics"
import { SelfUpdateProvider } from "@/hooks/use-self-update"
import { IconAction } from "@/components/icon-action"
import { AppSidebar } from "@/components/shell/app-sidebar"
import { Logo, LogoStacked } from "@/components/logo"
import { tabTitle } from "@/components/shell/nav"
import { useTabName } from "@/components/shell/tab-title"
import { SidebarInset, SidebarProvider, SidebarTrigger } from "@/components/ui/sidebar"
import { CommandPaletteProvider, useCommandPalette } from "@/components/shell/command-palette"
import { MagnifyingGlass } from "@/components/icons"
import { Sweep } from "@/components/loading"
import { NavScopeProvider } from "@/components/shell/nav-scope"
import { SavedFolderColours } from "@/components/files/folder-colour"
import { WorkspaceCommandsProvider } from "@/components/workspace/commands"
import { NetworkChangeConfirmation } from "@/components/network/change-confirmation"
import { HeldSessionsHost, HeldSessionsProvider } from "@/components/remote-desktop/held-sessions"
import { AgentDock } from "@/components/assistant/agent/dock"
import { AskAboutPage } from "@/components/assistant/ask-about"
import { HubLinkProvider, useShellHub } from "@/hooks/use-hub-link"
import { signInAddress } from "@/lib/sign-in-return"
import { waitingReason } from "@/lib/session-probe"
import { ServerSwitcher } from "@/components/hub-link/server-switcher"

/**
 * The page's name in the tab is what tells a row of dashboard tabs apart. This
 * layout is a client component, so it cannot export `metadata`; React carries
 * the element to the head instead. The root layout declares no title for that
 * reason: Next would write its own over a `document.title`, and beside this one
 * would be the first of two.
 *
 * The address names the page and the page names the item: a detail page's
 * heading, or what it says with `useTabTitle`, goes before the section, so two
 * tabs on two containers read differently (`tabTitle`, `components/shell/tab-title.tsx`).
 *
 * The hub link's provider sits above the title because a linked server names
 * itself in the tab, and above the shell because the rail, the phone's strip
 * and the palette all draw the hub from it. Unlinked it asks nothing and gives
 * nothing, and every one of them is what it was.
 *
 * The name in the tab is this server's own host name, the Overview's title,
 * never the name the hub lists it under: that one is the hub's text, and a hub
 * that is taken over could make it read "Session expired - sign in"
 * (hub-link-protocol §8.10). The hub only decides whether a name is shown.
 */
export default function DashboardLayout({ children }: { children: React.ReactNode }) {
  return (
    <HubLinkProvider>
      <TabTitle />
      <DashboardShell>{children}</DashboardShell>
    </HubLinkProvider>
  )
}

function TabTitle() {
  const pathname = usePathname()
  const hub = useShellHub()
  const host = useHostName()
  const name = useTabName()
  return <title>{tabTitle(pathname, hub ? (host ?? undefined) : undefined, name)}</title>
}

function DashboardShell({ children }: { children: React.ReactNode }) {
  const { status, loading, stalled, sessionEnded } = useAuth()
  const hub = useShellHub()
  const router = useRouter()

  // Redirecting here is a convenience, not a security control: every API call
  // behind this shell is independently authenticated by the server. An
  // account that has not been asked what the dashboard is for is asked first,
  // wherever it was headed; answering or skipping ends that for good.
  //
  // A browser that arrives signed out is sent to sign in with the page it
  // asked for carried along (`signInAddress`): a link from an alert, opened
  // after the session expired overnight, still ends on the page it named.
  // One that was signed in here and no longer is has signed out or been
  // signed out, and whoever signs in next starts from the Overview.
  const onboarding = Boolean(status?.user?.needsOnboarding)
  const signedInHere = useRef(false)
  useEffect(() => {
    if (status?.authenticated) signedInHere.current = true
    if (!loading && !status?.authenticated)
      router.replace(
        sessionEnded
          ? "/login?reason=session"
          : signedInHere.current
            ? "/login"
            : signInAddress(window.location.pathname + window.location.search),
      )
    else if (onboarding) router.replace("/welcome")
  }, [loading, status, sessionEnded, onboarding, router])

  if (loading || !status?.authenticated || onboarding)
    return <ShellSplash waiting={stalled === undefined ? undefined : waitingReason(stalled)} />

  // A recovery probe can discover another tab's sign-in without leaving this
  // route. Recreate mounted editors before the new account can reuse their buffers.
  return (
    <WorkspaceCommandsProvider key={status.user?.id}>
      <CommandPaletteProvider>
        {/* One poll of the dashboard's own version for the whole shell: the
          sidebar notice and the Updates page are on screen together, and the
          provider is also what keeps the poll alive across the moment the
          backend restarts itself during an upgrade. */}
        <SelfUpdateProvider>
          {/* A section whose pages the route cannot name on its own — the
            databases, one deployment — hands the rail its own panel through
            here, so the rail never fetches a thing to draw a list the page
            below it already holds. */}
          <NavScopeProvider>
            {/* Remote Desktop's sessions outlive the page that shows them, so
              a look at another page does not hang up on the computer. */}
            <HeldSessionsProvider>
              <SidebarProvider
                desktopCollapsible={false}
                style={{ "--sidebar-width": "15.5rem" } as React.CSSProperties}
                className="bg-ground"
              >
                <SkipToContent />
                {/* Owns the metrics socket for the whole shell, so the Overview and
              Metrics charts keep filling while you are on another page. Renders
              nothing. */}
                <MetricsStream />
                <AppSidebar />
                {/* From `md` up the page is a sheet laid on the window: inset 8px
              from its top, right and foot, with its own edge and corners,
              while the rail stays on the window's ground beside it. The eye
              always knows where the work is, and the navigation steps back
              (Just Hub's frame). A phone has no room to spare for a margin,
              so there the sheet is the screen. */}
                <div className="relative flex h-svh w-full min-w-0 flex-1 flex-col overflow-hidden bg-background md:my-2 md:mr-2 md:h-[calc(100svh-1rem)] md:rounded-xl md:border md:border-line">
                  {/* The bar that used to run across the top of every page is gone
              (git history has it, as `components/top-bar.tsx`). It carried a
              breadcrumb every page already states in its own header. Below
              `md` the rail is a sheet with nothing left to open it, so this
              one strip stays: the trigger, and the name of the product it
              belongs to — or, on a server linked to a hub, the server it is,
              which is the switcher to the others.

              It stands beside `<main>`, not in it: it is the phone's banner,
              as the rail's head is the desk's, and a screen reader asked for
              the main content used to be handed the product's name first.

              Its three controls are the only navigation a phone has, so each
              is a finger's 44px box in a 48px strip; the strip's own padding
              is cut to keep the glyphs where a 32px control put them. */}
                  <header className="flex h-12 shrink-0 items-center gap-0.5 border-b bg-ground px-1 md:hidden">
                    <SidebarTrigger className="size-11 text-muted-foreground" />
                    {hub ? <ServerSwitcher hub={hub} strip /> : <Logo />}
                    {/* A phone has no ⌘K and the rail's search field is inside
                      a closed sheet, so search had no way in at all here; nor
                      had asking the assistant about the page. */}
                    <div className="ml-auto flex items-center">
                      <AskAboutPage className="h-11" />
                      <MobileSearch />
                    </div>
                  </header>
                  <SidebarInset id={CONTENT_ID} className="min-h-0 overflow-hidden">
                    {/* The scroll lives here rather than on the document, which lets a
              page ask for the remaining height (`<Page fill>`) instead of
              growing past the viewport. */}
                    <div
                      data-workspace-shell-scroll
                      className="min-h-0 min-w-0 flex-1 overflow-x-hidden overflow-y-auto"
                    >
                      {/* A folder coloured in Files is that colour on every page
                  that draws it, not only on the one that coloured it. */}
                      <NetworkChangeConfirmation />
                      <SavedFolderColours>{children}</SavedFolderColours>
                      <HeldSessionsHost />
                      {/* The way back to a conversation with the assistant, from
                      whatever page a shortcut of its led to. */}
                      <AgentDock />
                    </div>
                  </SidebarInset>
                </div>
              </SidebarProvider>
            </HeldSessionsProvider>
          </NavScopeProvider>
        </SelfUpdateProvider>
      </CommandPaletteProvider>
    </WorkspaceCommandsProvider>
  )
}

function MobileSearch() {
  const palette = useCommandPalette()
  return (
    <IconAction
      variant="ghost"
      size="icon"
      label="Search"
      className="size-11 text-muted-foreground [&_svg:not([class*='size-'])]:size-4"
      onClick={palette.open}
    >
      <MagnifyingGlass className="size-4" />
    </IconAction>
  )
}

/** The `<main>` every page under the shell is drawn in. */
const CONTENT_ID = "content"

/**
 * The first stop on every page: past the rail's fifty links to the page
 * itself. A keyboard had no way round the rail, so reaching a page's first
 * control was a walk through all of it on every navigation (WCAG 2.4.1).
 *
 * Out of sight above the window until the keyboard is on it, then a popover
 * at the window's corner over whatever is there. It moves the focus rather than the address:
 * a `#content` left in the URL would be carried into every link copied from
 * the page.
 *
 * The focus goes to the page's heading, so a screen reader says which page
 * this is and the next Tab is the page's first control. The heading can hold
 * it only for that moment: `<main>` is never made focusable, because a click
 * anywhere in a focusable `<main>` would move the focus to it, and the
 * browser scrolls with the keyboard from the focused element outwards, which
 * would take Page Down away from the pane that was clicked.
 */
function SkipToContent() {
  return (
    <a
      href={`#${CONTENT_ID}`}
      data-slot="skip-link"
      onClick={(event) => {
        const main = document.getElementById(CONTENT_ID)
        const target = main?.querySelector<HTMLElement>("h1") ?? main
        if (!target) return
        event.preventDefault()
        target.tabIndex = -1
        target.addEventListener("blur", () => target.removeAttribute("tabindex"), { once: true })
        target.focus()
      }}
      // Held above the window's edge rather than clipped to a pixel, so it is
      // its real size when the focus brings it down: a finger's height, since
      // a phone with a keyboard attached reaches it too.
      className="fixed top-2 left-2 z-overlay inline-flex h-11 -translate-y-16 items-center rounded-md border bg-popover px-4 text-body font-medium text-popover-foreground shadow-md focus-ring focus:translate-y-0"
    >
      Skip to content
    </a>
  )
}

/**
 * What fills the window while the session probe is in flight.
 *
 * A bare spinner on an empty background reads as a broken page; the name says
 * the app is starting, which is what is actually happening — and it is one
 * paint, not a layout that then reflows into the shell.
 *
 * A probe that has failed and is being asked again (a drained limiter, a
 * backend restarting, no network yet) keeps this up rather than sending a
 * signed-in person to sign in, and `waiting` says why it is still here: a
 * splash with no word under it, for half a minute, is the broken page again.
 */
function ShellSplash({ waiting }: { waiting?: string }) {
  return (
    <main className="auth-backdrop flex min-h-svh flex-col items-center justify-center gap-4 bg-background">
      <div className="flex flex-col items-center gap-2.5">
        <LogoStacked className="h-18" />
        <Sweep rounded label="Starting the dashboard" className="h-0.5 w-24" />
      </div>
      {waiting && (
        <p role="status" className="max-w-xs px-4 text-center text-sm text-muted-foreground">
          {waiting}
        </p>
      )}
    </main>
  )
}
