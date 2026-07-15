import { GitCompareArrows, Plus, X } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Select } from "@/components/ui/select"
import type {
  Experiment,
  ExperimentStatus,
  ProjectContext,
} from "./project-service"

export const experimentStatusLabels: Record<ExperimentStatus, string> = {
  draft: "Preparing",
  creating: "Preparing",
  active: "Active",
  paused: "Paused",
  awaiting_approval: "Awaiting approval",
  review_ready: "Ready to review",
  integrating: "Integrating",
  integrated: "Integrated",
  conflicted: "Conflicted",
  failed: "Failed",
  discarded: "Discarded",
  archived: "Archived",
}

export function ExperimentComparison({
  text,
  onClose,
}: {
  text: string
  onClose: () => void
}) {
  return (
    <div className="view-enter mb-4 overflow-hidden rounded-xl border border-[var(--outline-variant)] bg-[var(--surface-container)]">
      <div className="flex items-center justify-between border-b border-[var(--outline-variant)] px-3 py-2">
        <p className="flex items-center gap-2 text-xs font-medium">
          <GitCompareArrows className="size-3.5 text-[var(--on-surface-variant)]" strokeWidth={1.8} />
          Comparison with Main
        </p>
        <button
          type="button"
          aria-label="Close comparison"
          onClick={onClose}
          className="flex size-6 items-center justify-center rounded-full text-[var(--on-surface-variant)] outline-none transition-colors hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
        >
          <X className="size-3.5" />
        </button>
      </div>
      <pre className="max-h-48 overflow-auto whitespace-pre-wrap px-3 py-2 font-mono text-[0.68rem] leading-5">{text}</pre>
    </div>
  )
}

export function ExperimentSelector({
  principalLabel,
  context,
  experiments,
  onSelect,
  onNew,
  onRestore,
}: {
  principalLabel: string
  context: ProjectContext
  experiments: Experiment[]
  onSelect: (experimentId: string) => void
  onNew?: () => void
  onRestore?: (experimentId: string) => void
}) {
  return (
    <div className="mb-4 flex min-h-9 items-center gap-2">
      <Select
        value={context.experimentId}
        onChange={(event) => {
          const experiment = experiments.find((item) => item.id === event.target.value)
          if (experiment?.status === "archived" && onRestore) {
            onRestore(experiment.id)
          } else {
            onSelect(event.target.value)
          }
        }}
        aria-label="Project context"
        wrapperClassName="w-auto"
        className="min-w-52 rounded-full bg-[var(--surface-container-high)] text-xs"
      >
        <option value="">{principalLabel}</option>
        {experiments.length > 0 && (
          <optgroup label="Experiments">
            {experiments.map((experiment) => (
              <option
                key={experiment.id}
                value={experiment.id}
                disabled={experiment.status === "discarded"}
              >
                {experiment.name} — {experimentStatusLabels[experiment.status]}
              </option>
            ))}
          </optgroup>
        )}
      </Select>
      {onNew && (
        <Button type="button" variant="ghost" onClick={onNew} className="h-9 rounded-full px-3 text-xs">
          <Plus className="size-3.5" /> Experiment
        </Button>
      )}
      {context.experimentId && (
        <span className="ml-auto rounded-full bg-[var(--surface-container)] px-3 py-1.5 text-[0.68rem] text-[var(--on-surface-variant)]">
          {experimentStatusLabels[context.status as ExperimentStatus] ?? context.status}
        </span>
      )}
    </div>
  )
}
