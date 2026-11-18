"use client"

import { useEffect, useRef } from "react"
import { MainMenu, TTDDialog, TTDDialogTrigger, WelcomeScreen } from "@excalidraw/excalidraw"
import { FloppyDisk, Plus, Sparkles } from "@/components/icons"
import { post } from "@/lib/api"
import { LogoGlyph } from "@/components/logo"

/**
 * Excalidraw's menu, its empty-canvas screen and its text-to-diagram dialog,
 * in this dashboard's terms. The stock menu ends in Excalidraw's GitHub, X
 * and Discord links and saves to a file; here a drawing lives on the server.
 */
export function BoardMenus({
  canWrite,
  onSave,
  onAddServerItem,
  onDescribe,
}: {
  canWrite: boolean
  onSave: () => void
  onAddServerItem: () => void
  onDescribe: () => void
}) {
  const marker = useRef<HTMLSpanElement>(null)
  useNamedChrome(marker)
  return (
    <>
      <span ref={marker} hidden />
      <MainMenu>
        {canWrite && (
          <MainMenu.Item icon={<FloppyDisk />} shortcut="Ctrl+S" onSelect={onSave}>
            Save to this server
          </MainMenu.Item>
        )}
        {canWrite && <MainMenu.DefaultItems.LoadScene />}
        <MainMenu.DefaultItems.Export />
        <MainMenu.DefaultItems.SaveAsImage />
        <MainMenu.DefaultItems.CommandPalette />
        <MainMenu.DefaultItems.SearchMenu />
        <MainMenu.DefaultItems.Help />
        {canWrite && <MainMenu.DefaultItems.ClearCanvas />}
        <MainMenu.Separator />
        <MainMenu.DefaultItems.ChangeCanvasBackground />
      </MainMenu>
      {canWrite && (
        <WelcomeScreen>
          <WelcomeScreen.Hints.MenuHint />
          <WelcomeScreen.Hints.ToolbarHint />
          <WelcomeScreen.Hints.HelpHint />
          <WelcomeScreen.Center>
            <WelcomeScreen.Center.Logo>
              <LogoGlyph className="h-12 w-auto text-brand" />
            </WelcomeScreen.Center.Logo>
            <WelcomeScreen.Center.Heading>
              Draw this server: what runs on it, and how it connects.
            </WelcomeScreen.Center.Heading>
            <WelcomeScreen.Center.Menu>
              <WelcomeScreen.Center.MenuItem icon={<Plus />} onSelect={onAddServerItem}>
                Add a project, database or this server
              </WelcomeScreen.Center.MenuItem>
              <WelcomeScreen.Center.MenuItem icon={<Sparkles />} onSelect={onDescribe}>
                Describe a diagram
              </WelcomeScreen.Center.MenuItem>
              <WelcomeScreen.Center.MenuItemLoadScene />
              <WelcomeScreen.Center.MenuItemHelp />
            </WelcomeScreen.Center.Menu>
          </WelcomeScreen.Center>
        </WelcomeScreen>
      )}
      {canWrite && <TTDDialog onTextSubmit={describe} />}
      {canWrite && <TTDDialogTrigger />}
    </>
  )
}

/**
 * Excalidraw's own chrome, named for a screen reader. Three things in it are
 * drawn by the library with no prop to reach them (A11Y-22):
 *
 * - the menu's trigger is an icon with no name at all;
 * - the strip of zoom and undo controls is a `<footer role="contentinfo">`
 *   inside the page's `<main>`, a second page footer nested in the content;
 * - the zoom figure is labelled "Reset zoom" while it reads "100%", so asking
 *   for it by the words on it finds nothing. Its `title` still says what a
 *   press does.
 *
 * The library draws them after it mounts and again when its layout changes (a
 * phone, zen mode, view mode), so they are set whenever its tree changes.
 * React leaves an attribute alone once its prop stops changing, which is why
 * setting them from outside holds.
 */
function useNamedChrome(marker: React.RefObject<HTMLElement | null>) {
  useEffect(() => {
    const root =
      marker.current?.closest<HTMLElement>(".excalidraw") ??
      document.querySelector<HTMLElement>(".excalidraw")
    if (!root) return
    const name = () => {
      const trigger = root.querySelector(".main-menu-trigger")
      if (trigger && !trigger.hasAttribute("aria-label")) {
        trigger.setAttribute("aria-label", MENU_LABEL)
      }
      for (const footer of root.querySelectorAll('footer[role="contentinfo"]')) {
        footer.setAttribute("role", "presentation")
      }
      root.querySelector(".reset-zoom-button[aria-label]")?.removeAttribute("aria-label")
    }
    name()
    const observer = new MutationObserver(name)
    observer.observe(root, { childList: true, subtree: true })
    return () => observer.disconnect()
  }, [marker])
}

/** The trigger's name. The head bar's own menu is "Drawing actions"; this one is the canvas's. */
const MENU_LABEL = "Canvas menu"

/**
 * Text to diagram, answered by the provider the database assistant uses: it
 * writes Mermaid, which Excalidraw's dialog previews and turns into shapes.
 */
async function describe(prompt: string) {
  try {
    const { mermaid } = await post<{ mermaid: string }>("/boards/diagram", { prompt })
    return { generatedResponse: mermaid }
  } catch (cause) {
    return { error: cause instanceof Error ? cause : new Error(String(cause)) }
  }
}
