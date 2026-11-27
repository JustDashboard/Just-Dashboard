"use client"

import { useState } from "react"
import type { SiteSpec } from "@/lib/types"
import { Group } from "@/components/panel"
import { Input } from "@/components/ui/input"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { siteRoutes } from "@/components/proxy/site-routes"
import { Field } from "@/components/proxy/site-form/fields"

export const REDIRECT_CODES = [
  { code: 301, hint: "Permanent. Browsers keep it more or less forever." },
  { code: 302, hint: "Temporary. Use it while you are still deciding." },
  { code: 307, hint: "Temporary, and a form post is posted again to the new address." },
  { code: 308, hint: "Permanent, and a form post is posted again to the new address." },
] as const

/** The status a redirect answers with; a site saved before 307 and 308 has only `permanent`. */
export function redirectCode(spec: SiteSpec) {
  return spec.redirectCode ?? (spec.permanent ? 301 : 302)
}

/**
 * The index order, typed as names separated by spaces. Held as typed so a
 * space can be typed at all; the spec gets the names.
 */
export function IndexFilesField({
  spec,
  onChange,
}: {
  spec: SiteSpec
  onChange: (files: string[] | undefined) => void
}) {
  const [text, setText] = useState(spec.indexFiles?.join(" ") ?? "")
  return (
    <Field
      label="Index files"
      htmlFor="site-index"
      tip="Looked for in this order in each folder. Empty is the usual order."
    >
      <Input
        id="site-index"
        value={text}
        onChange={(e) => {
          setText(e.target.value)
          const files = e.target.value.split(/\s+/).filter(Boolean)
          onChange(files.length ? files : undefined)
        }}
        placeholder={spec.kind === "php" ? "index.php index.html" : "index.html index.htm"}
        className="font-mono text-xs"
      />
    </Field>
  )
}

/** Each path as nginx will decide it, most decisive first. */
export function RoutesTable({ spec }: { spec: SiteSpec }) {
  const routes = siteRoutes(spec)
  return (
    <Group className="p-0">
      <Table aria-label="What each path does">
        <TableHeader>
          <TableRow>
            <TableHead>Path</TableHead>
            <TableHead>Goes to</TableHead>
            <TableHead>For example</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {routes.map((route) => (
            <TableRow key={route.location}>
              <TableCell className="align-top">
                <div className="font-mono">{route.location}</div>
                <div className="text-hint text-muted-foreground">{route.rule}</div>
              </TableCell>
              <TableCell className="align-top">
                <div className="font-mono break-all">{route.target || "—"}</div>
                {route.notes.length > 0 && (
                  <div className="text-hint text-muted-foreground">{route.notes.join(" · ")}</div>
                )}
              </TableCell>
              <TableCell className="align-top font-mono text-hint text-muted-foreground">
                {route.example
                  ? `${route.example.request} → ${route.example.reaches}`
                  : "the request's own path"}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Group>
  )
}
