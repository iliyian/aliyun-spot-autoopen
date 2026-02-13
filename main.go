package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/iliyian/aliyun-spot-manager/internal/config"
	"github.com/iliyian/aliyun-spot-manager/internal/monitor"
	"github.com/joho/godotenv"
	"github.com/robfig/cron/v3"
	log "github.com/sirupsen/logrus"
)

func main() {
	// Load .env file
	if err := godotenv.Load(); err != nil {
		log.Warn("No .env file found, using environment variables")
	}

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Setup logging
	setupLogging(cfg)

	log.Info("Starting Aliyun Spot Instance Monitor")

	// Create monitor
	mon, err := monitor.New(cfg)
	if err != nil {
		log.Fatalf("Failed to create monitor: %v", err)
	}

	// Run initial check
	log.Info("Running initial instance discovery...")
	if err := mon.DiscoverInstances(); err != nil {
		log.Fatalf("Failed to discover instances: %v", err)
	}

	// Start Telegram bot for commands
	mon.StartBot()

	// Setup cron scheduler
	c := cron.New()
	_, err = c.AddFunc(cfg.CronSchedule, func() {
		if err := mon.Check(); err != nil {
			log.Errorf("Check failed: %v", err)
		}
	})
	if err != nil {
		log.Fatalf("Failed to setup cron: %v", err)
	}

	// Setup traffic check cron if enabled
	if cfg.TrafficShutdownEnabled {
		trafficSchedule := fmt.Sprintf("@every %ds", cfg.TrafficCheckInterval)
		_, err = c.AddFunc(trafficSchedule, func() {
			if err := mon.CheckTraffic(); err != nil {
				log.Errorf("Traffic check failed: %v", err)
			}
		})
		if err != nil {
			log.Fatalf("Failed to setup traffic check cron: %v", err)
		}
		log.Infof("Traffic shutdown enabled: China limit=%.0f GB, Non-China limit=%.0f GB, check every %ds",
			cfg.TrafficLimitChinaGB, cfg.TrafficLimitNonChinaGB, cfg.TrafficCheckInterval)
	}

	c.Start()
	log.Infof("Scheduler started, checking every %d seconds", cfg.CheckInterval)

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("Shutting down...")
	c.Stop()
}

func setupLogging(cfg *config.Config) {
	// Set log level
	level, err := log.ParseLevel(cfg.LogLevel)
	if err != nil {
		level = log.InfoLevel
	}
	log.SetLevel(level)

	// Set log format
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
	})

	// Set log output
	if cfg.LogFile != "" {
		file, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			log.Warnf("Failed to open log file %s, using stdout: %v", cfg.LogFile, err)
		} else {
			log.SetOutput(file)
		}
	}
}
