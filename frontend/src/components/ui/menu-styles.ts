// Selects, action menus and context menus share a drawing while Radix owns
// each control's keyboard and focus semantics.
export const menuSurfaceClasses =
  "z-50 min-w-32 overflow-x-hidden overflow-y-auto rounded-md border bg-popover p-1 text-popover-foreground shadow-md outline-hidden data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:zoom-in-95 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95 data-[side=bottom]:slide-in-from-top-1 data-[side=top]:slide-in-from-bottom-1 data-[side=left]:slide-in-from-right-1 data-[side=right]:slide-in-from-left-1 motion-reduce:animate-none!"

export const menuItemClasses =
  "relative flex min-h-11 items-center gap-2 rounded-sm px-2 py-1.5 text-body outline-hidden select-none transition-colors focus:bg-menu-hover data-[highlighted]:bg-menu-hover data-[disabled]:pointer-events-none data-[disabled]:opacity-50 sm:min-h-8 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4 [&_svg:not([class*='text-'])]:text-muted-foreground"

export const menuDangerClasses =
  "data-[variant=destructive]:text-destructive data-[variant=destructive]:focus:bg-wash-danger data-[variant=destructive]:focus:text-destructive data-[variant=destructive]:data-[highlighted]:bg-wash-danger data-[variant=destructive]:*:[svg]:text-destructive!"

export const menuLabelClasses =
  "eyebrow px-2 pt-2 pb-1 data-[inset]:pl-8 [&.font-mono]:tracking-normal [&.font-mono]:normal-case"

export const menuIndicatorClasses =
  "pointer-events-none absolute right-2 flex size-4 items-center justify-center"
