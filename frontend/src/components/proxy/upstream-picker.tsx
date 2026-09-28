"use client"

import { useState } from "react"
import { Box, Check, Connection, Servers } from "@/components/icons"
import { imageProduct, processProduct, ProductLogo } from "@/components/product-logo"
import { Button } from "@/components/ui/button"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group"
import { Popover, PopoverAnchor, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import type { UpstreamOption } from "@/components/proxy/upstream-options"
import { cn } from "@/lib/utils"

/**
 * An upstream typed, or picked from what is running on this machine.
 *
 * The field stays free text — a hostname, a socket, another machine are all
 * upstreams nothing here can list — and the button beside it opens what the
 * host can see: containers by the port they publish, then every other
 * socket on loopback or every interface. A container port nothing publishes
 * is shown and cannot be picked, with what to do about it.
 */
export function UpstreamPicker({
  id,
  value,
  onChange,
  options,
  loading,
  failed,
  onRetry,
  placeholder,
  label,
  pickLabel = "Pick from running services",
}: {
  id?: string
  value: string
  onChange: (value: string) => void
  options: UpstreamOption[]
  /** The listeners are still being read. */
  loading: boolean
  /** The listeners could not be read. */
  failed: boolean
  onRetry: () => void
  placeholder?: string
  /** The field's name where no Field label names it. */
  label?: string
  /** The button's name, distinct where a form has several of these. */
  pickLabel?: string
}) {
  const [open, setOpen] = useState(false)
  const containers = options.filter((o) => o.source === "container")
  const listeners = options.filter((o) => o.source === "listener")
  const pick = (url: string) => {
    onChange(url)
    setOpen(false)
  }

  return (
    // Modal so the list scrolls: the sheet it opens from holds the page's
    // scroll lock, and a non-modal popover's list sits outside it.
    <Popover open={open} onOpenChange={setOpen} modal>
      <PopoverAnchor asChild>
        <InputGroup>
          <InputGroupInput
            id={id}
            value={value}
            onChange={(e) => onChange(e.target.value)}
            placeholder={placeholder}
            aria-label={label}
            className="font-mono text-xs"
          />
          <InputGroupAddon align="inline-end" className="gap-0 p-0">
            <PopoverTrigger asChild>
              <InputGroupButton aria-label={pickLabel}>
                <Servers aria-hidden className="size-3.5" />
                Running
              </InputGroupButton>
            </PopoverTrigger>
          </InputGroupAddon>
        </InputGroup>
      </PopoverAnchor>
      <PopoverContent align="end" className="w-(--radix-popover-trigger-width) min-w-72 p-0">
        {/* cmdk names its own input and list from these; an aria-label on
            either is overwritten. */}
        <Command label="Filter running services" filter={contains}>
          <CommandInput placeholder="Port, process or container" />
          <CommandList label="Running services">
            {failed ? (
              <div className="flex items-center justify-between gap-3 px-3 py-2.5 text-hint text-muted-foreground">
                Could not read what is listening on this machine.
                <Button size="xs" variant="outline" onClick={onRetry}>
                  Retry
                </Button>
              </div>
            ) : loading && options.length === 0 ? (
              <div className="px-3 py-2.5 text-hint text-muted-foreground">
                Looking for running services…
              </div>
            ) : (
              <CommandEmpty className="px-3 py-6 text-center text-hint text-muted-foreground">
                {options.length === 0
                  ? "Nothing on this machine is listening where nginx could reach it."
                  : "Nothing matches."}
              </CommandEmpty>
            )}
            {containers.length > 0 && (
              <CommandGroup heading="Containers">
                {containers.map((option) => (
                  <Choice key={option.key} option={option} value={value} onPick={pick} />
                ))}
              </CommandGroup>
            )}
            {listeners.length > 0 && (
              <CommandGroup heading="On this machine">
                {listeners.map((option) => (
                  <Choice key={option.key} option={option} value={value} onPick={pick} />
                ))}
              </CommandGroup>
            )}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}

/**
 * A plain substring match. cmdk's own scoring is fuzzy, which for addresses
 * means 8080 also finds 127.0.0.1:8081 → 3000; a port is typed to find that
 * port.
 */
function contains(value: string, search: string) {
  return value.toLowerCase().includes(search.trim().toLowerCase()) ? 1 : 0
}

function Choice({
  option,
  value,
  onPick,
}: {
  option: UpstreamOption
  value: string
  onPick: (url: string) => void
}) {
  const product =
    option.source === "container"
      ? imageProduct(option.image ?? "")
      : option.process
        ? processProduct(option.process)
        : undefined
  const reached = option.url !== ""
  return (
    <CommandItem
      // What the filter reads, and unique: a container's name and port, a
      // socket's address and process.
      value={`${option.name} ${option.address}${option.containerPort ? ` ${option.containerPort}` : ""}`}
      disabled={!reached}
      onSelect={() => onPick(option.url)}
      className="items-start gap-2.5"
    >
      <ProductLogo
        id={product}
        size="sm"
        fallback={option.source === "container" ? Box : Connection}
      />
      <span className="flex min-w-0 flex-1 flex-col">
        {/* A socket whose process the host would not name is its address
            alone, rather than the address twice. */}
        <span className={cn("truncate", option.name ? "text-body" : "font-mono text-xs")}>
          {option.name || option.address}
        </span>
        {option.name && (
          <span className="truncate font-mono text-micro text-muted-foreground">
            {reached
              ? `${option.address}${option.containerPort ? ` → ${option.containerPort}` : ""}`
              : `port ${option.containerPort}`}
          </span>
        )}
        {option.unreachable && (
          <span className="text-micro leading-relaxed text-warning">{option.unreachable}</span>
        )}
      </span>
      <Check
        aria-hidden
        className={cn(
          "mt-0.5 size-3.5 shrink-0 text-brand",
          reached && value === option.url ? "opacity-100" : "opacity-0",
        )}
      />
    </CommandItem>
  )
}
