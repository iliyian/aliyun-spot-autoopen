package monitor

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/iliyian/aliyun-spot-autoopen/internal/aliyun"
	"github.com/iliyian/aliyun-spot-autoopen/internal/config"
	"github.com/iliyian/aliyun-spot-autoopen/internal/notify"
	log "github.com/sirupsen/logrus"
)

// Monitor monitors spot instances and auto-starts them when stopped
type Monitor struct {
	cfg           *config.Config
	ecsClient     *aliyun.ECSClient
	billingClient *aliyun.BillingClient
	notifier      *notify.TelegramNotifier
	botHandler    *notify.BotHandler

	// Tracked instances
	instances []*aliyun.SpotInstance
	mu        sync.RWMutex

	// Notification cooldown tracking
	lastNotify   map[string]time.Time
	lastNotifyMu sync.RWMutex
}

// New creates a new monitor
func New(cfg *config.Config) (*Monitor, error) {
	m := &Monitor{
		cfg:        cfg,
		ecsClient:  aliyun.NewECSClient(cfg.AliyunAccessKeyID, cfg.AliyunAccessKeySecret),
		lastNotify: make(map[string]time.Time),
	}

	if cfg.TelegramEnabled {
		m.notifier = notify.NewTelegramNotifier(cfg.TelegramBotToken, cfg.TelegramChatID)
	}

	// Initialize billing client for bot commands
	if cfg.TelegramEnabled {
		billingClient, err := aliyun.NewBillingClient(cfg.AliyunAccessKeyID, cfg.AliyunAccessKeySecret)
		if err != nil {
			log.Warnf("Failed to create billing client: %v", err)
		} else {
			m.billingClient = billingClient
		}
	}

	// Initialize bot handler for commands
	if cfg.TelegramEnabled {
		m.botHandler = notify.NewBotHandler(cfg.TelegramBotToken, cfg.TelegramChatID)
		m.botHandler.SetCommandHandler(m.handleBotCommand)
	}

	return m, nil
}

// StartBot starts the Telegram bot polling
func (m *Monitor) StartBot() {
	if m.botHandler != nil {
		m.botHandler.StartPolling()
	}
}

// handleBotCommand handles bot commands
func (m *Monitor) handleBotCommand(command string) error {
	switch command {
	case "billing", "cost", "fee":
		return m.SendBillingReport(m.cfg.BillingHours)
	case "status":
		return m.sendStatusReport()
	case "help":
		return m.sendHelpMessage()
	default:
		log.Debugf("Unknown command: %s", command)
		return nil
	}
}

// sendStatusReport sends a status report
func (m *Monitor) sendStatusReport() error {
	if m.notifier == nil {
		return fmt.Errorf("telegram notifier not initialized")
	}

	m.mu.RLock()
	instances := make([]*aliyun.SpotInstance, len(m.instances))
	copy(instances, m.instances)
	m.mu.RUnlock()

	if len(instances) == 0 {
		return m.notifier.Send("📊 <b>实例状态</b>\n\n暂无监控的实例")
	}

	var sb strings.Builder
	sb.WriteString("📊 <b>实例状态</b>\n")
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

	for _, inst := range instances {
		status, err := m.ecsClient.GetInstanceStatus(inst.RegionID, inst.InstanceID)
		if err != nil {
			status = "Unknown"
		}

		statusEmoji := "🟢"
		if status == "Stopped" {
			statusEmoji = "🔴"
		} else if status == "Starting" || status == "Stopping" {
			statusEmoji = "🟡"
		}

		sb.WriteString(fmt.Sprintf("%s <b>%s</b>\n", statusEmoji, inst.InstanceName))
		sb.WriteString(fmt.Sprintf("   ID: <code>%s</code>\n", inst.InstanceID))
		sb.WriteString(fmt.Sprintf("   区域: %s\n", inst.RegionID))
		sb.WriteString(fmt.Sprintf("   状态: %s\n\n", status))
	}

	return m.notifier.Send(sb.String())
}

