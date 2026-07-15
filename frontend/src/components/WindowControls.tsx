import { Minus, Square, X } from "lucide-react"
import {
  Quit,
  WindowMinimise,
  WindowToggleMaximise,
} from "../../wailsjs/runtime/runtime"

import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

function WindowControls({ className }: { className?: string }) {
  return (
    <div
      role="group"
      aria-label="Window controls"
      className={cn(
        "window-no-drag flex h-8 items-center overflow-hidden rounded-full border border-[var(--outline-variant)] bg-[var(--surface-container-high)] text-[var(--on-surface-variant)] shadow-[0_3px_12px_var(--shadow-elevated)] backdrop-blur-xl",
        className,
      )}
    >
      <Button
        type="button"
        variant="ghost"
        size="icon"
        aria-label="Minimize"
        title="Minimize"
        onClick={WindowMinimise}
        className="h-8 w-9 rounded-none hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)]"
      >
        <Minus className="size-3.5" strokeWidth={1.65} />
      </Button>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        aria-label="Maximize or restore"
        title="Maximize or restore"
        onClick={WindowToggleMaximise}
        className="h-8 w-9 rounded-none hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)]"
      >
        <Square className="size-3" strokeWidth={1.6} />
      </Button>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        aria-label="Close"
        title="Close"
        onClick={Quit}
        className="h-8 w-9 rounded-none hover:bg-[var(--error)] hover:text-white"
      >
        <X className="size-3.5" strokeWidth={1.65} />
      </Button>
    </div>
  )
}

export { WindowControls }
