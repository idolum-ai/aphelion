//go:build linux

package telegram

import "encoding/json"

type getUpdatesResponse struct {
	Ok          bool     `json:"ok"`
	Description string   `json:"description"`
	Result      []Update `json:"result"`
}

type sendMessageResponse struct {
	Ok          bool   `json:"ok"`
	Description string `json:"description"`
	Result      struct {
		MessageID int64 `json:"message_id"`
	} `json:"result"`
}

type sendVoiceResponse = sendMessageResponse

type getFileResponse struct {
	Ok          bool   `json:"ok"`
	Description string `json:"description"`
	Result      struct {
		FilePath string `json:"file_path"`
	} `json:"result"`
}

type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message"`
}

type Message struct {
	MessageID      int64           `json:"message_id"`
	Date           int64           `json:"date"`
	Chat           *Chat           `json:"chat"`
	From           *User           `json:"from"`
	Text           string          `json:"text"`
	Caption        string          `json:"caption"`
	Voice          *Voice          `json:"voice"`
	Entities       []MessageEntity `json:"entities"`
	ReplyToMessage *Message        `json:"reply_to_message"`
	Raw            json.RawMessage `json:"-"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type MessageEntity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
}

type Voice struct {
	FileID   string `json:"file_id"`
	Duration int    `json:"duration"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
}