// sendHelpMessage sends a help message
func (m *Monitor) sendHelpMessage() error {
	if m.notifier == nil {
		return fmt.Errorf("telegram notifier not initialized")
	}

	message := fmt.Sprintf(`🤖 <b>可用命令</b>
━━━━━━━━━━━━━━━━━━━━━━━━

/billing - 查询扣费汇总（最近%d小时）
/status - 查看实例状态
/help - 显示帮助信息

━━━━━━━━━━━━━━━━━━━━━━━━
<i>别名: /cost, /fee</i>`, m.cfg.BillingHours)

	return m.notifier.Send(message)
}

// DiscoverInstances discovers all spot instances across all regions
func (m *Monitor) DiscoverInstances() error {
	instances, err := m.ecsClient.DiscoverAllSpotInstances()
	if err != nil {
		return fmt.Errorf("failed to discover instances: %w", err)
	}

	m.mu.Lock()
	m.instances = instances
	m.mu.Unlock()

	log.Infof("Discovered %d spot instances", len(instances))
	for _, inst := range instances {
		log.Infof("  - %s (%s) in %s [%s]", inst.InstanceName, inst.InstanceID, inst.RegionID, inst.Status)
	}

	// Send notification
	if m.notifier != nil && len(instances) > 0 {
		instanceList := make([]string, len(instances))
		for i, inst := range instances {
			instanceList[i] = fmt.Sprintf("%s (%s) - %s", inst.InstanceName, inst.InstanceID, inst.RegionID)
		}
		if err := m.notifier.NotifyMonitorStarted(len(instances), instanceList); err != nil {
			log.Warnf("Failed to send monitor started notification: %v", err)
		}
	}

	return nil
}

// Check checks all instances and starts stopped ones
func (m *Monitor) Check() error {
	m.mu.RLock()
	instances := make([]*aliyun.SpotInstance, len(m.instances))
	copy(instances, m.instances)
	m.mu.RUnlock()

	for _, inst := range instances {
		if err := m.checkInstance(inst); err != nil {
			log.Errorf("Failed to check instance %s: %v", inst.InstanceID, err)
		}
	}

	return nil
}

// checkInstance checks a single instance and starts it if stopped
func (m *Monitor) checkInstance(inst *aliyun.SpotInstance) error {
	// Get current status
	status, err := m.ecsClient.GetInstanceStatus(inst.RegionID, inst.InstanceID)
	if err != nil {
		return fmt.Errorf("failed to get status: %w", err)
	}

	log.Debugf("Instance %s (%s) status: %s", inst.InstanceName, inst.InstanceID, status)

	// Only handle stopped instances
	if status != "Stopped" {
		return nil
	}

	log.Warnf("Instance %s (%s) is stopped, attempting to start", inst.InstanceName, inst.InstanceID)

	// Check notification cooldown
	if !m.canNotify(inst.InstanceID) {
		log.Debugf("Notification cooldown active for instance %s", inst.InstanceID)
	} else {
		// Send reclaimed notification
		if m.notifier != nil {
			if err := m.notifier.NotifyInstanceReclaimed(inst.InstanceID, inst.InstanceName, inst.RegionID); err != nil {
				log.Warnf("Failed to send reclaimed notification: %v", err)
			}
		}
		m.updateNotifyTime(inst.InstanceID)
	}

	// Try to start the instance with retries
	startTime := time.Now()
	var lastErr error
	for i := 0; i < m.cfg.RetryCount; i++ {
		if i > 0 {
			log.Infof("Retry %d/%d for instance %s", i+1, m.cfg.RetryCount, inst.InstanceID)
			time.Sleep(time.Duration(m.cfg.RetryInterval) * time.Second)
		}

		if err := m.ecsClient.StartInstance(inst.RegionID, inst.InstanceID); err != nil {
			lastErr = err
			log.Warnf("Failed to start instance %s (attempt %d): %v", inst.InstanceID, i+1, err)
			continue
		}

		log.Infof("Start command sent for instance %s", inst.InstanceID)

		// Wait for instance to be running (using Aliyun API)
		if err := m.waitForRunning(inst.RegionID, inst.InstanceID); err != nil {
			lastErr = err
			log.Warnf("Instance %s did not reach running state: %v", inst.InstanceID, err)
			continue
		}

		// Get updated instance info for IP
		updatedInst, err := m.ecsClient.GetInstance(inst.RegionID, inst.InstanceID)
		if err != nil {
			log.Warnf("Failed to get updated instance info: %v", err)
		} else {
			inst = updatedInst
		}

		// Success!
		duration := time.Since(startTime)
		log.Infof("Instance %s started successfully in %.0f seconds", inst.InstanceID, duration.Seconds())

		if m.notifier != nil {
			if err := m.notifier.NotifyInstanceStarted(inst.InstanceID, inst.InstanceName, inst.RegionID, inst.PublicIPAddress, duration); err != nil {
				log.Warnf("Failed to send started notification: %v", err)
			}
		}

		return nil
	}

	// All retries failed
	log.Errorf("Failed to start instance %s after %d retries", inst.InstanceID, m.cfg.RetryCount)
	if m.notifier != nil {
		if err := m.notifier.NotifyInstanceStartFailed(inst.InstanceID, inst.InstanceName, inst.RegionID, m.cfg.RetryCount, lastErr); err != nil {
			log.Warnf("Failed to send failure notification: %v", err)
		}
	}

	return lastErr
}

