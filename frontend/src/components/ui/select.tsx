import type * as React from "react"
import { ChevronDown } from "lucide-react"

import { cn } from "@/lib/utils"

function Select({
  className,
  wrapperClassName,
  children,
  ...props
}: React.ComponentProps<"select"> & { wrapperClassName?: string }) {
  return (
    <span className={cn("relative inline-flex w-full", wrapperClassName)}>
      <select
        data-slot="select"
        className={cn(
          "h-9 w-full appearance-none rounded-xl border border-[var(--outline-variant)] bg-[var(--surface-container)] pl-3 pr-8 text-sm text-[var(--on-surface)] outline-none transition-[border-color,box-shadow] focus-visible:border-[var(--focus-border)] focus-visible:ring-2 focus-visible:ring-[var(--focus-ring)] disabled:pointer-events-none disabled:opacity-50",
          className,
        )}
        {...props}
      >
        {children}
      </select>
      <ChevronDown
        aria-hidden="true"
        className="pointer-events-none absolute right-2.5 top-1/2 size-3.5 -translate-y-1/2 text-[var(--on-surface-variant)]"
        strokeWidth={1.75}
      />
    </span>
  )
}

export { Select }
