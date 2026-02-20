package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/iliyian/aliyun-spot-manager/internal/aliyun"
	"github.com/iliyian/aliyun-spot-manager/internal/gcp"
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

// NotifyBillingSummary sends a billing summary notification with monthly data and estimate
func (t *TelegramNotifier) NotifyBillingSummary(summary *aliyun.BillingSummary) error {
	if summary == nil || len(summary.Instances) == 0 {
		message := fmt.Sprintf(`📊 <b>扣费汇总</b> (%s)
━━━━━━━━━━━━━━━━━━━━━━━━

暂无扣费记录

━━━━━━━━━━━━━━━━━━━━━━━━
💰 本月累计: ¥0.00
📈 月度估算: ¥0.00`, summary.BillingCycle)
		return t.Send(message)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📊 <b>扣费汇总</b> (%s)\n", summary.BillingCycle))
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━\n")

	// Statistics section
	sb.WriteString(fmt.Sprintf("📅 统计区间: %s 01日 ~ %s\n",
		summary.BillingCycle,
		summary.EndTime.Format("02日 15:04")))
	sb.WriteString(fmt.Sprintf("⏱ 已过天数: %d 天\n", summary.ElapsedDays))
	sb.WriteString(fmt.Sprintf("🕐 总运行时长: %.1f 小时\n", summary.TotalRunningHours))
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

	for _, inst := range summary.Instances {
		// Instance header with spec
		if inst.InstanceSpec != "" {
			sb.WriteString(fmt.Sprintf("🖥 <b>%s</b> [%s]\n", inst.InstanceName, inst.InstanceSpec))
		} else {
			sb.WriteString(fmt.Sprintf("🖥 <b>%s</b>\n", inst.InstanceName))
		}
		sb.WriteString(fmt.Sprintf("   <code>%s</code> | %s\n", inst.InstanceID, inst.Region))

		// Billing items
		for i, item := range inst.Items {
			prefix := "├─"
			if i == len(inst.Items)-1 {
				prefix = "└─"
			}
			sb.WriteString(fmt.Sprintf("   %s %s: ¥%.4f\n", prefix, item.BillingItemName, item.PretaxAmount))
		}

		// Instance subtotal with hourly cost
		if inst.RunningHours > 0 && inst.HourlyCost > 0 {
			sb.WriteString(fmt.Sprintf("   <b>小计: ¥%.4f</b> (%.1fh, ¥%.4f/h)\n\n", inst.TotalAmount, inst.RunningHours, inst.HourlyCost))
		} else {
			sb.WriteString(fmt.Sprintf("   <b>小计: ¥%.4f</b>\n\n", inst.TotalAmount))
		}
	}

	sb.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━\n")
	sb.WriteString(fmt.Sprintf("💰 <b>本月累计: ¥%.4f</b>\n", summary.TotalAmount))
	sb.WriteString(fmt.Sprintf("📈 <b>月度估算: ¥%.2f</b>\n", summary.MonthlyEstimate))

	// Show calculation method
	if summary.EstimateMethod != "" {
		sb.WriteString(fmt.Sprintf("📝 <i>%s</i>", summary.EstimateMethod))
	}

	return t.Send(sb.String())
}

// NotifyTrafficSummary sends a traffic summary notification
func (t *TelegramNotifier) NotifyTrafficSummary(summary *aliyun.TrafficSummary) error {
	if summary == nil {
		message := `📶 <b>流量统计</b>
━━━━━━━━

暂无流量数据

━━━━━━━━━━━━━━━━`
		return t.Send(message)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📶 <b>流量统计</b> (%s)\n", summary.BillingCycle))
	sb.WriteString("━━━━━━━━━━━━━━━━\n")

	// Statistics section
	sb.WriteString(fmt.Sprintf("📅 统计区间: %s 01日 ~ %s\n",
		summary.BillingCycle,
		summary.EndTime.Format("02日 15:04")))
	sb.WriteString("━━━━━━━━━━━━━━━━\n\n")

	// China Mainland section
	sb.WriteString("🇨🇳 <b>中国大陆</b>\n")
	if summary.ChinaMainland.Traffic > 0 {
		sb.WriteString(fmt.Sprintf("   📊 总流量: <b>%s</b>\n", aliyun.FormatTrafficSize(summary.ChinaMainland.Traffic)))
		sb.WriteString(fmt.Sprintf("   🌐 区域数: %d\n", summary.ChinaMainland.RegionCount))
		// Product details
		if len(summary.ChinaMainland.ProductDetails) > 0 {
			sb.WriteString("   📦 产品明细:\n")
			for product, traffic := range summary.ChinaMainland.ProductDetails {
				if traffic > 0 {
					sb.WriteString(fmt.Sprintf("      • %s: %s\n", product, aliyun.FormatTrafficSize(traffic)))
				}
			}
		}
		// Region list
		if len(summary.ChinaMainland.Regions) > 0 {
			sb.WriteString("   📍 区域列表:\n")
			for _, region := range summary.ChinaMainland.Regions {
				regionName := aliyun.GetRegionDisplayName(region)
				sb.WriteString(fmt.Sprintf("      • %s\n", regionName))
			}
		}
	} else {
		sb.WriteString("   暂无流量\n")
	}
	sb.WriteString("\n")

	// Non-China Mainland section
	sb.WriteString("🌏 <b>非中国大陆</b>\n")
	if summary.NonChinaMainland.Traffic > 0 {
		sb.WriteString(fmt.Sprintf("   📊 总流量: <b>%s</b>\n", aliyun.FormatTrafficSize(summary.NonChinaMainland.Traffic)))
		sb.WriteString(fmt.Sprintf("   🌐 区域数: %d\n", summary.NonChinaMainland.RegionCount))
		// Product details
		if len(summary.NonChinaMainland.ProductDetails) > 0 {
			sb.WriteString("   📦 产品明细:\n")
			for product, traffic := range summary.NonChinaMainland.ProductDetails {
				if traffic > 0 {
					sb.WriteString(fmt.Sprintf("      • %s: %s\n", product, aliyun.FormatTrafficSize(traffic)))
				}
			}
		}
		// Region list with traffic details
		if len(summary.RegionDetails) > 0 {
			sb.WriteString("   📍 区域明细:\n")
			for _, detail := range summary.RegionDetails {
				if !aliyun.IsChinaMainlandRegion(detail.BusinessRegionId) && detail.Traffic > 0 {
					regionName := aliyun.GetRegionDisplayName(detail.BusinessRegionId)
					sb.WriteString(fmt.Sprintf("      • %s: %s\n", regionName, aliyun.FormatTrafficSize(detail.Traffic)))
				}
			}
		}
	} else {
		sb.WriteString("   暂无流量\n")
	}
	sb.WriteString("\n")

	sb.WriteString("━━━━━━━━━━━━━━━━\n")
	sb.WriteString(fmt.Sprintf("📈 <b>本月总流量: %s</b>\n", aliyun.FormatTrafficSize(summary.TotalTraffic)))

	// Show percentage breakdown
	if summary.TotalTraffic > 0 {
		chinaPercent := float64(summary.ChinaMainland.Traffic) / float64(summary.TotalTraffic) * 100
		nonChinaPercent := float64(summary.NonChinaMainland.Traffic) / float64(summary.TotalTraffic) * 100
		sb.WriteString(fmt.Sprintf("📊 中国大陆: %.1f%% | 非中国大陆: %.1f%%", chinaPercent, nonChinaPercent))
	}

	return t.Send(sb.String())
}

// NotifyTrafficShutdown sends a notification when instances are stopped due to traffic limit
func (t *TelegramNotifier) NotifyTrafficShutdown(region string, trafficGB, limitGB float64, stoppedInstances []string) error {
	regionLabel := "🇨🇳 中国大陆"
	if region == "non-china" {
		regionLabel = "🌏 非中国大陆"
	}

	var sb strings.Builder
	sb.WriteString("🚨 <b>流量超额自动关机</b>\n")
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━\n\n")
	sb.WriteString(fmt.Sprintf("📍 区域: %s\n", regionLabel))
	sb.WriteString(fmt.Sprintf("📊 当前流量: <b>%.2f GB</b>\n", trafficGB))
	sb.WriteString(fmt.Sprintf("🚫 流量阈值: %.2f GB\n", limitGB))
	sb.WriteString(fmt.Sprintf("⏰ 时间: %s\n\n", time.Now().Format("2006-01-02 15:04:05")))

	if len(stoppedInstances) > 0 {
		sb.WriteString("🔴 <b>已关闭实例:</b>\n")
		for _, inst := range stoppedInstances {
			sb.WriteString(fmt.Sprintf("   • %s\n", inst))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━\n")
	sb.WriteString("💡 <i>使用节省停机模式，不再计费 vCPU/内存</i>\n")
	sb.WriteString("⚠️ <i>自动重启已暂停，新月流量重置后恢复</i>")

	return t.Send(sb.String())
}

// NotifyTrafficSummaryWithLimits sends a traffic summary with threshold info
func (t *TelegramNotifier) NotifyTrafficSummaryWithLimits(summary *aliyun.TrafficSummary, chinaLimitGB, nonChinaLimitGB float64, chinaShutdown, nonChinaShutdown bool) error {
	if summary == nil {
		message := `📶 <b>流量统计</b>
━━━━━━━━

暂无流量数据

━━━━━━━━━━━━━━━━`
		return t.Send(message)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📶 <b>流量统计</b> (%s)\n", summary.BillingCycle))
	sb.WriteString("━━━━━━━━━━━━━━━━\n")

	// Statistics section
	sb.WriteString(fmt.Sprintf("📅 统计区间: %s 01日 ~ %s\n",
		summary.BillingCycle,
		summary.EndTime.Format("02日 15:04")))
	sb.WriteString("━━━━━━━━━━━━━━━━\n\n")

	// China Mainland section
	sb.WriteString("🇨🇳 <b>中国大陆</b>\n")
	if summary.ChinaMainland.Traffic > 0 {
		sb.WriteString(fmt.Sprintf("   📊 总流量: <b>%s</b> / %.0f GB\n", aliyun.FormatTrafficSize(summary.ChinaMainland.Traffic), chinaLimitGB))
		remainChina := chinaLimitGB - summary.ChinaMainland.TrafficGB
		if remainChina < 0 {
			remainChina = 0
		}
		sb.WriteString(fmt.Sprintf("   📉 剩余额度: %.2f GB\n", remainChina))
		if chinaShutdown {
			sb.WriteString("   🔴 <b>已超额关机</b>\n")
		}
		sb.WriteString(fmt.Sprintf("   🌐 区域数: %d\n", summary.ChinaMainland.RegionCount))
		if len(summary.ChinaMainland.ProductDetails) > 0 {
			sb.WriteString("   📦 产品明细:\n")
			for product, traffic := range summary.ChinaMainland.ProductDetails {
				if traffic > 0 {
					sb.WriteString(fmt.Sprintf("      • %s: %s\n", product, aliyun.FormatTrafficSize(traffic)))
				}
			}
		}
	} else {
		sb.WriteString(fmt.Sprintf("   暂无流量 (阈值: %.0f GB)\n", chinaLimitGB))
	}
	sb.WriteString("\n")

	// Non-China Mainland section
	sb.WriteString("🌏 <b>非中国大陆</b>\n")
	if summary.NonChinaMainland.Traffic > 0 {
		sb.WriteString(fmt.Sprintf("   📊 总流量: <b>%s</b> / %.0f GB\n", aliyun.FormatTrafficSize(summary.NonChinaMainland.Traffic), nonChinaLimitGB))
		remainNonChina := nonChinaLimitGB - summary.NonChinaMainland.TrafficGB
		if remainNonChina < 0 {
			remainNonChina = 0
		}
		sb.WriteString(fmt.Sprintf("   📉 剩余额度: %.2f GB\n", remainNonChina))
		if nonChinaShutdown {
			sb.WriteString("   🔴 <b>已超额关机</b>\n")
		}
		sb.WriteString(fmt.Sprintf("   🌐 区域数: %d\n", summary.NonChinaMainland.RegionCount))
		if len(summary.NonChinaMainland.ProductDetails) > 0 {
			sb.WriteString("   📦 产品明细:\n")
			for product, traffic := range summary.NonChinaMainland.ProductDetails {
				if traffic > 0 {
					sb.WriteString(fmt.Sprintf("      • %s: %s\n", product, aliyun.FormatTrafficSize(traffic)))
				}
			}
		}
		if len(summary.RegionDetails) > 0 {
			sb.WriteString("   📍 区域明细:\n")
			for _, detail := range summary.RegionDetails {
				if !aliyun.IsChinaMainlandRegion(detail.BusinessRegionId) && detail.Traffic > 0 {
					regionName := aliyun.GetRegionDisplayName(detail.BusinessRegionId)
					sb.WriteString(fmt.Sprintf("      • %s: %s\n", regionName, aliyun.FormatTrafficSize(detail.Traffic)))
				}
			}
		}
	} else {
		sb.WriteString(fmt.Sprintf("   暂无流量 (阈值: %.0f GB)\n", nonChinaLimitGB))
	}
	sb.WriteString("\n")

	sb.WriteString("━━━━━━━━━━━━━━━━\n")
	sb.WriteString(fmt.Sprintf("📈 <b>本月总流量: %s</b>\n", aliyun.FormatTrafficSize(summary.TotalTraffic)))

	if summary.TotalTraffic > 0 {
		chinaPercent := float64(summary.ChinaMainland.Traffic) / float64(summary.TotalTraffic) * 100
		nonChinaPercent := float64(summary.NonChinaMainland.Traffic) / float64(summary.TotalTraffic) * 100
		sb.WriteString(fmt.Sprintf("📊 中国大陆: %.1f%% | 非中国大陆: %.1f%%", chinaPercent, nonChinaPercent))
	}

	return t.Send(sb.String())
}

// NotifyGCPCreditsSummary sends a GCP credits status report
func (t *TelegramNotifier) NotifyGCPCreditsSummary(summary *gcp.CreditsSummary) error {
	msg := fmt.Sprintf(`☁️ <b>GCP Credits 状态</b>
━━━━━━━━━━━━━━━━
💳 总额度: $%.2f
💸 已使用: $%.2f
💰 剩余: $%.2f (%.1f%%)
📅 查询时间: %s
━━━━━━━━━━━━━━━━`,
		summary.TotalCredits, summary.UsedAmount,
		summary.RemainingAmount, summary.RemainingPct,
		summary.QueryTime.Format("2006-01-02 15:04"))
	return t.Send(msg)
}

// NotifyGCPCreditsLow sends a GCP credits low alert
func (t *TelegramNotifier) NotifyGCPCreditsLow(summary *gcp.CreditsSummary, threshold float64) error {
	msg := fmt.Sprintf(`⚠️ <b>GCP Credits 即将用尽</b>
━━━━━━━━━━━━━━━━
💳 总额度: $%.2f
💸 已使用: $%.2f
💰 剩余: $%.2f (%.1f%%)
🚨 低于 %.0f%% 告警阈值
━━━━━━━━━━━━━━━━
请及时关注！`,
		summary.TotalCredits, summary.UsedAmount,
		summary.RemainingAmount, summary.RemainingPct, threshold)
	return t.Send(msg)
}