// waitForRunning waits for an instance to reach running state
func (m *Monitor) waitForRunning(regionID, instanceID string) error {
	timeout := time.After(2 * time.Minute)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for instance to start")
		case <-ticker.C:
			status, err := m.ecsClient.GetInstanceStatus(regionID, instanceID)
			if err != nil {
				log.Warnf("Failed to get instance status: %v", err)
				continue
			}
			if status == "Running" {
				return nil
			}
			log.Debugf("Instance %s status: %s, waiting...", instanceID, status)
		}
	}
}

// canNotify checks if we can send a notification for the given instance
func (m *Monitor) canNotify(instanceID string) bool {
	m.lastNotifyMu.RLock()
	defer m.lastNotifyMu.RUnlock()

	lastTime, ok := m.lastNotify[instanceID]
	if !ok {
		return true
	}

	return time.Since(lastTime) > time.Duration(m.cfg.NotifyCooldown)*time.Second
}

// updateNotifyTime updates the last notification time for an instance
func (m *Monitor) updateNotifyTime(instanceID string) {
	m.lastNotifyMu.Lock()
	defer m.lastNotifyMu.Unlock()
	m.lastNotify[instanceID] = time.Now()
}

// SendBillingReport sends a billing report for the specified hours
func (m *Monitor) SendBillingReport(hours int) error {
	if m.billingClient == nil {
		return fmt.Errorf("billing client not initialized")
	}

	if m.notifier == nil {
		return fmt.Errorf("telegram notifier not initialized")
	}

	// Get instance info
	m.mu.RLock()
	instanceInfos := make([]aliyun.InstanceInfo, len(m.instances))
	for i, inst := range m.instances {
		instanceInfos[i] = aliyun.InstanceInfo{
			InstanceID:   inst.InstanceID,
			InstanceName: inst.InstanceName,
			RegionID:     inst.RegionID,
		}
	}
	m.mu.RUnlock()

	if len(instanceInfos) == 0 {
		log.Warn("No instances to query billing for")
		return nil
	}

	log.Infof("Querying billing for %d instances (last %d hours)...", len(instanceInfos), hours)

	// Query billing by hours
	summary, err := m.billingClient.QueryBillingByHours(instanceInfos, hours)
	if err != nil {
		return fmt.Errorf("failed to query billing: %w", err)
	}

	// Send notification with hours and monthly estimate
	if err := m.notifier.NotifyBillingSummary(summary); err != nil {
		return fmt.Errorf("failed to send billing notification: %w", err)
	}

	log.Infof("Billing report sent successfully (last %d hours, total: ¥%.4f, monthly estimate: ¥%.2f)",
		hours, summary.TotalAmount, summary.MonthlyEstimate)
	return nil
}
