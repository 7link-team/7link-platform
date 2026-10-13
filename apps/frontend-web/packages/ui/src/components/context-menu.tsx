import * as React from "react"
import { ContextMenu as BaseContextMenu } from "@base-ui/react/context-menu"
import { ChevronRight } from "lucide-react"

import { cn } from "@workspace/ui/lib/utils"

const ContextMenu = BaseContextMenu.Root
const ContextMenuTrigger = BaseContextMenu.Trigger
const ContextMenuSub = BaseContextMenu.SubmenuRoot

const ContextMenuSubTrigger = React.forwardRef<
  React.ElementRef<typeof BaseContextMenu.SubmenuTrigger>,
  React.ComponentPropsWithoutRef<typeof BaseContextMenu.SubmenuTrigger>
>(({ className, children, ...props }, ref) => (
  <BaseContextMenu.SubmenuTrigger
    ref={ref}
    className={cn(
      "relative flex cursor-pointer items-center rounded-sm px-2 py-1.5 text-sm transition-colors outline-none select-none focus:bg-accent focus:text-accent-foreground data-[disabled]:pointer-events-none data-[disabled]:opacity-50",
      className
    )}
    {...props}
  >
    {children}
    <ChevronRight className="ms-auto size-4" />
  </BaseContextMenu.SubmenuTrigger>
))
ContextMenuSubTrigger.displayName = "ContextMenuSubTrigger"

const ContextMenuContent = React.forwardRef<
  React.ElementRef<typeof BaseContextMenu.Positioner>,
  React.ComponentPropsWithoutRef<typeof BaseContextMenu.Positioner> & {
    className?: string
  }
>(({ className, children, ...props }, ref) => (
  <BaseContextMenu.Portal>
    <BaseContextMenu.Positioner ref={ref} {...props} className="z-50">
      <BaseContextMenu.Popup
        className={cn(
          "min-w-[8rem] overflow-hidden rounded-md border bg-popover p-1 text-popover-foreground shadow-md transition-opacity duration-200 data-[ending-style]:opacity-0 data-[starting-style]:opacity-0",
          className
        )}
      >
        {children}
      </BaseContextMenu.Popup>
    </BaseContextMenu.Positioner>
  </BaseContextMenu.Portal>
))
ContextMenuContent.displayName = "ContextMenuContent"

const ContextMenuItem = React.forwardRef<
  React.ElementRef<typeof BaseContextMenu.Item>,
  React.ComponentPropsWithoutRef<typeof BaseContextMenu.Item>
>(({ className, ...props }, ref) => (
  <BaseContextMenu.Item
    ref={ref}
    className={cn(
      "relative flex cursor-pointer items-center rounded-sm px-2 py-1.5 text-sm transition-colors outline-none select-none focus:bg-accent focus:text-accent-foreground data-[disabled]:pointer-events-none data-[disabled]:opacity-50",
      className
    )}
    {...props}
  />
))
ContextMenuItem.displayName = "ContextMenuItem"

const ContextMenuSeparator = React.forwardRef<
  React.ElementRef<typeof BaseContextMenu.Separator>,
  React.ComponentPropsWithoutRef<typeof BaseContextMenu.Separator>
>(({ className, ...props }, ref) => (
  <BaseContextMenu.Separator
    ref={ref}
    className={cn("-mx-1 my-1 h-px bg-muted", className)}
    {...props}
  />
))
ContextMenuSeparator.displayName = "ContextMenuSeparator"

export {
  ContextMenu,
  ContextMenuTrigger,
  ContextMenuSub,
  ContextMenuSubTrigger,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
}
