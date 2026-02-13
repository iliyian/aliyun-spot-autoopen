package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// BotHandler handles Telegram bot commands
type BotHandler struct {
	botToken        string
	chatID          string
	client          *http.Client
	commandHandler  func(command string) error
	callbackHandler func(callbackID, data string, messageID int64) error
	lastUpdateID    int64
}

// NewBotHandler creates a new bot handler
func NewBotHandler(botToken, chatID string) *BotHandler {
	return &BotHandler{
		botToken: botToken,
		chatID:   chatID,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		lastUpdateID: 0,
	}
}

// SetCommandHandler sets the command handler function
func (b *BotHandler) SetCommandHandler(handler func(command string) error) {
	b.commandHandler = handler
}

// SetCallbackHandler sets the callback query handler function
func (b *BotHandler) SetCallbackHandler(handler func(callbackID, data string, messageID int64) error) {
	b.callbackHandler = handler
}

// TelegramUpdate represents a Telegram update
type TelegramUpdate struct {
	UpdateID      int64             `json:"update_id"`
	Message       *TelegramMessage  `json:"message"`
	CallbackQuery *TelegramCallback `json:"callback_query"`
}

// TelegramMessage represents a Telegram message
type TelegramMessage struct {
	MessageID int64         `json:"message_id"`
	From      *TelegramUser `json:"from"`
	Chat      *TelegramChat `json:"chat"`
	Text      string        `json:"text"`
	Date      int64         `json:"date"`
}

// TelegramUser represents a Telegram user
type TelegramUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

// TelegramChat represents a Telegram chat
type TelegramChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// TelegramCallback represents a Telegram callback query
type TelegramCallback struct {
	ID      string           `json:"id"`
	From    *TelegramUser    `json:"from"`
	Message *TelegramMessage `json:"message"`
	Data    string           `json:"data"`
}

// InlineKeyboardButton represents an inline keyboard button
type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

// InlineKeyboardMarkup represents an inline keyboard
type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

// telegramMessageWithKeyboard represents a message with inline keyboard
type telegramMessageWithKeyboard struct {
	ChatID      string                `json:"chat_id"`
	Text        string                `json:"text"`
	ParseMode   string                `json:"parse_mode"`
	ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
}

// telegramEditMessage represents an edit message request
type telegramEditMessage struct {
	ChatID      string                `json:"chat_id"`
	MessageID   int64                 `json:"message_id"`
	Text        string                `json:"text"`
	ParseMode   string                `json:"parse_mode"`
	ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
}

// telegramAnswerCallback represents an answer callback query request
type telegramAnswerCallback struct {
	CallbackQueryID string `json:"callback_query_id"`
	Text            string `json:"text,omitempty"`
	ShowAlert       bool   `json:"show_alert,omitempty"`
}

// TelegramUpdatesResponse represents the response from getUpdates
type TelegramUpdatesResponse struct {
	OK     bool             `json:"ok"`
	Result []TelegramUpdate `json:"result"`
}

// SendMessageWithKeyboard sends a message with inline keyboard
func (b *BotHandler) SendMessageWithKeyboard(text string, keyboard [][]InlineKeyboardButton) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", b.botToken)

	msg := telegramMessageWithKeyboard{
		ChatID:    b.chatID,
		Text:      text,
		ParseMode: "HTML",
		ReplyMarkup: &InlineKeyboardMarkup{
			InlineKeyboard: keyboard,
		},
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	resp, err := b.client.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram API returned status %d", resp.StatusCode)
	}

	return nil
}

// EditMessageText edits an existing message text and keyboard
func (b *BotHandler) EditMessageText(messageID int64, text string, keyboard [][]InlineKeyboardButton) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/editMessageText", b.botToken)

	msg := telegramEditMessage{
		ChatID:    b.chatID,
		MessageID: messageID,
		Text:      text,
		ParseMode: "HTML",
	}

	if keyboard != nil {
		msg.ReplyMarkup = &InlineKeyboardMarkup{
			InlineKeyboard: keyboard,
		}
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal edit message: %w", err)
	}

	resp, err := b.client.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("failed to edit message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram API returned status %d", resp.StatusCode)
	}

	return nil
}

// AnswerCallbackQuery answers a callback query
func (b *BotHandler) AnswerCallbackQuery(callbackID, text string, showAlert bool) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/answerCallbackQuery", b.botToken)

	msg := telegramAnswerCallback{
		CallbackQueryID: callbackID,
		Text:            text,
		ShowAlert:       showAlert,
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal answer callback: %w", err)
	}

	resp, err := b.client.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("failed to answer callback: %w", err)
	}
	defer resp.Body.Close()

	return nil
}

// PollUpdates polls for new updates from Telegram
func (b *BotHandler) PollUpdates() error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=30", b.botToken, b.lastUpdateID+1)

	log.Debugf("Polling updates with offset=%d", b.lastUpdateID+1)

	resp, err := b.client.Get(url)
	if err != nil {
		return fmt.Errorf("failed to get updates: %w", err)
	}
	defer resp.Body.Close()

	var updatesResp TelegramUpdatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&updatesResp); err != nil {
		return fmt.Errorf("failed to decode updates: %w", err)
	}

	if !updatesResp.OK {
		return fmt.Errorf("telegram API returned not OK")
	}

	log.Debugf("Got %d updates from Telegram", len(updatesResp.Result))

	for _, update := range updatesResp.Result {
		log.Debugf("Processing update_id=%d, lastUpdateID was %d", update.UpdateID, b.lastUpdateID)
		b.lastUpdateID = update.UpdateID

		// Handle callback query
		if update.CallbackQuery != nil {
			chatIDInt, _ := strconv.ParseInt(b.chatID, 10, 64)
			if update.CallbackQuery.Message != nil && update.CallbackQuery.Message.Chat.ID == chatIDInt {
				log.Infof("Received callback query: %s (update_id=%d)", update.CallbackQuery.Data, update.UpdateID)
				if b.callbackHandler != nil {
					if err := b.callbackHandler(update.CallbackQuery.ID, update.CallbackQuery.Data, update.CallbackQuery.Message.MessageID); err != nil {
						log.Errorf("Failed to handle callback query: %v", err)
					}
				}
			}
			continue
		}

		if update.Message == nil {
			continue
		}

		// Check if message is from authorized chat
		chatIDInt, _ := strconv.ParseInt(b.chatID, 10, 64)
		if update.Message.Chat.ID != chatIDInt {
			log.Debugf("Ignoring message from unauthorized chat: %d", update.Message.Chat.ID)
			continue
		}

		// Process command
		if strings.HasPrefix(update.Message.Text, "/") {
			command := strings.TrimPrefix(update.Message.Text, "/")
			command = strings.Split(command, " ")[0] // Get first word
			command = strings.Split(command, "@")[0] // Remove bot username if present

			log.Infof("Received command: /%s from chat %d (update_id=%d, msg_id=%d)",
				command, update.Message.Chat.ID, update.UpdateID, update.Message.MessageID)

			if b.commandHandler != nil {
				if err := b.commandHandler(command); err != nil {
					log.Errorf("Failed to handle command /%s: %v", command, err)
				}
			}
		}
	}

	return nil
}

// StartPolling starts polling for updates in a goroutine
func (b *BotHandler) StartPolling() {
	go func() {
		log.Info("Starting Telegram bot polling...")
		for {
			if err := b.PollUpdates(); err != nil {
				log.Warnf("Failed to poll updates: %v", err)
				time.Sleep(5 * time.Second)
				continue
			}
		}
	}()
}
