// Bridge between the app shell (palette, Home) and whichever workspace is
// visible. The shell never reaches into workspace internals: it dispatches an
// intent and the visible workspace claims it. When no workspace is open the
// intent is queued and consumed by the next workspace that finishes loading.

export type WorkspaceQuickAction = "note" | "todo" | "document" | "tidy" | "terminal"

export type QueuedQuickAction = { action: WorkspaceQuickAction; shell?: string }

export const workspaceActionEvent = "seizen:workspace-action"
export const openProjectEvent = "seizen:open-project"

let pendingQuickAction: QueuedQuickAction | null = null

export function requestWorkspaceAction(
  action: WorkspaceQuickAction,
  shell?: string,
): boolean {
  let claimed = false
  const detail = {
    action,
    shell,
    claim: () => {
      claimed = true
    },
  }
  window.dispatchEvent(new CustomEvent(workspaceActionEvent, { detail }))
  return claimed
}

export function queueQuickAction(action: WorkspaceQuickAction, shell?: string) {
  pendingQuickAction = { action, shell }
}

export function takeQuickAction(): QueuedQuickAction | null {
  const queued = pendingQuickAction
  pendingQuickAction = null
  return queued
}

export function requestOpenProject(projectId: string) {
  window.dispatchEvent(
    new CustomEvent(openProjectEvent, { detail: { projectId } }),
  )
}

export function isWorkspaceActionDetail(value: unknown): value is {
  action: WorkspaceQuickAction
  shell?: string
  claim: () => void
} {
  if (typeof value !== "object" || value === null) return false
  const detail = value as { action?: unknown; claim?: unknown }
  return (
    typeof detail.claim === "function" &&
    (detail.action === "note" ||
      detail.action === "todo" ||
      detail.action === "document" ||
      detail.action === "tidy" ||
      detail.action === "terminal")
  )
}
