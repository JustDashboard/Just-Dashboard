"use client"

import { Copy, Download } from "@/components/icons"
import { useCopy } from "@/hooks/use-copy"
import { Well } from "@/components/panel"
import { Button } from "@/components/ui/button"

/**
 * A client's WireGuard configuration as the two things a person does with
 * it: point a phone's camera at the code, or copy or save the file for a
 * laptop. The code is the server's own rendering (whole pixels per module,
 * with the quiet zone a scanner needs), drawn on white because a scanner
 * reads dark modules on a light ground and nothing else.
 *
 * The private key is in both. The sheet says so once, plainly, rather than
 * hiding the file behind a second press.
 */
export function ClientConfig({ name, config, qr }: { name: string; config: string; qr: string }) {
  const { copy, copied } = useCopy()
  const file = `${name.replace(/[^a-zA-Z0-9_.-]+/g, "-").replace(/^-+|-+$/g, "") || "wireguard"}.conf`
  const download = () => {
    const url = URL.createObjectURL(new Blob([config], { type: "text/plain" }))
    const a = document.createElement("a")
    a.href = url
    a.download = file
    a.click()
    URL.revokeObjectURL(url)
  }
  return (
    <div className="flex min-w-0 flex-col gap-4 md:flex-row md:items-start">
      <div className="shrink-0 self-center rounded-xl bg-white p-2 md:self-start">
        {/* eslint-disable-next-line @next/next/no-img-element -- a data URL the server rendered */}
        <img src={qr} alt={`QR code for ${name}'s WireGuard configuration`} className="size-56" />
      </div>
      <div className="flex min-w-0 flex-1 flex-col gap-2">
        <p className="text-hint text-muted-foreground">
          Scan it in the WireGuard app, or save the file. It holds this device&rsquo;s private key:
          share it only with the device itself.
        </p>
        <Well className="max-h-64 overflow-auto font-mono text-micro leading-relaxed whitespace-pre">
          {config}
        </Well>
        <div className="flex flex-wrap gap-2">
          <Button size="sm" variant="outline" onClick={() => void copy(config)}>
            <Copy aria-hidden />
            {copied ? "Copied" : "Copy"}
          </Button>
          <Button size="sm" variant="outline" onClick={download}>
            <Download aria-hidden />
            Save {file}
          </Button>
        </div>
      </div>
    </div>
  )
}
