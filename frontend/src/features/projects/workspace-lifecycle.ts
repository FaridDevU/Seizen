type WorkspaceSuspender = () => Promise<void>

const suspenders = new Map<string, WorkspaceSuspender>()

export function registerWorkspaceSuspender(
  id: string,
  suspender: WorkspaceSuspender,
) {
  suspenders.set(id, suspender)
  return () => {
    if (suspenders.get(id) === suspender) suspenders.delete(id)
  }
}

export async function suspendWorkspace(id: string) {
  const suspender = suspenders.get(id)
  if (!suspender) return false
  await suspender()
  return true
}

// Suspends all open workspaces; used when closing the app.
export async function suspendAllWorkspaces() {
  const pending = [...suspenders.values()]
  if (pending.length === 0) return false
  await Promise.all(pending.map((suspend) => suspend()))
  return true
}

export async function stopProjectInOrder(
  persist: () => Promise<void>,
  cleanupRuntime: () => Promise<void>,
  closeTerminals: () => Promise<void>,
) {
  await persist()
  await cleanupRuntime()
  await closeTerminals()
}
