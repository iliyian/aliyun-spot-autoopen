package monitor

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/iliyian/aliyun-spot-manager/internal/aliyun"
	"github.com/iliyian/aliyun-spot-manager/internal/config"
	"github.com/iliyian/aliyun-spot-manager/internal/gcp"
	"github.com/iliyian/aliyun-spot-manager/internal/notify"
	log "github.com/sirupsen/logrus"
)

// AliyunAccountClients holds all clients for a single Aliyun account
type AliyunAccountClients struct {
	Account       config.AliyunAccount
	ECSClient     *aliyun.ECSClient
	BillingClient *aliyun.BillingClient
	TrafficClient *aliyun.TrafficClient
	CBWPClient    *aliyun.CBWPClient
}

// Monitor monitors spot instances and auto-starts them when stopped
type Monitor struct {
	cfg           *config.Config
	aliyunClients []*AliyunAccountClients
	gcpClient     *gcp.ComputeClient
	notifier      *notify.TelegramNotifier
	botHandler    *notify.BotHandler

	// Tracked instances
	instances    []*aliyun.SpotInstance
	gcpInstances []*gcp.PreemptibleInstance
	mu           sync.RWMutex

	// Notification cooldown tracking
	lastNotify   map[string]time.Time
	lastNotifyMu sync.RWMutex

	// NoStock tracking - instances that cannot start due to resource sold out
	noStockInstances   map[string]bool
	noStockInstancesMu sync.RWMutex

	// Traffic shutdown tracking (per-account, independent for China/non-China)
	chinaShutdown     map[string]bool // account label -> shutdown state
	nonChinaShutdown  map[string]bool // account label -> shutdown state
	trafficShutdownMu sync.RWMutex
}

// New creates a new monitor
func New(cfg *config.Config) (*Monitor, error) {
	m := &Monitor{
		cfg:              cfg,
		lastNotify:       make(map[string]time.Time),
		noStockInstances: make(map[string]bool),
		chinaShutdown:    make(map[string]bool),
		nonChinaShutdown: make(map[string]bool),
	}

	if cfg.TelegramEnabled {
		m.notifier = notify.NewTelegramNotifier(cfg.TelegramBotToken, cfg.TelegramChatID)
	}

	// Initialize Aliyun clients for each account
	for _, acc := range cfg.AliyunAccounts {
		clients := &AliyunAccountClients{
			Account:   acc,
			ECSClient: aliyun.NewECSClient(acc.AccessKeyID, acc.AccessKeySecret),
		}

		if cfg.TelegramEnabled {
			billingClient, err := aliyun.NewBillingClient(acc.AccessKeyID, acc.AccessKeySecret)
			if err != nil {
				log.Warnf("[%s] Failed to create billing client: %v", acc.Label, err)
			} else {
				clients.BillingClient = billingClient
			}

			clients.CBWPClient = aliyun.NewCBWPClient(acc.AccessKeyID, acc.AccessKeySecret)
		}

		// Traffic client for bot commands or traffic shutdown
		if cfg.TelegramEnabled || cfg.TrafficShutdownEnabled {
			trafficClient, err := aliyun.NewTrafficClient(acc.AccessKeyID, acc.AccessKeySecret)
			if err != nil {
				log.Warnf("[%s] Failed to create traffic client: %v", acc.Label, err)
			} else {
				clients.TrafficClient = trafficClient
			}
		}

		m.aliyunClients = append(m.aliyunClients, clients)
	}

	// Initialize bot handler for commands
	if cfg.TelegramEnabled {
		m.botHandler = notify.NewBotHandler(cfg.TelegramBotToken, cfg.TelegramChatID)
		m.botHandler.SetCommandHandler(m.handleBotCommand)
		m.botHandler.SetCallbackHandler(m.handleCallbackQuery)
	}

	// Initialize GCP client
	if cfg.GCPEnabled {
		gcpClient, err := gcp.NewComputeClient(cfg.GCPProjectID, cfg.GCPCredentialsJSON)
		if err != nil {
			return nil, fmt.Errorf("failed to create GCP client: %w", err)
		}
		m.gcpClient = gcpClient
	}

	return m, nil
}

// getECSClientByLabel returns the ECS client for a specific account label
func (m *Monitor) getECSClientByLabel(label string) *aliyun.ECSClient {
	for _, c := range m.aliyunClients {
		if c.Account.Label == label {
			return c.ECSClient
		}
	}
	return nil
}

// getCBWPClientByLabel returns the CBWP client for a specific account label
func (m *Monitor) getCBWPClientByLabel(label string) *aliyun.CBWPClient {
	for _, c := range m.aliyunClients {
		if c.Account.Label == label {
			return c.CBWPClient
		}
	}
	return nil
}

// StartBot starts the Telegram bot polling and registers commands
func (m *Monitor) StartBot() {
	if m.botHandler != nil {
		// Register bot commands with Telegram (same functionality only registers one command)
		commands := []notify.BotCommand{
			{Command: "status", Description: "查看实例状态"},
			{Command: "billing", Description: "查询本月扣费汇总"},
			{Command: "traffic", Description: "查询本月流量统计"},
			{Command: "cbwp", Description: "管理共享带宽包"},
			{Command: "help", Description: "显示帮助信息"},
		}
		if err := m.botHandler.SetMyCommands(commands); err != nil {
			log.Warnf("Failed to register bot commands: %v", err)
		}

		m.botHandler.StartPolling()
	}
}

