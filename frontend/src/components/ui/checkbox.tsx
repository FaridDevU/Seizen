import type * as React from "react"
import { Check } from "lucide-react"

import { cn } from "@/lib/utils"

function Checkbox({ className, ...props }: React.ComponentProps<"input">) {
  return (
    <span className={cn("relative inline-flex size-4 shrink-0", className)}>
      <input
        type="checkbox"
        data-slot="checkbox"
        className="peer size-4 appearance-none rounded-[0.3rem] border border-[var(--outline-variant)] bg-[var(--surface-container)] outline-none transition-colors checked:border-[var(--primary)] checked:bg-[var(--primary)] focus-visible:ring-2 focus-visible:ring-[var(--focus-ring)] disabled:cursor-wait disabled:opacity-50"
        {...props}
      />
      <Check
        aria-hidden="true"
        className="pointer-events-none absolute inset-0 m-auto size-3 text-[var(--primary-foreground)] opacity-0 transition-opacity peer-checked:opacity-100"
        strokeWidth={3}
      />
    </span>
  )
}

export { Checkbox }
