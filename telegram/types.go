//go:build linux

package telegram

import "encoding/json"

type getUpdatesResponse struct {
	Ok          bool                       `json:"ok"`
	Description string                     `json:"description"`
	Result      []Update                   `json:"result"`
	Parameters  telegramResponseParameters `json:"parameters,omitempty"`
}

type sendMessageResponse struct {
	Ok          bool   `json:"ok"`
	Description string `json:"description"`
	Result      struct {
		MessageID int64 `json:"message_id"`
	} `json:"result"`
}

type sendVoiceResponse = sendMessageResponse
type editMessageResponse = sendMessageResponse
type setMyCommandsResponse = telegramOKResponse
type getMeResponse struct {
	Ok          bool   `json:"ok"`
	Description string `json:"description"`
	Result      User   `json:"result"`
}

type inlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineButton `json:"inline_keyboard"`
}

type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

type InlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
}

type telegramOKResponse struct {
	Ok          bool   `json:"ok"`
	Description string `json:"description"`
}

type telegramResponseParameters struct {
	RetryAfter int `json:"retry_after,omitempty"`
}

type getFileResponse struct {
	Ok          bool   `json:"ok"`
	Description string `json:"description"`
	Result      struct {
		FilePath string `json:"file_path"`
		FileSize int64  `json:"file_size"`
	} `json:"result"`
}

type Update struct {
	UpdateID        int64                   `json:"update_id"`
	Message         *Message                `json:"message"`
	CallbackQuery   *CallbackQuery          `json:"callback_query"`
	MessageReaction *MessageReactionUpdated `json:"message_reaction"`
}

type CallbackQuery struct {
	ID       string   `json:"id"`
	From     *User    `json:"from"`
	Message  *Message `json:"message"`
	Data     string   `json:"data"`
	UpdateID int64    `json:"-"`
}

type Message struct {
	MessageID      int64           `json:"message_id"`
	Date           int64           `json:"date"`
	Chat           *Chat           `json:"chat"`
	From           *User           `json:"from"`
	Text           string          `json:"text"`
	Caption        string          `json:"caption"`
	Photo          []PhotoSize     `json:"photo"`
	Document       *Document       `json:"document"`
	Voice          *Voice          `json:"voice"`
	Audio          *Audio          `json:"audio"`
	Video          *Video          `json:"video"`
	VideoNote      *VideoNote      `json:"video_note"`
	Animation      *Animation      `json:"animation"`
	Sticker        *Sticker        `json:"sticker"`
	Contact        *Contact        `json:"contact"`
	Location       *Location       `json:"location"`
	Venue          *Venue          `json:"venue"`
	Poll           *Poll           `json:"poll"`
	Entities       []MessageEntity `json:"entities"`
	ReplyToMessage *Message        `json:"reply_to_message"`
	Raw            json.RawMessage `json:"-"`
}

type MessageReactionUpdated struct {
	Chat        *Chat           `json:"chat"`
	MessageID   int64           `json:"message_id"`
	User        *User           `json:"user"`
	ActorChat   *Chat           `json:"actor_chat"`
	Date        int64           `json:"date"`
	OldReaction []ReactionType  `json:"old_reaction"`
	NewReaction []ReactionType  `json:"new_reaction"`
	Raw         json.RawMessage `json:"-"`
}

type ReactionType struct {
	Type          string `json:"type"`
	Emoji         string `json:"emoji,omitempty"`
	CustomEmojiID string `json:"custom_emoji_id,omitempty"`
}

type Chat struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
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

type PhotoSize struct {
	FileID   string `json:"file_id"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	FileSize int64  `json:"file_size"`
}

type Document struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
}

type Audio struct {
	FileID   string `json:"file_id"`
	Duration int    `json:"duration"`
	MimeType string `json:"mime_type"`
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
}

type Video struct {
	FileID   string `json:"file_id"`
	Duration int    `json:"duration"`
	MimeType string `json:"mime_type"`
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

type VideoNote struct {
	FileID   string `json:"file_id"`
	Duration int    `json:"duration"`
	FileSize int64  `json:"file_size"`
	Length   int    `json:"length"`
}

type Animation struct {
	FileID   string `json:"file_id"`
	Duration int    `json:"duration"`
	MimeType string `json:"mime_type"`
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

type Sticker struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	IsAnimated   bool   `json:"is_animated"`
	IsVideo      bool   `json:"is_video"`
	Type         string `json:"type"`
	Emoji        string `json:"emoji"`
	SetName      string `json:"set_name"`
	MimeType     string `json:"mime_type"`
	FileSize     int64  `json:"file_size"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
}

type Contact struct {
	PhoneNumber string `json:"phone_number"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	UserID      int64  `json:"user_id"`
	VCard       string `json:"vcard"`
}

type Location struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type Venue struct {
	Location     *Location `json:"location"`
	Title        string    `json:"title"`
	Address      string    `json:"address"`
	FoursquareID string    `json:"foursquare_id"`
}

type Poll struct {
	ID       string `json:"id"`
	Question string `json:"question"`
	Type     string `json:"type"`
}
