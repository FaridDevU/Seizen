import {
  InitializeNotifications,
  IsNotificationAvailable,
  SendNotification,
} from "../../../wailsjs/runtime/runtime"

// Native OS notifications for long-running agent work. Only fired when the
// window is hidden — in the foreground the in-app toasts already cover it.
// Every step degrades silently: an unsupported runtime just returns false.
let ready: Promise<boolean> | null = null

function ensureReady() {
  if (!ready) {
    ready = (async () => {
      try {
        if (!(await IsNotificationAvailable())) return false
        await InitializeNotifications()
        return true
      } catch {
        return false
      }
    })()
  }
  return ready
}

export async function notifyInBackground(title: string, body: string) {
  if (!document.hidden) return false
  try {
    if (!(await ensureReady())) return false
    await SendNotification({
      id: `seizen-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`,
      title,
      body,
    })
    return true
  } catch {
    return false
  }
}
