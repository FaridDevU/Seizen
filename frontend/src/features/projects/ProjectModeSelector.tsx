import { useLayoutEffect, useRef, useState } from "react"

import { cn } from "@/lib/utils"

export type ProjectMode = "workspace" | "app" | "server-lab"

const modes: Array<{ id: ProjectMode; label: string }> = [
  { id: "workspace", label: "Workspace" },
  { id: "app", label: "App" },
  { id: "server-lab", label: "Server Lab" },
]

export function ProjectModeSelector({
  value,
  onChange,
}: {
  value: ProjectMode
  onChange: (mode: ProjectMode) => void
}) {
  const buttonsRef = useRef<Partial<Record<ProjectMode, HTMLButtonElement | null>>>({})
  const [indicator, setIndicator] = useState<{ left: number; width: number } | null>(null)

  useLayoutEffect(() => {
    const active = buttonsRef.current[value]
    if (!active) return
    setIndicator({ left: active.offsetLeft, width: active.offsetWidth })
  }, [value])

  return (
    <div
      role="tablist"
      aria-label="Project view"
      className="window-no-drag pointer-events-auto relative flex h-9 items-center rounded-full border border-[var(--outline-variant)] bg-[var(--surface-container-high)] p-1 shadow-[0_3px_12px_var(--shadow-elevated)] backdrop-blur-xl"
    >
      {indicator && (
        <span
          aria-hidden="true"
          className="absolute top-1 h-7 rounded-full bg-[var(--primary-container)] shadow-[inset_0_0_0_1px_var(--outline-variant)] transition-[left,width] duration-300 ease-[cubic-bezier(.22,1,.36,1)]"
          style={{ left: indicator.left, width: indicator.width }}
        />
      )}
      {modes.map((mode) => (
        <button
          key={mode.id}
          ref={(element) => {
            buttonsRef.current[mode.id] = element
          }}
          type="button"
          role="tab"
          aria-selected={value === mode.id}
          onClick={() => onChange(mode.id)}
          className={cn(
            "relative z-10 h-7 rounded-full px-3 text-[0.7rem] font-medium text-[var(--on-surface-variant)] outline-none transition-colors hover:text-[var(--on-surface)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]",
            value === mode.id && "text-[var(--on-primary-container)]",
          )}
        >
          {mode.label}
        </button>
      ))}
    </div>
  )
}