// handleBotCommand handles bot commands
func (m *Monitor) handleBotCommand(command string) error {
	switch command {
	case "billing", "cost", "fee":
		return m.SendBillingReport()
	case "traffic", "flow", "bandwidth":
		return m.SendTrafficReport()
	case "status":
		return m.sendStatusReport()
	case "cbwp":
		return m.sendCBWPInstanceList()
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
	gcpInstances := make([]*gcp.PreemptibleInstance, len(m.gcpInstances))
	copy(gcpInstances, m.gcpInstances)
	m.mu.RUnlock()

	if len(instances) == 0 && len(gcpInstances) == 0 {
		return m.notifier.Send("📊 <b>实例状态</b>\n\n暂无监控的实例")
	}

	var sb strings.Builder
	sb.WriteString("📊 <b>实例状态</b>\n")
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

	// Group instances by account label
	instancesByAccount := make(map[string][]*aliyun.SpotInstance)
	for _, inst := range instances {
		instancesByAccount[inst.AccountLabel] = append(instancesByAccount[inst.AccountLabel], inst)
	}

	for _, acc := range m.aliyunClients {
		label := acc.Account.Label
		insts, ok := instancesByAccount[label]
		if !ok {
			continue
		}

		if label != "" {
			sb.WriteString(fmt.Sprintf("👤 <b>账号: %s</b>\n", label))
		}

		for _, inst := range insts {
			status, err := acc.ECSClient.GetInstanceStatus(inst.RegionID, inst.InstanceID)
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
	}

	// GCP instances
	if len(gcpInstances) > 0 {
		sb.WriteString("☁️ <b>GCP</b>\n")
		for _, inst := range gcpInstances {
			status, err := m.gcpClient.GetInstanceStatus(inst.Zone, inst.InstanceName)
			if err != nil {
				status = "Unknown"
			}

			statusEmoji := "🟢"
			switch status {
			case "TERMINATED", "STOPPED":
				statusEmoji = "🔴"
			case "STAGING", "STOPPING", "SUSPENDING":
				statusEmoji = "🟡"
			case "SUSPENDED":
				statusEmoji = "🟠"
			}

			sb.WriteString(fmt.Sprintf("%s <b>%s</b>\n", statusEmoji, inst.InstanceName))
			sb.WriteString(fmt.Sprintf("   区域: %s\n", inst.Zone))
			sb.WriteString(fmt.Sprintf("   状态: %s\n\n", status))
		}
	}

	return m.notifier.Send(sb.String())
}

// sendHelpMessage sends a help message
func (m *Monitor) sendHelpMessage() error {
	if m.notifier == nil {
		return fmt.Errorf("telegram notifier not initialized")
	}

	message := `🤖 <b>可用命令</b>
━━━━━━━━━━━━━━━━━━━━━━━━

/billing - 查询本月扣费汇总
/traffic - 查询本月流量统计
/status - 查看实例状态
/cbwp - 管理共享带宽包
/help - 显示帮助信息

━━━━━━━━━━━━━━━━
<i>别名: /cost, /fee, /flow, /bandwidth</i>`

	return m.notifier.Send(message)
}

// refreshInstances re-discovers spot instances and updates the tracked list.
func (m *Monitor) refreshInstances() error {
	var allInstances []*aliyun.SpotInstance
	for _, acc := range m.aliyunClients {
		instances, err := acc.ECSClient.DiscoverAllSpotInstances(acc.Account.Label)
		if err != nil {
			log.Warnf("[%s] Failed to discover instances: %v", acc.Account.Label, err)
			continue
		}
		allInstances = append(allInstances, instances...)
	}

	m.mu.Lock()
	oldMap := make(map[string]bool, len(m.instances))
	for _, inst := range m.instances {
		oldMap[inst.InstanceID] = true
	}
	newMap := make(map[string]bool, len(allInstances))
	for _, inst := range allInstances {
		newMap[inst.InstanceID] = true
	}

	for _, inst := range allInstances {
		if !oldMap[inst.InstanceID] {
			log.Infof("[%s] New instance discovered: %s (%s) in %s", inst.AccountLabel, inst.InstanceName, inst.InstanceID, inst.RegionID)
		}
	}
	for _, inst := range m.instances {
		if !newMap[inst.InstanceID] {
			log.Infof("[%s] Instance removed: %s (%s) in %s", inst.AccountLabel, inst.InstanceName, inst.InstanceID, inst.RegionID)
		}
	}

	m.instances = allInstances
	m.mu.Unlock()

	// Refresh GCP instances
	if m.gcpClient != nil {
		m.refreshGCPInstances()
	}

	return nil
}

// refreshGCPInstances re-discovers GCP preemptible instances
func (m *Monitor) refreshGCPInstances() {
	gcpInstances, err := m.gcpClient.DiscoverAllPreemptibleInstances(m.cfg.GCPZones)
	if err != nil {
		log.Warnf("Failed to refresh GCP instances: %v", err)
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	oldMap := make(map[string]bool, len(m.gcpInstances))
	for _, inst := range m.gcpInstances {
		oldMap[inst.Zone+"/"+inst.InstanceName] = true
	}
	newMap := make(map[string]bool, len(gcpInstances))
	for _, inst := range gcpInstances {
		newMap[inst.Zone+"/"+inst.InstanceName] = true
	}

	for _, inst := range gcpInstances {
		key := inst.Zone + "/" + inst.InstanceName
		if !oldMap[key] {
			log.Infof("GCP: New instance discovered: %s in %s", inst.InstanceName, inst.Zone)
		}
	}
	for _, inst := range m.gcpInstances {
		key := inst.Zone + "/" + inst.InstanceName
		if !newMap[key] {
			log.Infof("GCP: Instance removed: %s in %s", inst.InstanceName, inst.Zone)
		}
	}

	m.gcpInstances = gcpInstances
}

// DiscoverInstances discovers all spot instances across all accounts and regions
func (m *Monitor) DiscoverInstances() error {
	var allInstances []*aliyun.SpotInstance
	for _, acc := range m.aliyunClients {
		instances, err := acc.ECSClient.DiscoverAllSpotInstances(acc.Account.Label)
		if err != nil {
			log.Warnf("[%s] Failed to discover instances: %v", acc.Account.Label, err)
			continue
		}
		allInstances = append(allInstances, instances...)
	}

	m.mu.Lock()
	m.instances = allInstances
	m.mu.Unlock()

	log.Infof("Discovered total %d spot instances", len(allInstances))
	for _, inst := range allInstances {
		log.Infof("[%s]  - %s (%s) in %s [%s]", inst.AccountLabel, inst.InstanceName, inst.InstanceID, inst.RegionID, inst.Status)
	}

	// Discover GCP instances
	if m.gcpClient != nil {
		gcpInstances, err := m.gcpClient.DiscoverAllPreemptibleInstances(m.cfg.GCPZones)
		if err != nil {
			log.Warnf("Failed to discover GCP instances: %v", err)
		} else {
			m.mu.Lock()
			m.gcpInstances = gcpInstances
			m.mu.Unlock()

			log.Infof("Discovered %d GCP preemptible instances", len(gcpInstances))
			for _, inst := range gcpInstances {
				log.Infof("  - %s in %s [%s]", inst.InstanceName, inst.Zone, inst.Status)
			}
		}
	}

	// Send notification
	if m.notifier != nil {
		totalCount := len(allInstances)
		instanceList := make([]string, 0)
		for _, inst := range allInstances {
			label := ""
			if inst.AccountLabel != "" {
				label = fmt.Sprintf("[%s] ", inst.AccountLabel)
			}
			instanceList = append(instanceList, fmt.Sprintf("%s%s (%s) - %s", label, inst.InstanceName, inst.InstanceID, inst.RegionID))
		}

		m.mu.RLock()
		gcpInsts := m.gcpInstances
		m.mu.RUnlock()

		for _, inst := range gcpInsts {
			totalCount++
			instanceList = append(instanceList, fmt.Sprintf("[GCP] %s - %s", inst.InstanceName, inst.Zone))
		}

		if totalCount > 0 {
			if err := m.notifier.NotifyMonitorStarted(totalCount, instanceList); err != nil {
				log.Warnf("Failed to send monitor started notification: %v", err)
			}
		}
	}

	return nil
}

// Check checks all instances and starts stopped ones
func (m *Monitor) Check() error {
	// Re-discover instances to pick up newly added or removed ones
	if err := m.refreshInstances(); err != nil {
		log.Warnf("Failed to refresh instances, using cached list: %v", err)
	}

	m.mu.RLock()
	instances := make([]*aliyun.SpotInstance, len(m.instances))
	copy(instances, m.instances)
	gcpInstances := make([]*gcp.PreemptibleInstance, len(m.gcpInstances))
	copy(gcpInstances, m.gcpInstances)
	m.mu.RUnlock()

	for _, inst := range instances {
		if err := m.checkInstance(inst); err != nil {
			log.Errorf("[%s] Failed to check instance %s: %v", inst.AccountLabel, inst.InstanceID, err)
		}
	}

	// Check GCP instances
	for _, inst := range gcpInstances {
		if err := m.checkGCPInstance(inst); err != nil {
			log.Errorf("Failed to check GCP instance %s: %v", inst.InstanceName, err)
		}
	}

	return nil
}

// checkInstance checks a single instance and starts it if stopped
func (m *Monitor) checkInstance(inst *aliyun.SpotInstance) error {
	// Find the correct client for this account
	ecsClient := m.getECSClientByLabel(inst.AccountLabel)
	if ecsClient == nil {
		return fmt.Errorf("no ECS client found for account %s", inst.AccountLabel)
	}

	// Check if this instance is blocked by traffic shutdown
	m.trafficShutdownMu.RLock()
	isChina := aliyun.IsChinaMainlandRegion(inst.RegionID)
	// Traffic shutdown is now per-account
	blocked := (isChina && m.chinaShutdown[inst.AccountLabel]) || (!isChina && m.nonChinaShutdown[inst.AccountLabel])
	m.trafficShutdownMu.RUnlock()

	if blocked {
		log.Debugf("[%s] Instance %s (%s) skipped: traffic shutdown active for %s region",
			inst.AccountLabel, inst.InstanceName, inst.InstanceID, inst.RegionID)
		return nil
	}

	// Get current status
	status, err := ecsClient.GetInstanceStatus(inst.RegionID, inst.InstanceID)
	if err != nil {
		return fmt.Errorf("failed to get status: %w", err)
	}

	log.Debugf("[%s] Instance %s (%s) status: %s", inst.AccountLabel, inst.InstanceName, inst.InstanceID, status)

	// If instance is running, clear NoStock flag if it was set
	if status == "Running" {
		m.noStockInstancesMu.Lock()
		if m.noStockInstances[inst.InstanceID] {
			log.Infof("[%s] Instance %s (%s) is running, clearing NoStock flag", inst.AccountLabel, inst.InstanceName, inst.InstanceID)
			delete(m.noStockInstances, inst.InstanceID)
		}
		m.noStockInstancesMu.Unlock()
		return nil
	}

	// Only handle stopped instances
	if status != "Stopped" {
		return nil
	}

	// Check if this instance is blocked by NoStock
	m.noStockInstancesMu.RLock()
	noStock := m.noStockInstances[inst.InstanceID]
	m.noStockInstancesMu.RUnlock()

	if noStock {
		log.Debugf("[%s] Instance %s (%s) skipped: resource sold out (NoStock), waiting for availability",
			inst.AccountLabel, inst.InstanceName, inst.InstanceID)
		return nil
	}

	log.Warnf("[%s] Instance %s (%s) is stopped, attempting to start", inst.AccountLabel, inst.InstanceName, inst.InstanceID)

	// Check notification cooldown
	if !m.canNotify(inst.InstanceID) {
		log.Debugf("[%s] Notification cooldown active for instance %s", inst.AccountLabel, inst.InstanceID)
	} else {
		// Send reclaimed notification
		if m.notifier != nil {
			if err := m.notifier.NotifyInstanceReclaimed(inst.InstanceID, inst.InstanceName, inst.RegionID); err != nil {
				log.Warnf("[%s] Failed to send reclaimed notification: %v", inst.AccountLabel, err)
			}
		}
		m.updateNotifyTime(inst.InstanceID)
	}

	// Try to start the instance with retries
	startTime := time.Now()
	var lastErr error
	noStockDetected := false
	attemptCount := 0
	for i := 0; i < m.cfg.RetryCount; i++ {
		attemptCount = i + 1
		if i > 0 {
			log.Infof("[%s] Retry %d/%d for instance %s", inst.AccountLabel, i+1, m.cfg.RetryCount, inst.InstanceID)
			time.Sleep(time.Duration(m.cfg.RetryInterval) * time.Second)
		}

		if err := ecsClient.StartInstance(inst.RegionID, inst.InstanceID); err != nil {
			lastErr = err
			log.Warnf("[%s] Failed to start instance %s (attempt %d): %v", inst.AccountLabel, inst.InstanceID, i+1, err)

			// Check if it's a NoStock error - stop retrying immediately
			if aliyun.IsNoStockError(err) {
				log.Warnf("[%s] Instance %s: resource sold out (NoStock), stopping retries", inst.AccountLabel, inst.InstanceID)
				noStockDetected = true
				break
			}

			continue
		}

		log.Infof("[%s] Start command sent for instance %s", inst.AccountLabel, inst.InstanceID)

		// Wait for instance to be running (using Aliyun API)
		if err := m.waitForRunning(ecsClient, inst.RegionID, inst.InstanceID, inst.AccountLabel); err != nil {
			lastErr = err
			log.Warnf("[%s] Instance %s did not reach running state: %v", inst.AccountLabel, inst.InstanceID, err)
			continue
		}

		// Get updated instance info for IP
		updatedInst, err := ecsClient.GetInstance(inst.RegionID, inst.InstanceID, inst.AccountLabel)
		if err != nil {
			log.Warnf("[%s] Failed to get updated instance info: %v", inst.AccountLabel, err)
		} else {
			inst = updatedInst
		}

		// Success!
		duration := time.Since(startTime)
		log.Infof("[%s] Instance %s started successfully in %.0f seconds", inst.AccountLabel, inst.InstanceID, duration.Seconds())

		if m.notifier != nil {
			if err := m.notifier.NotifyInstanceStarted(inst.InstanceID, inst.InstanceName, inst.RegionID, inst.PublicIPAddress, duration); err != nil {
				log.Warnf("[%s] Failed to send started notification: %v", inst.AccountLabel, err)
			}
		}

		return nil
	}

	// Handle NoStock: set flag and send specific notification, stop auto-restart
	if noStockDetected {
		m.noStockInstancesMu.Lock()
		m.noStockInstances[inst.InstanceID] = true
		m.noStockInstancesMu.Unlock()

		log.Errorf("[%s] Instance %s marked as NoStock, auto-restart paused", inst.AccountLabel, inst.InstanceID)
		if m.notifier != nil {
			if err := m.notifier.NotifyInstanceNoStock(inst.InstanceID, inst.InstanceName, inst.RegionID, attemptCount); err != nil {
				log.Warnf("[%s] Failed to send NoStock notification: %v", inst.AccountLabel, err)
			}
		}

		return lastErr
	}

	// All retries failed (non-NoStock errors)
	log.Errorf("[%s] Failed to start instance %s after %d retries", inst.AccountLabel, inst.InstanceID, m.cfg.RetryCount)
	if m.notifier != nil {
		if err := m.notifier.NotifyInstanceStartFailed(inst.InstanceID, inst.InstanceName, inst.RegionID, m.cfg.RetryCount, lastErr); err != nil {
			log.Warnf("[%s] Failed to send failure notification: %v", inst.AccountLabel, err)
		}
	}

	return lastErr
}

// waitForRunning waits for an instance to reach running state
func (m *Monitor) waitForRunning(ecsClient *aliyun.ECSClient, regionID, instanceID, accountLabel string) error {
	timeout := time.After(2 * time.Minute)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for instance to start")
		case <-ticker.C:
			status, err := ecsClient.GetInstanceStatus(regionID, instanceID)
			if err != nil {
				log.Warnf("[%s] Failed to get instance status: %v", accountLabel, err)
				continue
			}
			if status == "Running" {
				return nil
			}
			log.Debugf("[%s] Instance %s status: %s, waiting...", accountLabel, instanceID, status)
		}
	}
}

// checkGCPInstance checks a single GCP instance and starts it if stopped/terminated
func (m *Monitor) checkGCPInstance(inst *gcp.PreemptibleInstance) error {
	// Get current status
	status, err := m.gcpClient.GetInstanceStatus(inst.Zone, inst.InstanceName)
	if err != nil {
		return fmt.Errorf("failed to get GCP instance status: %w", err)
	}

	log.Debugf("GCP instance %s (%s) status: %s", inst.InstanceName, inst.Zone, status)

	// Only handle stopped/terminated instances
	if status != "TERMINATED" && status != "STOPPED" {
		return nil
	}

	log.Warnf("GCP instance %s (%s) is %s, attempting to start", inst.InstanceName, inst.Zone, status)

	// Notification key uses "gcp:" prefix to avoid collision with Aliyun instance IDs
	notifyKey := "gcp:" + inst.Zone + "/" + inst.InstanceName

	if !m.canNotify(notifyKey) {
		log.Debugf("Notification cooldown active for GCP instance %s", inst.InstanceName)
	} else {
		if m.notifier != nil {
			if err := m.notifier.NotifyInstanceReclaimed(inst.InstanceName, inst.InstanceName, "GCP/"+inst.Zone); err != nil {
				log.Warnf("Failed to send GCP reclaimed notification: %v", err)
			}
		}
		m.updateNotifyTime(notifyKey)
	}

	// Try to start the instance with retries
	startTime := time.Now()
	var lastErr error
	for i := 0; i < m.cfg.RetryCount; i++ {
		if i > 0 {
			log.Infof("GCP: Retry %d/%d for instance %s", i+1, m.cfg.RetryCount, inst.InstanceName)
			time.Sleep(time.Duration(m.cfg.RetryInterval) * time.Second)
		}

		if err := m.gcpClient.StartInstance(inst.Zone, inst.InstanceName); err != nil {
			lastErr = err
			log.Warnf("Failed to start GCP instance %s (attempt %d): %v", inst.InstanceName, i+1, err)
			continue
		}

		// Wait for instance to be running
		if err := m.waitForGCPRunning(inst.Zone, inst.InstanceName); err != nil {
			lastErr = err
			log.Warnf("GCP instance %s did not reach running state: %v", inst.InstanceName, err)
			continue
		}

		// Get updated instance info
		updatedInst, err := m.gcpClient.GetInstance(inst.Zone, inst.InstanceName)
		if err != nil {
			log.Warnf("Failed to get updated GCP instance info: %v", err)
		} else {
			inst = updatedInst
		}

		duration := time.Since(startTime)
		log.Infof("GCP instance %s started successfully in %.0f seconds", inst.InstanceName, duration.Seconds())

		if m.notifier != nil {
			if err := m.notifier.NotifyInstanceStarted(inst.InstanceName, inst.InstanceName, "GCP/"+inst.Zone, inst.ExternalIP, duration); err != nil {
				log.Warnf("Failed to send GCP started notification: %v", err)
			}
		}

		return nil
	}

	// All retries failed
	log.Errorf("Failed to start GCP instance %s after %d retries", inst.InstanceName, m.cfg.RetryCount)
	if m.notifier != nil {
		if err := m.notifier.NotifyInstanceStartFailed(inst.InstanceName, inst.InstanceName, "GCP/"+inst.Zone, m.cfg.RetryCount, lastErr); err != nil {
			log.Warnf("Failed to send GCP failure notification: %v", err)
		}
	}

	return lastErr
}

// waitForGCPRunning waits for a GCP instance to reach RUNNING state
func (m *Monitor) waitForGCPRunning(zone, instanceName string) error {
	timeout := time.After(2 * time.Minute)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for GCP instance to start")
		case <-ticker.C:
			status, err := m.gcpClient.GetInstanceStatus(zone, instanceName)
			if err != nil {
				log.Warnf("Failed to get GCP instance status: %v", err)
				continue
			}
			if status == "RUNNING" {
				return nil
			}
			log.Debugf("GCP instance %s status: %s, waiting...", instanceName, status)
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

// SendBillingReport sends billing reports for all accounts
func (m *Monitor) SendBillingReport() error {
	if m.notifier == nil {
		return fmt.Errorf("telegram notifier not initialized")
	}

	m.mu.RLock()
	instancesByAccount := make(map[string][]aliyun.InstanceInfo)
	for _, inst := range m.instances {
		instancesByAccount[inst.AccountLabel] = append(instancesByAccount[inst.AccountLabel], aliyun.InstanceInfo{
			InstanceID:   inst.InstanceID,
			InstanceName: inst.InstanceName,
			RegionID:     inst.RegionID,
		})
	}
	m.mu.RUnlock()

	for _, acc := range m.aliyunClients {
		if acc.BillingClient == nil {
			log.Warnf("[%s] Billing client not initialized", acc.Account.Label)
			continue
		}

		instanceInfos := instancesByAccount[acc.Account.Label]
		if len(instanceInfos) == 0 {
			log.Debugf("[%s] No instances to query billing for", acc.Account.Label)
			continue
		}

		log.Infof("[%s] Querying billing for %d instances...", acc.Account.Label, len(instanceInfos))

		summary, err := acc.BillingClient.QueryBilling(instanceInfos, acc.Account.Label)
		if err != nil {
			log.Errorf("[%s] Failed to query billing: %v", acc.Account.Label, err)
			continue
		}

		if err := m.notifier.NotifyBillingSummary(summary); err != nil {
			log.Errorf("[%s] Failed to send billing notification: %v", acc.Account.Label, err)
		}
	}

	return nil
}

// SendTrafficReport sends traffic reports for all accounts
func (m *Monitor) SendTrafficReport() error {
	if m.notifier == nil {
		return fmt.Errorf("telegram notifier not initialized")
	}

	for _, acc := range m.aliyunClients {
		if acc.TrafficClient == nil {
			log.Warnf("[%s] Traffic client not initialized", acc.Account.Label)
			continue
		}

		log.Infof("[%s] Querying traffic data...", acc.Account.Label)

		summary, err := acc.TrafficClient.QueryInternetTraffic(acc.Account.Label)
		if err != nil {
			log.Errorf("[%s] Failed to query traffic: %v", acc.Account.Label, err)
			continue
		}

		if m.cfg.TrafficShutdownEnabled {
			m.trafficShutdownMu.RLock()
			chinaSD := m.chinaShutdown[acc.Account.Label]
			nonChinaSD := m.nonChinaShutdown[acc.Account.Label]
			m.trafficShutdownMu.RUnlock()

			if err := m.notifier.NotifyTrafficSummaryWithLimits(summary,
				m.cfg.TrafficLimitChinaGB, m.cfg.TrafficLimitNonChinaGB,
				chinaSD, nonChinaSD); err != nil {
				log.Errorf("[%s] Failed to send traffic notification: %v", acc.Account.Label, err)
			}
		} else {
			if err := m.notifier.NotifyTrafficSummary(summary); err != nil {
				log.Errorf("[%s] Failed to send traffic notification: %v", acc.Account.Label, err)
			}
		}
	}

	return nil
}

// CheckTraffic checks traffic usage for all accounts and stops instances if limits are exceeded
func (m *Monitor) CheckTraffic() error {
	if !m.cfg.TrafficShutdownEnabled {
		return nil
	}

	for _, acc := range m.aliyunClients {
		if acc.TrafficClient == nil {
			continue
		}

		log.Debugf("[%s] Checking traffic limits...", acc.Account.Label)

		summary, err := acc.TrafficClient.QueryInternetTraffic(acc.Account.Label)
		if err != nil {
			log.Errorf("[%s] Failed to query traffic: %v", acc.Account.Label, err)
			continue
		}

		chinaTrafficGB := summary.ChinaMainland.TrafficGB
		nonChinaTrafficGB := summary.NonChinaMainland.TrafficGB

		log.Debugf("[%s] Traffic check: China=%.2f/%.0f GB, Non-China=%.2f/%.0f GB",
			acc.Account.Label, chinaTrafficGB, m.cfg.TrafficLimitChinaGB,
			nonChinaTrafficGB, m.cfg.TrafficLimitNonChinaGB)

		m.trafficShutdownMu.Lock()
		// Check China mainland traffic
		if chinaTrafficGB >= m.cfg.TrafficLimitChinaGB {
			if !m.chinaShutdown[acc.Account.Label] {
				m.chinaShutdown[acc.Account.Label] = true
				log.Warnf("[%s] China mainland traffic %.2f GB exceeded limit %.0f GB, shutting down China instances",
					acc.Account.Label, chinaTrafficGB, m.cfg.TrafficLimitChinaGB)
				go m.shutdownRegionInstances(acc.Account.Label, "china", chinaTrafficGB, m.cfg.TrafficLimitChinaGB)
			}
		} else if m.chinaShutdown[acc.Account.Label] {
			log.Infof("[%s] China mainland traffic %.2f GB is below limit %.0f GB, clearing shutdown flag",
				acc.Account.Label, chinaTrafficGB, m.cfg.TrafficLimitChinaGB)
			m.chinaShutdown[acc.Account.Label] = false
		}

		// Check non-China traffic
		if nonChinaTrafficGB >= m.cfg.TrafficLimitNonChinaGB {
			if !m.nonChinaShutdown[acc.Account.Label] {
				m.nonChinaShutdown[acc.Account.Label] = true
				log.Warnf("[%s] Non-China traffic %.2f GB exceeded limit %.0f GB, shutting down non-China instances",
					acc.Account.Label, nonChinaTrafficGB, m.cfg.TrafficLimitNonChinaGB)
				go m.shutdownRegionInstances(acc.Account.Label, "non-china", nonChinaTrafficGB, m.cfg.TrafficLimitNonChinaGB)
			}
		} else if m.nonChinaShutdown[acc.Account.Label] {
			log.Infof("[%s] Non-China traffic %.2f GB is below limit %.0f GB, clearing shutdown flag",
				acc.Account.Label, nonChinaTrafficGB, m.cfg.TrafficLimitNonChinaGB)
			m.nonChinaShutdown[acc.Account.Label] = false
		}
		m.trafficShutdownMu.Unlock()
	}

	return nil
}

// shutdownRegionInstances stops all running instances for a specific account in the specified region group
func (m *Monitor) shutdownRegionInstances(accountLabel string, region string, trafficGB, limitGB float64) {
	ecsClient := m.getECSClientByLabel(accountLabel)
	if ecsClient == nil {
		return
	}

	m.mu.RLock()
	var instances []*aliyun.SpotInstance
	for _, inst := range m.instances {
		if inst.AccountLabel == accountLabel {
			instances = append(instances, inst)
		}
	}
	m.mu.RUnlock()

	var stoppedInstances []string

	for _, inst := range instances {
		isChina := aliyun.IsChinaMainlandRegion(inst.RegionID)
		if (region == "china" && !isChina) || (region == "non-china" && isChina) {
			continue
		}

		// Check if instance is running
		status, err := ecsClient.GetInstanceStatus(inst.RegionID, inst.InstanceID)
		if err != nil {
			log.Errorf("[%s] Failed to get status for instance %s: %v", accountLabel, inst.InstanceID, err)
			continue
		}

		if status != "Running" {
			continue
		}

		log.Warnf("[%s] Stopping instance %s (%s) due to traffic limit exceeded", accountLabel, inst.InstanceName, inst.InstanceID)
		if err := ecsClient.StopInstance(inst.RegionID, inst.InstanceID, "StopCharging"); err != nil {
			log.Errorf("[%s] Failed to stop instance %s: %v", accountLabel, inst.InstanceID, err)
			continue
		}

		stoppedInstances = append(stoppedInstances, fmt.Sprintf("%s (%s) - %s",
			inst.InstanceName, inst.InstanceID, aliyun.GetRegionDisplayName(inst.RegionID)))
	}

	// Send notification
	if m.notifier != nil && len(stoppedInstances) > 0 {
		if err := m.notifier.NotifyTrafficShutdown(accountLabel, region, trafficGB, limitGB, stoppedInstances); err != nil {
			log.Errorf("[%s] Failed to send traffic shutdown notification: %v", accountLabel, err)
		}
	}
}

// sendCBWPInstanceList sends the instance list with inline keyboard for CBWP management
func (m *Monitor) sendCBWPInstanceList() error {
	if m.botHandler == nil {
		return fmt.Errorf("bot handler not initialized")
	}

	m.mu.RLock()
	instances := make([]*aliyun.SpotInstance, len(m.instances))
	copy(instances, m.instances)
	m.mu.RUnlock()

	if len(instances) == 0 {
		return m.notifier.Send("🌐 <b>共享带宽管理</b>\n\n暂无监控的实例")
	}

	var keyboard [][]notify.InlineKeyboardButton
	for _, inst := range instances {
		label := ""
		if inst.AccountLabel != "" {
			label = fmt.Sprintf("[%s] ", inst.AccountLabel)
		}
		keyboard = append(keyboard, []notify.InlineKeyboardButton{
			{
				Text:         fmt.Sprintf("%s%s (%s)", label, inst.InstanceName, inst.RegionID),
				CallbackData: fmt.Sprintf("cbwp|select|%s|%s", inst.InstanceID, inst.AccountLabel),
			},
		})
	}

	text := "🌐 <b>共享带宽管理</b>\n━━━━━━━━━━━━━━━━\n\n请选择要操作的实例："
	return m.botHandler.SendMessageWithKeyboard(text, keyboard)
}

// handleCallbackQuery handles inline keyboard callback queries
func (m *Monitor) handleCallbackQuery(callbackID, data string, messageID int64) error {
	parts := strings.Split(data, "|")
	if len(parts) < 2 || parts[0] != "cbwp" {
		return nil
	}

	action := parts[1]

	switch action {
	case "select":
		if len(parts) < 4 {
			return nil
		}
		instanceID := parts[2]
		accountLabel := parts[3]
		return m.handleCBWPSelectInstance(callbackID, instanceID, accountLabel, messageID)

	case "bind":
		if len(parts) < 5 {
			return nil
		}
		instanceID := parts[2]
		accountLabel := parts[3]
		bwpID := parts[4]
		return m.handleCBWPBind(callbackID, instanceID, accountLabel, bwpID, messageID)

	case "unbind":
		if len(parts) < 5 {
			return nil
		}
		instanceID := parts[2]
		accountLabel := parts[3]
		bwpID := parts[4]
		return m.handleCBWPUnbind(callbackID, instanceID, accountLabel, bwpID, messageID)

	case "back":
		_ = m.botHandler.AnswerCallbackQuery(callbackID, "", false)
		return m.handleCBWPBackToList(messageID)

	default:
		return nil
	}
}

// handleCBWPSelectInstance handles instance selection for CBWP management
func (m *Monitor) handleCBWPSelectInstance(callbackID, instanceID, accountLabel string, messageID int64) error {
	_ = m.botHandler.AnswerCallbackQuery(callbackID, "查询中...", false)

	cbwpClient := m.getCBWPClientByLabel(accountLabel)
	if cbwpClient == nil {
		return m.botHandler.EditMessageText(messageID, "❌ 未找到该账号的客户端", nil)
	}

	// Find the instance
	m.mu.RLock()
	var inst *aliyun.SpotInstance
	for _, i := range m.instances {
		if i.InstanceID == instanceID && i.AccountLabel == accountLabel {
			inst = i
			break
		}
	}
	m.mu.RUnlock()

	if inst == nil {
		return m.botHandler.EditMessageText(messageID, "❌ 未找到该实例", nil)
	}

	// Query EIPs for this instance
	eips, err := cbwpClient.DescribeEipAddresses(inst.RegionID, instanceID)
	if err != nil {
		log.Errorf("[%s] Failed to query EIPs for instance %s: %v", accountLabel, instanceID, err)
		return m.botHandler.EditMessageText(messageID, fmt.Sprintf("❌ 查询 EIP 失败: %v", err), nil)
	}

	if len(eips) == 0 {
		keyboard := [][]notify.InlineKeyboardButton{
			{{Text: "« 返回", CallbackData: "cbwp|back"}},
		}
		return m.botHandler.EditMessageText(messageID,
			fmt.Sprintf("🌐 <b>%s</b>\n\n该实例没有绑定 EIP，无法操作共享带宽包", inst.InstanceName),
			keyboard)
	}

	// Query bandwidth packages in the same region
	bwps, err := cbwpClient.DescribeCommonBandwidthPackages(inst.RegionID)
	if err != nil {
		log.Errorf("[%s] Failed to query bandwidth packages in region %s: %v", accountLabel, inst.RegionID, err)
		return m.botHandler.EditMessageText(messageID, fmt.Sprintf("❌ 查询共享带宽包失败: %v", err), nil)
	}

	if len(bwps) == 0 {
		keyboard := [][]notify.InlineKeyboardButton{
			{{Text: "« 返回", CallbackData: "cbwp|back"}},
		}
		return m.botHandler.EditMessageText(messageID,
			fmt.Sprintf("🌐 <b>%s</b>\n\n该地域 (%s) 没有共享带宽包", inst.InstanceName, inst.RegionID),
			keyboard)
	}

	// Build status text and action buttons
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🌐 <b>%s</b>\n", inst.InstanceName))
	if accountLabel != "" {
		sb.WriteString(fmt.Sprintf("   账号: %s\n", accountLabel))
	}
	sb.WriteString(fmt.Sprintf("   区域: %s\n", inst.RegionID))
	sb.WriteString("━━━━━━━━━━━━━━━━\n\n")

	var keyboard [][]notify.InlineKeyboardButton

	for _, eip := range eips {
		sb.WriteString(fmt.Sprintf("📍 EIP: <code>%s</code>\n", eip.IPAddress))

		if eip.BandwidthPackageID != "" {
			// EIP is in a bandwidth package - show unbind option
			bwpName := eip.BandwidthPackageID
			for _, bwp := range bwps {
				if bwp.BandwidthPackageID == eip.BandwidthPackageID {
					if bwp.Name != "" {
						bwpName = bwp.Name
					}
					sb.WriteString(fmt.Sprintf("   📦 当前带宽包: %s (%sMbps)\n", bwpName, bwp.Bandwidth))
					break
				}
			}
			sb.WriteString("   状态: ✅ 已加入共享带宽\n\n")

			keyboard = append(keyboard, []notify.InlineKeyboardButton{
				{
					Text:         fmt.Sprintf("🔴 移出 %s", eip.IPAddress),
					CallbackData: fmt.Sprintf("cbwp|unbind|%s|%s|%s", instanceID, accountLabel, eip.BandwidthPackageID),
				},
			})
		} else {
			// EIP is not in any bandwidth package - show bind options
			sb.WriteString("   状态: ⚪ 未加入共享带宽\n\n")

			for _, bwp := range bwps {
				bwpLabel := bwp.BandwidthPackageID
				if bwp.Name != "" {
					bwpLabel = bwp.Name
				}
				keyboard = append(keyboard, []notify.InlineKeyboardButton{
					{
						Text:         fmt.Sprintf("🟢 加入 %s (%sMbps)", bwpLabel, bwp.Bandwidth),
						CallbackData: fmt.Sprintf("cbwp|bind|%s|%s|%s", instanceID, accountLabel, bwp.BandwidthPackageID),
					},
				})
			}
		}
	}

	keyboard = append(keyboard, []notify.InlineKeyboardButton{
		{Text: "« 返回", CallbackData: "cbwp|back"},
	})

	return m.botHandler.EditMessageText(messageID, sb.String(), keyboard)
}

// handleCBWPBind handles binding an EIP to a bandwidth package
func (m *Monitor) handleCBWPBind(callbackID, instanceID, accountLabel, bwpID string, messageID int64) error {
	_ = m.botHandler.AnswerCallbackQuery(callbackID, "正在加入共享带宽包...", false)

	cbwpClient := m.getCBWPClientByLabel(accountLabel)
	if cbwpClient == nil {
		return m.botHandler.EditMessageText(messageID, "❌ 未找到该账号的客户端", nil)
	}

	// Find the instance
	m.mu.RLock()
	var inst *aliyun.SpotInstance
	for _, i := range m.instances {
		if i.InstanceID == instanceID && i.AccountLabel == accountLabel {
			inst = i
			break
		}
	}
	m.mu.RUnlock()

	if inst == nil {
		return m.botHandler.EditMessageText(messageID, "❌ 未找到该实例", nil)
	}

	// Get EIPs
	eips, err := cbwpClient.DescribeEipAddresses(inst.RegionID, instanceID)
	if err != nil || len(eips) == 0 {
		return m.botHandler.EditMessageText(messageID, "❌ 查询 EIP 失败", nil)
	}

	// Find the first unbound EIP
	var targetEIP *aliyun.EIPInfo
	for _, eip := range eips {
		if eip.BandwidthPackageID == "" {
			targetEIP = eip
			break
		}
	}

	if targetEIP == nil {
		return m.botHandler.EditMessageText(messageID, "❌ 没有可用的 EIP（所有 EIP 已在带宽包中）", nil)
	}

	// Execute bind
	if err := cbwpClient.AddCommonBandwidthPackageIp(inst.RegionID, bwpID, targetEIP.AllocationID); err != nil {
		log.Errorf("[%s] Failed to bind EIP %s to CBWP %s: %v", accountLabel, targetEIP.AllocationID, bwpID, err)
		keyboard := [][]notify.InlineKeyboardButton{
			{{Text: "« 返回", CallbackData: "cbwp|back"}},
		}
		return m.botHandler.EditMessageText(messageID,
			fmt.Sprintf("❌ <b>加入失败</b>\n\nEIP: %s\n错误: %v", targetEIP.IPAddress, err),
			keyboard)
	}

	keyboard := [][]notify.InlineKeyboardButton{
		{{Text: "« 返回实例列表", CallbackData: "cbwp|back"}},
	}
	return m.botHandler.EditMessageText(messageID,
		fmt.Sprintf("✅ <b>已加入共享带宽</b>\n━━━━━━━━━━━━━━━━\n实例: %s\nEIP: <code>%s</code>\n带宽包: <code>%s</code>\n时间: %s",
			inst.InstanceName, targetEIP.IPAddress, bwpID, time.Now().Format("2006-01-02 15:04:05")),
		keyboard)
}

// handleCBWPUnbind handles removing an EIP from a bandwidth package
func (m *Monitor) handleCBWPUnbind(callbackID, instanceID, accountLabel, bwpID string, messageID int64) error {
	_ = m.botHandler.AnswerCallbackQuery(callbackID, "正在移出共享带宽包...", false)

	cbwpClient := m.getCBWPClientByLabel(accountLabel)
	if cbwpClient == nil {
		return m.botHandler.EditMessageText(messageID, "❌ 未找到该账号的客户端", nil)
	}

	// Find the instance
	m.mu.RLock()
	var inst *aliyun.SpotInstance
	for _, i := range m.instances {
		if i.InstanceID == instanceID && i.AccountLabel == accountLabel {
			inst = i
			break
		}
	}
	m.mu.RUnlock()

	if inst == nil {
		return m.botHandler.EditMessageText(messageID, "❌ 未找到该实例", nil)
	}

	// Get EIPs
	eips, err := cbwpClient.DescribeEipAddresses(inst.RegionID, instanceID)
	if err != nil || len(eips) == 0 {
		return m.botHandler.EditMessageText(messageID, "❌ 查询 EIP 失败", nil)
	}

	// Find the EIP in this bandwidth package
	var targetEIP *aliyun.EIPInfo
	for _, eip := range eips {
		if eip.BandwidthPackageID == bwpID {
			targetEIP = eip
			break
		}
	}

	if targetEIP == nil {
		return m.botHandler.EditMessageText(messageID, "❌ 未找到在该带宽包中的 EIP", nil)
	}

	// Execute unbind
	if err := cbwpClient.RemoveCommonBandwidthPackageIp(inst.RegionID, bwpID, targetEIP.AllocationID); err != nil {
		log.Errorf("[%s] Failed to unbind EIP %s from CBWP %s: %v", accountLabel, targetEIP.AllocationID, bwpID, err)
		keyboard := [][]notify.InlineKeyboardButton{
			{{Text: "« 返回", CallbackData: "cbwp|back"}},
		}
		return m.botHandler.EditMessageText(messageID,
			fmt.Sprintf("❌ <b>移出失败</b>\n\nEIP: %s\n错误: %v", targetEIP.IPAddress, err),
			keyboard)
	}

	keyboard := [][]notify.InlineKeyboardButton{
		{{Text: "« 返回实例列表", CallbackData: "cbwp|back"}},
	}
	return m.botHandler.EditMessageText(messageID,
		fmt.Sprintf("✅ <b>已移出共享带宽</b>\n━━━━━━━━━━━━━━━━\n实例: %s\nEIP: <code>%s</code>\n带宽包: <code>%s</code>\n时间: %s",
			inst.InstanceName, targetEIP.IPAddress, bwpID, time.Now().Format("2006-01-02 15:04:05")),
		keyboard)
}

// handleCBWPBackToList handles going back to the instance list
func (m *Monitor) handleCBWPBackToList(messageID int64) error {
	m.mu.RLock()
	instances := make([]*aliyun.SpotInstance, len(m.instances))
	copy(instances, m.instances)
	m.mu.RUnlock()

	if len(instances) == 0 {
		return m.botHandler.EditMessageText(messageID, "🌐 <b>共享带宽管理</b>\n\n暂无监控的实例", nil)
	}

	var keyboard [][]notify.InlineKeyboardButton
	for _, inst := range instances {
		label := ""
		if inst.AccountLabel != "" {
			label = fmt.Sprintf("[%s] ", inst.AccountLabel)
		}
		keyboard = append(keyboard, []notify.InlineKeyboardButton{
			{
				Text:         fmt.Sprintf("%s%s (%s)", label, inst.InstanceName, inst.RegionID),
				CallbackData: fmt.Sprintf("cbwp|select|%s|%s", inst.InstanceID, inst.AccountLabel),
			},
		})
	}

	text := "🌐 <b>共享带宽管理</b>\n━━━━━━━━━━━━━━━━\n\n请选择要操作的实例："
	return m.botHandler.EditMessageText(messageID, text, keyboard)
}
