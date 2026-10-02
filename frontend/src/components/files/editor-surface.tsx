"use client"

import { SidePanel } from "@/components/side-panel"
import { PaneFooter, PaneHeader } from "@/components/panel"

/** The same editor controls occupy a sheet for a quick edit and a pane for a longer visit. */
export function EditorSurface({
  destination,
  title,
  actions,
  footer,
  children,
  ...props
}: React.ComponentProps<typeof SidePanel> & { destination?: boolean }) {
  if (!destination)
    return (
      <SidePanel title={title} actions={actions} footer={footer} {...props}>
        {children}
      </SidePanel>
    )
  return (
    <section
      aria-label="File editor"
      className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden"
    >
      <PaneHeader className="flex-wrap gap-2 px-3 py-2">
        <h1 className="flex min-w-0 flex-1 items-center gap-2 text-title font-semibold">{title}</h1>
      </PaneHeader>
      {actions && (
        <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-hairline bg-surface-header px-3 py-2">
          {actions}
        </div>
      )}
      <div className="flex min-h-0 flex-1 flex-col">{children}</div>
      {footer && (
        <PaneFooter className="flex-wrap justify-end gap-2 px-3 py-2">{footer}</PaneFooter>
      )}
    </section>
  )
}
