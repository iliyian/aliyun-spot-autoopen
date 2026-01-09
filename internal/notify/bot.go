package notify

import (
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
	botToken   string
	chatID     string
	client     *http.Client
	commandHandler func(command string) error
	lastUpdateID int64
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

// TelegramUpdate represents a Telegram update
type TelegramUpdate struct {
	UpdateID int64           `json:"update_id"`
	Message  *TelegramMessage `json:"message"`
}

// TelegramMessage represents a Telegram message
type TelegramMessage struct {
	MessageID int64            `json:"message_id"`
	From      *TelegramUser    `json:"from"`
	Chat      *TelegramChat    `json:"chat"`
	Text      string           `json:"text"`
	Date      int64            `json:"date"`
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

// TelegramUpdatesResponse represents the response from getUpdates
type TelegramUpdatesResponse struct {
	OK     bool             `json:"ok"`
	Result []TelegramUpdate `json:"result"`
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