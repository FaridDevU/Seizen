import { OnFileDrop } from "../../../wailsjs/runtime/runtime"

// Wails delivers OS file drops through one global runtime callback. Several
// workspaces stay mounted at once, so this module registers that callback a single
// time and lets the visible workspace claim the drop.
type DropSubscriber = (paths: string[]) => boolean

const subscribers = new Set<DropSubscriber>()
let registered = false

export function subscribeToFileDrops(subscriber: DropSubscriber) {
  if (!registered) {
    registered = true
    OnFileDrop((_x, _y, paths) => {
      if (!Array.isArray(paths) || paths.length === 0) return
      for (const candidate of subscribers) {
        if (candidate(paths)) return
      }
    }, false)
  }
  subscribers.add(subscriber)
  return () => {
    subscribers.delete(subscriber)
  }
}
