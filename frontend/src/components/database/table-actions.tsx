"use client"

import {
  AcronymJson,
  CloudUpload,
  Download,
  Hash,
  MoreHorizontal,
  Pencil,
  Plus,
  Trash,
} from "@/components/icons"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

/**
 * Everything that can be done to a table, behind one menu.
 *
 * Declared once and drawn by the Browse and Structure workbenches alike, so
 * the two never disagree about what a table can have done to it. Each verb
 * carries its word and, where the word alone is a guess, a line under it —
 * "Empty table" and "Drop table" are one letter apart in a hurry.
 */
export function TableMenu({
  canWrite,
  canDDL,
  counting,
  onCount,
  onExport,
  onImport,
  onAddColumn,
  onCreateIndex,
  onRename,
  onTruncate,
  onDrop,
}: {
  canWrite: boolean
  canDDL: boolean
  counting?: boolean
  onCount?: () => void
  onExport?: (f: "csv" | "json") => void
  onImport?: () => void
  onAddColumn: () => void
  onCreateIndex: () => void
  onRename: () => void
  onTruncate: () => void
  onDrop: () => void
}) {
  const reads = Boolean(onCount || onExport || (canWrite && onImport))
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button size="icon-sm" variant="ghost" pending={counting} aria-label="More table actions">
          <MoreHorizontal />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-60">
        {onCount && (
          <DropdownMenuItem onClick={onCount}>
            <Hash />
            <Words title="Count rows" hint="An exact COUNT(*), on request." />
          </DropdownMenuItem>
        )}
        {onExport && (
          <>
            <DropdownMenuItem onClick={() => onExport("csv")}>
              <Download />
              Export as CSV
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => onExport("json")}>
              <AcronymJson />
              Export as JSON
            </DropdownMenuItem>
          </>
        )}
        {canWrite && onImport && (
          <DropdownMenuItem onClick={onImport}>
            <CloudUpload />
            <Words title="Import data…" hint="CSV or JSON, in one transaction." />
          </DropdownMenuItem>
        )}
        {canDDL && (
          <>
            {reads && <DropdownMenuSeparator />}
            <DropdownMenuLabel className="text-hint font-medium text-muted-foreground">
              Schema
            </DropdownMenuLabel>
            <DropdownMenuItem onClick={onAddColumn}>
              <Plus />
              Add column…
            </DropdownMenuItem>
            <DropdownMenuItem onClick={onCreateIndex}>
              <Plus />
              Create index…
            </DropdownMenuItem>
            <DropdownMenuItem onClick={onRename}>
              <Pencil />
              Rename table…
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onClick={onTruncate}>
              <Trash />
              <Words title="Empty table…" hint="Deletes every row; the table stays." />
            </DropdownMenuItem>
            <DropdownMenuItem variant="destructive" onClick={onDrop}>
              <Trash />
              <Words title="Drop table…" hint="Deletes the table and everything in it." />
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function Words({ title, hint }: { title: string; hint: string }) {
  return (
    <span className="flex min-w-0 flex-col">
      <span>{title}</span>
      <span className="text-hint text-muted-foreground">{hint}</span>
    </span>
  )
}
