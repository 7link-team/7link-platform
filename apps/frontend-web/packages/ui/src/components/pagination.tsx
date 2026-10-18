import * as React from "react"
import { ChevronLeft, ChevronRight, MoreHorizontal } from "lucide-react"
import { useTranslations } from "use-intl"

import { cn } from "@workspace/ui/lib/utils"
import { Button, buttonVariants } from "@workspace/ui/components/button"

function Pagination({ className, ...props }: React.ComponentProps<"nav">) {
  return (
    <nav
      role="navigation"
      aria-label="pagination"
      data-slot="pagination"
      className={cn("mx-auto flex w-full justify-center", className)}
      {...props}
    />
  )
}
Pagination.displayName = "Pagination"

function PaginationContent({
  className,
  ...props
}: React.ComponentProps<"ul">) {
  return (
    <ul
      data-slot="pagination-content"
      className={cn("flex flex-row items-center gap-1", className)}
      {...props}
    />
  )
}
PaginationContent.displayName = "PaginationContent"

function PaginationItem({ className, ...props }: React.ComponentProps<"li">) {
  return (
    <li data-slot="pagination-item" className={cn("", className)} {...props} />
  )
}
PaginationItem.displayName = "PaginationItem"

type PaginationLinkProps = {
  isActive?: boolean
} & React.ComponentProps<"a">

function PaginationLink({
  className,
  isActive,
  ...props
}: PaginationLinkProps) {
  return (
    <a
      aria-current={isActive ? "page" : undefined}
      data-slot="pagination-link"
      className={cn(
        buttonVariants({
          variant: isActive ? "default" : "ghost",
          size: "icon",
        }),
        className
      )}
      {...props}
    />
  )
}
PaginationLink.displayName = "PaginationLink"

function PaginationPrevious({
  className,
  ...props
}: React.ComponentProps<typeof PaginationLink>) {
  return (
    <PaginationLink
      aria-label="Go to previous page"
      data-slot="pagination-previous"
      className={cn("gap-1 pl-2.5", className)}
      {...props}
    >
      <ChevronLeft className="h-4 w-4" />
      <span className="sr-only">Previous</span>
    </PaginationLink>
  )
}
PaginationPrevious.displayName = "PaginationPrevious"

function PaginationNext({
  className,
  ...props
}: React.ComponentProps<typeof PaginationLink>) {
  return (
    <PaginationLink
      aria-label="Go to next page"
      data-slot="pagination-next"
      className={cn("gap-1 pr-2.5", className)}
      {...props}
    >
      <span className="sr-only">Next</span>
      <ChevronRight className="h-4 w-4" />
    </PaginationLink>
  )
}
PaginationNext.displayName = "PaginationNext"

function PaginationEllipsis({
  className,
  ...props
}: React.ComponentProps<"span">) {
  return (
    <span
      aria-hidden
      data-slot="pagination-ellipsis"
      className={cn("flex h-9 w-9 items-center justify-center", className)}
      {...props}
    >
      <MoreHorizontal className="h-4 w-4" />
      <span className="sr-only">More pages</span>
    </span>
  )
}
PaginationEllipsis.displayName = "PaginationEllipsis"

/** Controlled page buttons shared by local and server-paged tables. */
function PaginationPages({
  page,
  totalPages,
  onPageChange,
  disabled = false,
}: {
  page: number
  totalPages: number
  onPageChange: (page: number) => void
  disabled?: boolean
}) {
  const t = useTranslations("table")
  const last = Math.max(1, totalPages)
  const current = Math.max(1, Math.min(page, last))
  const visible = new Set<number>()
  for (const value of [
    1,
    2,
    3,
    current - 1,
    current,
    current + 1,
    last - 2,
    last - 1,
    last,
  ]) {
    if (value >= 1 && value <= last) visible.add(value)
  }
  const pages: Array<number | string> = []
  for (const value of [...visible].sort((a, b) => a - b)) {
    const previous = pages.at(-1)
    if (typeof previous === "number" && value - previous > 1) {
      pages.push(value - previous === 2 ? previous + 1 : `gap-${value}`)
    }
    pages.push(value)
  }
  return (
    <Pagination aria-label={t("pagination")} className="mx-0 w-auto">
      <PaginationContent className="flex-wrap justify-center gap-0.5">
        <PaginationItem>
          <Button
            type="button"
            variant="outline"
            size="icon-xs"
            aria-label={t("previousPage")}
            disabled={disabled || current <= 1}
            onClick={() => onPageChange(current - 1)}
          >
            <ChevronLeft className="size-4" />
          </Button>
        </PaginationItem>
        {pages.map((value) => (
          <PaginationItem key={value}>
            {typeof value === "number" ? (
              <Button
                type="button"
                variant={value === current ? "default" : "ghost"}
                size="icon-xs"
                className="w-auto min-w-6 px-1 tabular-nums"
                aria-label={t("pageNumber", { page: value })}
                aria-current={value === current ? "page" : undefined}
                disabled={disabled}
                onClick={() => {
                  if (value !== current) onPageChange(value)
                }}
              >
                {value}
              </Button>
            ) : (
              <PaginationEllipsis className="size-6" />
            )}
          </PaginationItem>
        ))}
        <PaginationItem>
          <Button
            type="button"
            variant="outline"
            size="icon-xs"
            aria-label={t("nextPage")}
            disabled={disabled || current >= last}
            onClick={() => onPageChange(current + 1)}
          >
            <ChevronRight className="size-4" />
          </Button>
        </PaginationItem>
      </PaginationContent>
    </Pagination>
  )
}

export {
  Pagination,
  PaginationContent,
  PaginationItem,
  PaginationLink,
  PaginationPrevious,
  PaginationNext,
  PaginationEllipsis,
  PaginationPages,
}
