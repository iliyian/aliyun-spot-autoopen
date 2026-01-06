package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// TelegramNotifier sends notifications via Telegram
type TelegramNotifier struct {
	botToken string
	chatID   string
	client   *http.Client
}

// NewTelegramNotifier creates a new Telegram notifier
func NewTelegramNotifier(botToken, chatID string) *TelegramNotifier {
	return &TelegramNotifier{
		botToken: botToken,
		chatID:   chatID,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// telegramMessage represents a Telegram message
type telegramMessage struct {
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode"`
}

// Send sends a message via Telegram
func (t *TelegramNotifier) Send(message string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.botToken)

	msg := telegramMessage{
		ChatID:    t.chatID,
		Text:      message,
		ParseMode: "HTML",
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	resp, err := t.client.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram API returned status %d", resp.StatusCode)
	}

	return nil
}

// NotifyInstanceReclaimed sends a notification when an instance is reclaimed
func (t *TelegramNotifier) NotifyInstanceReclaimed(instanceID, instanceName, region string) error {
	message := fmt.Sprintf(`🔴 <b>实例被回收</b>
━━━━━━━━━━━━━━━
实例: %s
ID: <code>%s</code>
区域: %s
时间: %s
━━━━━━━━━━━━━━━
正在尝试自动启动...`,
		instanceName, instanceID, region, time.Now().Format("2006-01-02 15:04:05"))

	return t.Send(message)
}

// NotifyInstanceStarting sends a notification when an instance is starting
func (t *TelegramNotifier) NotifyInstanceStarting(instanceID, instanceName, region string) error {
	message := fmt.Sprintf(`🟡 <b>实例启动中</b>
━━━━━━━━━━━━━━━
实例: %s
ID: <code>%s</code>
区域: %s
时间: %s
━━━━━━━━━━━━━━━
正在等待健康检查...`,
		instanceName, instanceID, region, time.Now().Format("2006-01-02 15:04:05"))

	return t.Send(message)
}

// NotifyInstanceStarted sends a notification when an instance is successfully started
func (t *TelegramNotifier) NotifyInstanceStarted(instanceID, instanceName, region, publicIP string, duration time.Duration) error {
	ipInfo := "无公网IP"
	if publicIP != "" {
		ipInfo = publicIP
	}

	message := fmt.Sprintf(`✅ <b>实例已启动</b>
━━━━━━━━━━━━━━━
实例: %s
ID: <code>%s</code>
区域: %s
公网IP: <code>%s</code>
状态: Running ✓
启动耗时: %.0f 秒
━━━━━━━━━━━━━━━`,
		instanceName, instanceID, region, ipInfo, duration.Seconds())

	return t.Send(message)
}

// NotifyInstanceStartFailed sends a notification when an instance fails to start
func (t *TelegramNotifier) NotifyInstanceStartFailed(instanceID, instanceName, region string, retryCount int, err error) error {
	message := fmt.Sprintf(`❌ <b>启动失败</b>
━━━━━━━━━━━━━━━
实例: %s
ID: <code>%s</code>
区域: %s
错误: %s
重试: %d 次均失败
━━━━━━━━━━━━━━━
请手动检查！`,
		instanceName, instanceID, region, err.Error(), retryCount)

	return t.Send(message)
}

// NotifyHealthCheckTimeout sends a notification when health check times out
func (t *TelegramNotifier) NotifyHealthCheckTimeout(instanceID, instanceName, region, publicIP string, timeout int) error {
	ipInfo := "无公网IP"
	if publicIP != "" {
		ipInfo = publicIP
	}

	message := fmt.Sprintf(`⚠️ <b>健康检查超时</b>
━━━━━━━━━━━━━━━
实例: %s
ID: <code>%s</code>
区域: %s
公网IP: <code>%s</code>
检查类型: Ping
等待时间: %d 秒
━━━━━━━━━━━━━━━
实例已启动但可能未就绪，请手动检查！`,
		instanceName, instanceID, region, ipInfo, timeout)

	return t.Send(message)
}

// NotifyMonitorStarted sends a notification when the monitor starts
func (t *TelegramNotifier) NotifyMonitorStarted(instanceCount int, instances []string) error {
	instanceList := ""
	for _, inst := range instances {
		instanceList += fmt.Sprintf("\n• %s", inst)
	}

	message := fmt.Sprintf(`🚀 <b>监控已启动</b>
━━━━━━━━━━━━━━━
监控实例数: %d
时间: %s
━━━━━━━━━━━━━━━
<b>实例列表:</b>%s`,
		instanceCount, time.Now().Format("2006-01-02 15:04:05"), instanceList)

	return t.Send(message)
}