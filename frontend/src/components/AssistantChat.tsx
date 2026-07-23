import { useEffect, useRef, useState } from "react"
import { History, Maximize2, Minimize2, Plus, Settings2, Trash2, X } from "lucide-react"

import {
  DeleteAssistantChat,
  GetAssistantChatMessages,
  ListAssistantChats,
} from "../../wailsjs/go/core/App"
import type { core } from "../../wailsjs/go/models"
import { cn } from "@/lib/utils"

export type ChatMessage = {
  role: "user" | "assistant"
  content: string
  error?: boolean
  // What the assistant did this turn, as small pills under the bubble.
  chips?: string[]
}

export type ChatSize = "compact" | "large"

// The conversation inside the morphed chat bar: messages, a history flyout,
// and a new-chat action. The input stays in the owner's bar row below.
export function AssistantChat({
  messages,
  busy,
  chatId,
  size,
  onToggleSize,
  setup,
  onLoadChat,
  onNewChat,
  onClose,
}: {
  messages: ChatMessage[]
  busy: boolean
  chatId: string
  size: ChatSize
  onToggleSize: () => void
  // When the assistant has no working provider yet: a friendly pointer to Settings.
  setup?: { message: string; onOpenSettings: () => void } | null
  onLoadChat: (chatId: string, messages: ChatMessage[]) => void
  onNewChat: () => void
  onClose: () => void
}) {
  const [historyOpen, setHistoryOpen] = useState(false)
  const [chats, setChats] = useState<core.AssistantChat[]>([])
  const scrollRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight, behavior: "smooth" })
  }, [messages, busy])

  const refreshChats = () =>
    ListAssistantChats()
      .then(setChats)
      .catch(() => setChats([]))

  useEffect(() => {
    if (historyOpen) void refreshChats()
  }, [historyOpen])

  const loadChat = async (id: string) => {
    try {
      const stored = await GetAssistantChatMessages(id)
      onLoadChat(
        id,
        stored.map((message) => ({
          role: message.role === "assistant" ? "assistant" : "user",
          content: message.content,
        })),
      )
      setHistoryOpen(false)
    } catch {
      // Chat vanished: refresh the list instead of showing a broken view.
      void refreshChats()
    }
  }

  return (
    <div className="chat-morph-content relative flex min-h-0 flex-1 flex-col">
      <div className="flex items-center justify-between gap-2 border-b border-[var(--outline-variant)] px-4 py-2.5">
        <span className="font-mono text-[0.66rem] font-semibold uppercase tracking-[0.18em] text-[var(--on-surface-variant)]">
          Assistant
        </span>
        <div className="flex items-center gap-1">
          <button
            type="button"
            aria-label={size === "compact" ? "Make the chat bigger" : "Make the chat smaller"}
            title={size === "compact" ? "Bigger" : "Smaller"}
            onClick={onToggleSize}
            className="flex size-7 items-center justify-center rounded-full text-[var(--on-surface-variant)] outline-none transition-colors hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
          >
            {size === "compact" ? (
              <Maximize2 className="size-3.5" strokeWidth={1.8} />
            ) : (
              <Minimize2 className="size-3.5" strokeWidth={1.8} />
            )}
          </button>
          <button
            type="button"
            aria-label="New chat"
            title="New chat"
            onClick={() => {
              setHistoryOpen(false)
              onNewChat()
            }}
            className="flex size-7 items-center justify-center rounded-full text-[var(--on-surface-variant)] outline-none transition-colors hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
          >
            <Plus className="size-4" strokeWidth={1.8} />
          </button>
          <button
            type="button"
            aria-label="Chat history"
            aria-expanded={historyOpen}
            title="History"
            onClick={() => setHistoryOpen((open) => !open)}
            className={cn(
              "flex size-7 items-center justify-center rounded-full text-[var(--on-surface-variant)] outline-none transition-colors hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]",
              historyOpen && "bg-[var(--primary-container)] text-[var(--on-primary-container)]",
            )}
          >
            <History className="size-4" strokeWidth={1.8} />
          </button>
          <button
            type="button"
            aria-label="Close chat"
            title="Close"
            onClick={onClose}
            className="flex size-7 items-center justify-center rounded-full text-[var(--on-surface-variant)] outline-none transition-colors hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
          >
            <X className="size-4" strokeWidth={1.8} />
          </button>
        </div>
      </div>

      <div ref={scrollRef} className="min-h-0 flex-1 space-y-3 overflow-y-auto px-4 py-3.5">
        {messages.length === 0 && !busy && !setup && (
          <p className="px-2 pt-10 text-center text-xs text-[var(--on-surface-variant)]">
            Ask anything about your spaces, or tell me what to open or create.
          </p>
        )}
        {messages.length === 0 && !busy && setup && (
          <div className="mx-auto mt-8 max-w-[18rem] rounded-2xl bg-[var(--surface-container)] px-4 py-4 text-center shadow-[inset_0_0_0_1px_var(--outline-variant)]">
            <Settings2
              className="mx-auto size-5 text-[var(--primary)]"
              strokeWidth={1.8}
            />
            <p className="mt-2 text-xs leading-relaxed text-[var(--on-surface-variant)]">
              {setup.message}
            </p>
            <button
              type="button"
              onClick={setup.onOpenSettings}
              className="mt-3 h-8 rounded-lg bg-[var(--primary)] px-3 text-xs font-semibold text-[var(--primary-foreground)] outline-none transition-opacity hover:opacity-90 focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
            >
              Open Settings
            </button>
          </div>
        )}
        {messages.map((message, index) => (
          <div
            key={index}
            className={cn("flex", message.role === "user" ? "justify-end" : "justify-start")}
          >
            <div className={cn("max-w-[85%]", message.role === "user" ? "text-right" : "")}>
              <div
                className={cn(
                  "whitespace-pre-wrap rounded-2xl px-3.5 py-2 text-left text-[0.82rem] leading-relaxed",
                  message.role === "user"
                    ? "rounded-br-md bg-[var(--primary-container)] text-[var(--on-primary-container)]"
                    : message.error
                      ? "rounded-bl-md bg-[var(--error-container)] text-[var(--on-error-container)]"
                      : "rounded-bl-md bg-[var(--surface-container)] text-[var(--on-surface)] shadow-[inset_0_0_0_1px_var(--outline-variant)]",
                )}
              >
                {message.content}
              </div>
              {message.chips && message.chips.length > 0 && (
                <div className="mt-1.5 flex flex-wrap gap-1.5">
                  {message.chips.map((chip, chipIndex) => (
                    <span
                      key={chipIndex}
                      className="max-w-full truncate rounded-full bg-[var(--primary-container)]/60 px-2.5 py-0.5 text-[0.66rem] font-medium text-[var(--on-primary-container)]"
                    >
                      {chip}
                    </span>
                  ))}
                </div>
              )}
            </div>
          </div>
        ))}
        {busy && (
          <div className="flex justify-start">
            <div className="chat-thinking flex items-center gap-1.5 rounded-2xl rounded-bl-md bg-[var(--surface-container)] px-3.5 py-2.5 shadow-[inset_0_0_0_1px_var(--outline-variant)]">
              <span />
              <span />
              <span />
            </div>
          </div>
        )}
      </div>

      {historyOpen && (
        <div className="absolute inset-x-3 top-12 z-10 max-h-[16rem] overflow-y-auto rounded-xl border border-[var(--outline-variant)] bg-[var(--surface-container-high)] py-1 shadow-[0_12px_32px_var(--shadow-elevated)] backdrop-blur-xl">
          {chats.length === 0 && (
            <p className="px-4 py-3 text-xs text-[var(--on-surface-variant)]">No chats yet.</p>
          )}
          {chats.map((chat) => (
            <div
              key={chat.id}
              className={cn(
                "group flex items-center gap-2 px-2 py-0.5",
                chat.id === chatId && "bg-[var(--state-layer)]",
              )}
            >
              <button
                type="button"
                onClick={() => void loadChat(chat.id)}
                className="min-w-0 flex-1 rounded-lg px-2 py-1.5 text-left outline-none transition-colors hover:bg-[var(--state-layer)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
              >
                <span className="block truncate text-xs font-medium text-[var(--on-surface)]">
                  {chat.title}
                </span>
              </button>
              <button
                type="button"
                aria-label={`Delete chat ${chat.title}`}
                onClick={() => {
                  void DeleteAssistantChat(chat.id)
                    .then(refreshChats)
                    .then(() => {
                      if (chat.id === chatId) onNewChat()
                    })
                    .catch(() => {})
                }}
                className="flex size-6 shrink-0 items-center justify-center rounded-full text-[var(--on-surface-variant)] opacity-0 outline-none transition-opacity hover:text-[var(--error)] focus-visible:opacity-100 focus-visible:ring-2 focus-visible:ring-[var(--ring)] group-hover:opacity-100"
              >
                <Trash2 className="size-3.5" strokeWidth={1.7} />
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
