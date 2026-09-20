"use client"

import { useState } from "react"
import { Copy, Key, Layout, Pencil, Plus, Trash } from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, get, post } from "@/lib/api"
import { plural } from "@/lib/format"
import type { DbConnection, DbDriverInfo, DbTable, DbTableDetail } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import type { useConfirm } from "@/components/confirm-dialog"
import {
  AddColumnDialog,
  CreateIndexDialog,
  CreateTableDialog,
  RenameDialog,
} from "@/components/database/ddl-dialogs"
import { TableRail, type TableSelection } from "@/components/database/table-rail"
import { TableMenu } from "@/components/database/table-actions"
import { IconAction, RowActions } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { Pane, PaneHeader, Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { EmptyNote, EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Tag } from "@/components/tag"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { copyText } from "@/lib/clipboard"

/**
 * The Structure tab: a table's columns, primary key, indexes, foreign keys and
 * the DDL that would recreate it — the reference view every database tool has.
 *
 * The same rail as Browse on the left, so a table is picked here the way it is
 * picked there; the page used to open on "select a table" with nothing on it
 * to select one with. The schema-changing verbs live here too — this is the
 * page that shows what a column is, so it is the page that adds one.
 */
export function StructureTab({
  conn,
  schema,
  table,
  info,
  confirm,
  onSelect,
}: {
  conn: DbConnection
  schema: string
  table?: string
  info?: DbDriverInfo
  confirm: ReturnType<typeof useConfirm>["confirm"]
  onSelect: (sel: TableSelection | null) => void
}) {
  const { can } = useAuth()
  const [renaming, setRenaming] = useState<string | null>(null)
  const [dialog, setDialog] = useState<
    null | "createTable" | "addColumn" | "createIndex" | "renameTable"
  >(null)
  const tables = usePoll(
    (signal) => get<DbTable[]>(`/databases/${conn.id}/tables`, { schema: "" }, signal),
    0,
    [conn.id],
  )
  const detail = usePoll(
    (signal) =>
      table
        ? get<DbTableDetail>(`/databases/${conn.id}/table`, { schema, table }, signal)
        : Promise.resolve(null as unknown as DbTableDetail),
    0,
    [conn.id, schema, table],
  )

  const d = detail.data
  const pk = new Set(d?.primaryKey ?? [])
  const canEdit = can("service.control") && (info?.ddl ?? false)
  const selection = table ? { schema, table } : null
  const current = tables.data?.find((t) => t.name === table && t.schema === schema)

  const dropColumn = (column: string) =>
    confirm({
      title: "Drop column",
      phrase: column,
      confirmLabel: "Drop column",
      description: (
        <p>
          Permanently removes <span className="font-mono text-xs">{column}</span> from{" "}
          <b>{table}</b>, and every value stored in it. This cannot be undone.
        </p>
      ),
      action: async (c) => {
        await del(`/databases/${conn.id}/ddl/column`, {
          body: { schema, table, name: column },
          confirm: c,
        })
        notify.success(`Dropped ${column}`)
        detail.refresh()
      },
    })

  const dropIndex = (name: string) =>
    confirm({
      title: "Drop index",
      confirmLabel: "Drop index",
      description: (
        <p>
          Removes the index <span className="font-mono text-xs">{name}</span>. Queries relying on it
          will fall back to a scan.
        </p>
      ),
      action: async (c) => {
        await del(`/databases/${conn.id}/ddl/index`, {
          body: { schema, table, name },
          confirm: c,
        })
        notify.success(`Dropped ${name}`)
        detail.refresh()
      },
    })

  const dropTable = () =>
    confirm({
      title: "Drop table",
      phrase: table,
      confirmLabel: "Drop table",
      description: (
        <p>
          Permanently destroys <b>{table}</b> and every row in it. This cannot be undone.
        </p>
      ),
      action: async (c) => {
        await del(`/databases/${conn.id}/ddl/table`, { body: { schema, table }, confirm: c })
        notify.success(`Dropped ${table}`)
        onSelect(null)
        tables.refresh()
      },
    })

  const truncateTable = () =>
    confirm({
      title: "Empty table",
      phrase: table,
      confirmLabel: "Empty it",
      description: (
        <p>
          Removes every row from <b>{table}</b>, keeping the table itself. This cannot be undone.
        </p>
      ),
      action: async (c) => {
        await post(`/databases/${conn.id}/ddl/truncate`, { schema, table }, { confirm: c })
        notify.success(`Emptied ${table}`)
        tables.refresh()
      },
    })

  return (
    <Pane className="min-h-0 flex-1">
      <div className="grid min-h-0 flex-1 grid-rows-[minmax(0,14rem)_minmax(0,1fr)] lg:grid-cols-[17rem_minmax(0,1fr)] lg:grid-rows-1">
        <TableRail
          connId={conn.id}
          tables={tables.data}
          loading={tables.loading}
          selected={selection}
          onSelect={(t) => onSelect({ schema: t.schema, table: t.name })}
          className="border-b border-hairline lg:border-r lg:border-b-0"
          action={
            canEdit && (
              <Button
                size="icon-sm"
                variant="outline"
                className="size-7"
                aria-label="Create table"
                title="Create table"
                onClick={() => setDialog("createTable")}
              >
                <Plus className="size-3.5" />
              </Button>
            )
          }
        />

        <div className="flex min-h-0 min-w-0 flex-col">
          <PaneHeader className="gap-2">
            <span className="min-w-0 flex-1 truncate text-body font-medium">
              {table ?? <span className="text-muted-foreground">Pick a table</span>}
            </span>
            {current?.type && current.type.toLowerCase() !== "table" && <Tag>{current.type}</Tag>}
            {table && d && (
              <span className="numeric hidden text-hint text-muted-foreground sm:inline">
                {plural(d.columns.length, "column")} ·{" "}
                {plural(d.indexes.length, "index", "indexes")} ·{" "}
                {plural(d.foreignKeys.length, "foreign key")}
              </span>
            )}
            {table && canEdit && (
              <div className="flex shrink-0 items-center gap-1.5">
                <Button size="sm" variant="outline" onClick={() => setDialog("addColumn")}>
                  <Plus className="size-3.5" />
                  Add column
                </Button>
                <Button size="sm" variant="outline" onClick={() => setDialog("createIndex")}>
                  <Plus className="size-3.5" />
                  Create index
                </Button>
                <TableMenu
                  canWrite={canEdit}
                  canDDL={canEdit}
                  onAddColumn={() => setDialog("addColumn")}
                  onCreateIndex={() => setDialog("createIndex")}
                  onRename={() => setDialog("renameTable")}
                  onTruncate={truncateTable}
                  onDrop={dropTable}
                />
              </div>
            )}
          </PaneHeader>

          <div className="min-h-0 min-w-0 flex-1 overflow-y-auto">
            {!table && <EmptyState icon={Layout} title="Pick a table to inspect its structure" />}
            {table && detail.error && <ErrorState error={detail.error} className="m-4" />}
            {table && !detail.error && !d && <LoadingRows rows={8} className="p-4" />}
            {table && d && (
              <div className="animate-rise space-y-8 p-5">
                <Panel plain>
                  <PanelHeader title="Columns" />
                  <PanelBody flush className="-mx-5">
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>Name</TableHead>
                          <TableHead>Type</TableHead>
                          <TableHead>Nullable</TableHead>
                          <TableHead>Default</TableHead>
                          <TableHead>Key</TableHead>
                          {canEdit && <TableHead className="w-20" />}
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {d.columns.map((c) => (
                          <TableRow key={c.name} className="group">
                            <TableCell className="font-mono font-medium">{c.name}</TableCell>
                            <TableCell className="font-mono text-muted-foreground">
                              {c.type.toLowerCase()}
                            </TableCell>
                            <TableCell>
                              {c.nullable ? (
                                <span className="text-muted-foreground">yes</span>
                              ) : (
                                <span className="text-foreground">no</span>
                              )}
                            </TableCell>
                            <TableCell className="max-w-40 truncate font-mono text-muted-foreground">
                              {c.default || "—"}
                            </TableCell>
                            <TableCell>{pk.has(c.name) && <Tag icon={Key}>pk</Tag>}</TableCell>
                            {canEdit && (
                              <TableCell className="w-20">
                                <RowActions>
                                  <IconAction
                                    label="Rename column"
                                    onClick={() => setRenaming(c.name)}
                                  >
                                    <Pencil />
                                  </IconAction>
                                  <IconAction
                                    label="Drop column"
                                    className="text-destructive"
                                    onClick={() => dropColumn(c.name)}
                                  >
                                    <Trash />
                                  </IconAction>
                                </RowActions>
                              </TableCell>
                            )}
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  </PanelBody>
                </Panel>

                <div className="grid gap-8 xl:grid-cols-2 [&>*]:min-w-0">
                  <Panel plain>
                    <PanelHeader title="Indexes" />
                    <PanelBody flush className="-mx-5">
                      {d.indexes.length === 0 ? (
                        <EmptyNote>No indexes.</EmptyNote>
                      ) : (
                        <Table>
                          <TableHeader>
                            <TableRow>
                              <TableHead>Name</TableHead>
                              <TableHead>Columns</TableHead>
                              <TableHead>Unique</TableHead>
                              {canEdit && <TableHead className="w-10" />}
                            </TableRow>
                          </TableHeader>
                          <TableBody>
                            {d.indexes.map((ix) => (
                              <TableRow key={ix.name} className="group">
                                <TableCell className="font-mono">
                                  {ix.name}
                                  {ix.primary && <Tag className="ml-1.5">primary</Tag>}
                                </TableCell>
                                <TableCell className="font-mono text-muted-foreground">
                                  {ix.columns.join(", ")}
                                </TableCell>
                                <TableCell>{ix.unique ? "yes" : "no"}</TableCell>
                                {canEdit && (
                                  <TableCell className="w-10">
                                    {!ix.primary && (
                                      <IconAction
                                        label="Drop index"
                                        reveal
                                        className="text-destructive"
                                        onClick={() => dropIndex(ix.name)}
                                      >
                                        <Trash />
                                      </IconAction>
                                    )}
                                  </TableCell>
                                )}
                              </TableRow>
                            ))}
                          </TableBody>
                        </Table>
                      )}
                    </PanelBody>
                  </Panel>

                  <Panel plain>
                    <PanelHeader title="Foreign keys" />
                    <PanelBody flush className="-mx-5">
                      {d.foreignKeys.length === 0 ? (
                        <EmptyNote>No foreign keys.</EmptyNote>
                      ) : (
                        <Table>
                          <TableHeader>
                            <TableRow>
                              <TableHead>Columns</TableHead>
                              <TableHead>References</TableHead>
                              <TableHead>On delete</TableHead>
                            </TableRow>
                          </TableHeader>
                          <TableBody>
                            {d.foreignKeys.map((fk) => (
                              <TableRow
                                key={fk.name}
                                onActivate={() =>
                                  onSelect({ schema: fk.refSchema || schema, table: fk.refTable })
                                }
                              >
                                <TableCell className="font-mono">{fk.columns.join(", ")}</TableCell>
                                <TableCell className="font-mono text-muted-foreground">
                                  {fk.refTable}({fk.refColumns.join(", ")})
                                </TableCell>
                                <TableCell className="text-muted-foreground">
                                  {fk.onDelete || "—"}
                                </TableCell>
                              </TableRow>
                            ))}
                          </TableBody>
                        </Table>
                      )}
                    </PanelBody>
                  </Panel>
                </div>

                {d.createSql && (
                  <Panel plain>
                    <PanelHeader
                      title="Definition"
                      actions={
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => void copyText(d.createSql!, "Copied DDL")}
                        >
                          <Copy className="size-3.5" />
                          Copy
                        </Button>
                      }
                    />
                    <PanelBody>
                      <Well className="max-h-96 whitespace-pre">{d.createSql}</Well>
                    </PanelBody>
                  </Panel>
                )}
              </div>
            )}
          </div>
        </div>
      </div>

      {renaming && table && (
        <RenameDialog
          open
          onOpenChange={(o) => !o && setRenaming(null)}
          connId={conn.id}
          schema={schema}
          table={table}
          kind="column"
          current={renaming}
          onDone={detail.refresh}
        />
      )}
      {dialog === "createTable" && (
        <CreateTableDialog
          open
          onOpenChange={() => setDialog(null)}
          connId={conn.id}
          schema={schema}
          info={info}
          onDone={tables.refresh}
        />
      )}
      {dialog === "addColumn" && table && (
        <AddColumnDialog
          open
          onOpenChange={() => setDialog(null)}
          connId={conn.id}
          schema={schema}
          table={table}
          info={info}
          onDone={detail.refresh}
        />
      )}
      {dialog === "createIndex" && table && (
        <CreateIndexDialog
          open
          onOpenChange={() => setDialog(null)}
          connId={conn.id}
          schema={schema}
          table={table}
          detail={d}
          onDone={detail.refresh}
        />
      )}
      {dialog === "renameTable" && table && (
        <RenameDialog
          open
          onOpenChange={() => setDialog(null)}
          connId={conn.id}
          schema={schema}
          table={table}
          kind="table"
          current={table}
          onDone={(to) => {
            onSelect({ schema, table: to })
            tables.refresh()
          }}
        />
      )}
    </Pane>
  )
}
