import { useEffect, useState, type FormEvent } from "react"
import * as DialogPrimitive from "@radix-ui/react-dialog"
import { CircleAlert, MessageCircleQuestion } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { cn } from "@/lib/utils"

type ConfirmOptions = {
  title: string
  message: string
  confirmLabel?: string
  cancelLabel?: string
  tone?: "default" | "danger"
  // Optional checkbox inside the dialog; its value comes back in confirmWithOption
  optionLabel?: string
}

type PromptOptions = {
  title: string
  message?: string
  placeholder?: string
  initial?: string
  confirmLabel?: string
}

type ConfirmResult = { accepted: boolean; option: boolean }

type Request =
  | (ConfirmOptions & { kind: "confirm"; resolve: (result: ConfirmResult) => void })
  | (PromptOptions & { kind: "prompt"; resolve: (value: string | null) => void })

let enqueue: ((request: Request) => void) | null = null

export function confirmDialog(options: ConfirmOptions): Promise<boolean> {
  return confirmWithOption(options).then((result) => result.accepted)
}

export function confirmWithOption(options: ConfirmOptions): Promise<ConfirmResult> {
  return new Promise((resolve) => {
    if (!enqueue) {
      resolve({ accepted: window.confirm(options.message), option: false })
      return
    }
    enqueue({ ...options, kind: "confirm", resolve })
  })
}

export function promptDialog(options: PromptOptions): Promise<string | null> {
  return new Promise((resolve) => {
    if (!enqueue) {
      resolve(window.prompt(options.message ?? options.title, options.initial ?? ""))
      return
    }
    enqueue({ ...options, kind: "prompt", resolve })
  })
}

export function ConfirmHost() {
  const [queue, setQueue] = useState<Request[]>([])
  const [text, setText] = useState("")
  const [optionChecked, setOptionChecked] = useState(false)
  const current = queue[0]

  useEffect(() => {
    enqueue = (request) => {
      if (request.kind === "prompt") setText(request.initial ?? "")
      setQueue((pending) => [...pending, request])
    }
    return () => {
      enqueue = null
    }
  }, [])

  if (!current) return null

  const danger = current.kind === "confirm" && current.tone === "danger"

  const finish = (accepted: boolean) => {
    if (current.kind === "confirm") {
      current.resolve({ accepted, option: accepted && optionChecked })
    } else {
      current.resolve(accepted ? text : null)
    }
    setOptionChecked(false)
    setQueue((pending) => pending.slice(1))
  }

  const submit = (event: FormEvent) => {
    event.preventDefault()
    finish(true)
  }

  return (
    <DialogPrimitive.Root open onOpenChange={(open) => !open && finish(false)}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="overlay-in fixed inset-0 z-[130] bg-black/20 backdrop-blur-[3px] dark:bg-black/45" />
        <DialogPrimitive.Content asChild>
          <form
            onSubmit={submit}
            className="dialog-in fixed left-1/2 top-1/2 z-[130] w-[calc(100%-2rem)] max-w-[24rem] rounded-[1.4rem] border border-[var(--outline-variant)] bg-[var(--surface-container-high)] p-5 shadow-[0_22px_60px_var(--shadow-elevated)] outline-none"
          >
            <div className="flex items-start gap-3">
              <span
                className={cn(
                  "flex size-9 shrink-0 items-center justify-center rounded-xl",
                  danger
                    ? "bg-[var(--error-container)] text-[var(--on-error-container)]"
                    : "bg-[var(--primary-container)] text-[var(--on-primary-container)]",
                )}
              >
                {danger ? (
                  <CircleAlert className="size-4" strokeWidth={1.8} />
                ) : (
                  <MessageCircleQuestion className="size-4" strokeWidth={1.8} />
                )}
              </span>
              <div className="min-w-0">
                <DialogPrimitive.Title className="text-sm font-semibold">
                  {current.title}
                </DialogPrimitive.Title>
                {(current.kind === "confirm" || current.message) && (
                  <DialogPrimitive.Description className="mt-1 text-xs leading-5 text-[var(--on-surface-variant)]">
                    {current.message}
                  </DialogPrimitive.Description>
                )}
              </div>
            </div>

            {current.kind === "prompt" && (
              <Input
                autoFocus
                value={text}
                onChange={(event) => setText(event.target.value)}
                placeholder={current.placeholder}
                className="mt-4"
              />
            )}

            {current.kind === "confirm" && current.optionLabel && (
              <label className="mt-4 flex w-fit items-center gap-2 text-xs text-[var(--on-surface-variant)]">
                <Checkbox
                  checked={optionChecked}
                  onChange={(event) => setOptionChecked(event.target.checked)}
                />
                {current.optionLabel}
              </label>
            )}

            <div className="mt-5 flex justify-end gap-2">
              <Button
                type="button"
                variant="ghost"
                className="rounded-full"
                onClick={() => finish(false)}
              >
                {(current.kind === "confirm" && current.cancelLabel) || "Cancel"}
              </Button>
              <Button
                type="submit"
                autoFocus={current.kind === "confirm"}
                className={cn(
                  "rounded-full",
                  danger &&
                    "bg-[var(--error)] text-[var(--surface)] hover:bg-[var(--error)]/90",
                )}
              >
                {current.confirmLabel ?? "Confirm"}
              </Button>
            </div>
          </form>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  )
}
