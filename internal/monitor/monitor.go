package monitor

import (
	"fmt"
	"sync"
	"time"

	"github.com/iliyian/aliyun-spot-autoopen/internal/aliyun"
	"github.com/iliyian/aliyun-spot-autoopen/internal/config"
	"github.com/iliyian/aliyun-spot-autoopen/internal/notify"
	log "github.com/sirupsen/logrus"
)

// Monitor monitors spot instances and auto-starts them when stopped
type Monitor struct {
	cfg       *config.Config
	ecsClient *aliyun.ECSClient
	notifier  *notify.TelegramNotifier

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

	return m, nil
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