package core

// Chat persistence for the Home assistant. The UI history lives in SQLite; the
// model's memory lives in the CLI's own session files (or is replayed from the
// stored messages for the API provider), so no process runs between turns and
// every chat is an isolated session.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type AssistantChat struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Provider  string `json:"provider"`
	UpdatedAt string `json:"updatedAt"`
}

type AssistantChatMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt string `json:"createdAt"`
}

func nowStamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func (a *App) ListAssistantChats() ([]AssistantChat, error) {
	db, err := a.database.Pool(a.context())
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(a.context(),
		`SELECT id, title, provider, updated_at FROM assistant_chats ORDER BY updated_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	chats := []AssistantChat{}
	for rows.Next() {
		var chat AssistantChat
		if err := rows.Scan(&chat.ID, &chat.Title, &chat.Provider, &chat.UpdatedAt); err != nil {
			return nil, err
		}
		chats = append(chats, chat)
	}
	return chats, rows.Err()
}

func (a *App) GetAssistantChatMessages(chatID string) ([]AssistantChatMessage, error) {
	db, err := a.database.Pool(a.context())
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(a.context(),
		`SELECT role, content, created_at FROM assistant_messages WHERE chat_id = ? ORDER BY id`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages := []AssistantChatMessage{}
	for rows.Next() {
		var message AssistantChatMessage
		if err := rows.Scan(&message.Role, &message.Content, &message.CreatedAt); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (a *App) DeleteAssistantChat(chatID string) error {
	db, err := a.database.Pool(a.context())
	if err != nil {
		return err
	}
	_, err = db.ExecContext(a.context(), `DELETE FROM assistant_chats WHERE id = ?`, chatID)
	return err
}

type assistantChatRecord struct {
	ID       string
	Provider string
	Session  string
}

// loadOrCreateAssistantChat returns the chat, creating it titled after the
// first prompt when chatID is empty.
func (a *App) loadOrCreateAssistantChat(ctx context.Context, chatID, provider, prompt string) (assistantChatRecord, error) {
	db, err := a.database.Pool(ctx)
	if err != nil {
		return assistantChatRecord{}, err
	}
	if chatID != "" {
		record := assistantChatRecord{ID: chatID}
		err := db.QueryRowContext(ctx,
			`SELECT provider, session_id FROM assistant_chats WHERE id = ?`, chatID).
			Scan(&record.Provider, &record.Session)
		if err == nil {
			return record, nil
		}
		return assistantChatRecord{}, errors.New("this chat no longer exists")
	}
	title := prompt
	if len(title) > 60 {
		title = strings.TrimSpace(title[:60]) + "…"
	}
	record := assistantChatRecord{ID: uuid.NewString(), Provider: provider}
	now := nowStamp()
	_, err = db.ExecContext(ctx,
		`INSERT INTO assistant_chats (id, title, provider, session_id, created_at, updated_at) VALUES (?, ?, ?, '', ?, ?)`,
		record.ID, title, provider, now, now)
	if err != nil {
		return assistantChatRecord{}, fmt.Errorf("could not create the chat: %w", err)
	}
	return record, nil
}

func (a *App) appendAssistantMessage(ctx context.Context, chatID, role, content string) error {
	db, err := a.database.Pool(ctx)
	if err != nil {
		return err
	}
	now := nowStamp()
	if _, err = db.ExecContext(ctx,
		`INSERT INTO assistant_messages (chat_id, role, content, created_at) VALUES (?, ?, ?, ?)`,
		chatID, role, content, now); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `UPDATE assistant_chats SET updated_at = ? WHERE id = ?`, now, chatID)
	return err
}

func (a *App) setAssistantChatSession(ctx context.Context, chatID, provider, session string) error {
	if session == "" {
		return nil
	}
	db, err := a.database.Pool(ctx)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx,
		`UPDATE assistant_chats SET session_id = ?, provider = ? WHERE id = ?`, session, provider, chatID)
	return err
}
