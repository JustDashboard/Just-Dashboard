"use client"

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
  return (
    <>
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
