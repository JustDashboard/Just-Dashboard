"use client"

import { useEffect, useState } from "react"
import { notify } from "@/lib/toast"
import { get, put } from "@/lib/api"
import type { SitePage, SitePageName } from "@/lib/types"
import { CodeEditor } from "@/components/code-editor"
import { Modal } from "@/components/modal"
import { Pane } from "@/components/panel"
import { ErrorState, LoadingPanel } from "@/components/state"
import { Button } from "@/components/ui/button"

/** What each page is called where the form lists it. */
export const PAGE_LABEL: Record<SitePageName, string> = {
  maintenance: "Maintenance page",
  "404": "404 Not found",
  "502": "502 Bad gateway",
  "503": "503 Unavailable",
  "504": "504 Timed out",
}

/**
 * One of a site's pages, edited beside what a visitor would see. Rendered
 * with a key per site and page, so another page never inherits this one's
 * buffer.
 *
 * The preview is an iframe with every sandbox permission withheld, so a
 * script in the page cannot run here, in the dashboard's origin, whatever it
 * does once nginx serves it on the site. A page is a file nginx reads on
 * every response, so saving it needs no reload.
 */
export function PageEditor({
  open,
  onOpenChange,
  site,
  page,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  site: string
  page: SitePageName
}) {
  const path = `/proxy/sites/${encodeURIComponent(site)}/pages/${page}`
  const [read, setRead] = useState<SitePage | null>(null)
  const [error, setError] = useState<Error | null>(null)
  const [content, setContent] = useState("")
  const [saving, setSaving] = useState(false)
  const [reads, setReads] = useState(0)

  useEffect(() => {
    if (!open) return
    const controller = new AbortController()
    get<SitePage>(path, undefined, controller.signal)
      .then((p) => {
        setRead(p)
        setContent(p.content)
        setError(null)
      })
      .catch((err: Error) => {
        if (!controller.signal.aborted) setError(err)
      })
    return () => controller.abort()
  }, [open, path, reads])

  const save = async () => {
    setSaving(true)
    try {
      const saved = await put<SitePage>(path, { content })
      setRead(saved)
      notify.success(`${PAGE_LABEL[page]} saved`)
      onOpenChange(false)
    } catch (err) {
      notify.error("Could not save the page", err)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      size="xl"
      title={`${PAGE_LABEL[page]} · ${site}`}
      description="Edit the HTML nginx answers with, beside a preview of it."
      initialFocus="body"
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            onClick={save}
            pending={saving}
            disabled={!read || saving || content === read.content}
          >
            Save page
          </Button>
        </>
      }
    >
      {error ? (
        <ErrorState error={error} onRetry={() => setReads((n) => n + 1)} />
      ) : !read ? (
        <LoadingPanel />
      ) : (
        <div className="flex flex-col gap-3">
          <p className="text-hint text-muted-foreground">
            {read.custom
              ? "The site's own page."
              : "The page the dashboard ships. Saving keeps a copy of it for this site."}
          </p>
          <div className="grid gap-3 md:grid-cols-2">
            <Pane className="h-80 md:h-[28rem]">
              <CodeEditor
                className="h-full"
                language="html"
                value={content}
                onChange={setContent}
              />
            </Pane>
            <Pane className="h-80 md:h-[28rem]">
              <iframe
                title={`Preview of ${PAGE_LABEL[page]}`}
                sandbox=""
                srcDoc={content}
                className="size-full bg-background"
              />
            </Pane>
          </div>
        </div>
      )}
    </Modal>
  )
}
