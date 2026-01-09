package aliyun

import (
	"fmt"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/bssopenapi"
	log "github.com/sirupsen/logrus"
)

// BillingItem represents a billing item for an instance
type BillingItem struct {
	InstanceID      string  // 实例ID
	InstanceName    string  // 实例名称 (ProductDetail)
	Region          string  // 区域
	ProductCode     string  // 产品代码 (ecs)
	ProductDetail   string  // 产品明细
	BillingItemName string  // 计费项名称 (实例规格、系统盘、数据盘、公网带宽等)
	InstanceSpec    string  // 实例规格 (ecs.t6-c4m1.large)
	PretaxAmount    float64 // 应付金额
	Currency        string  // 货币单位
}

// InstanceBillingSummary represents billing summary for a single instance
type InstanceBillingSummary struct {
	InstanceID   string
	InstanceName string
	Region       string
	InstanceSpec string // 实例规格
	Items        []BillingItem
	TotalAmount  float64
}

// BillingSummary represents the billing summary (can be for hours or daily)
type BillingSummary struct {
	StartTime       time.Time
	EndTime         time.Time
	Hours           int     // 查询的小时数
	Instances       []InstanceBillingSummary
	TotalAmount     float64
	MonthlyEstimate float64 // 月度估算
}

// BillingClient wraps the Aliyun BSS client
type BillingClient struct {
	client *bssopenapi.Client
}

// NewBillingClient creates a new BSS client
func NewBillingClient(accessKeyID, accessKeySecret string) (*BillingClient, error) {
	// BSS API uses cn-hangzhou as the default region
	client, err := bssopenapi.NewClientWithAccessKey("cn-hangzhou", accessKeyID, accessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("failed to create BSS client: %w", err)
	}

	return &BillingClient{
		client: client,
	}, nil
}

// InstanceInfo contains basic instance information for billing display
type InstanceInfo struct {
	InstanceID   string
	InstanceName string
	RegionID     string
}

// QueryBillingByHours queries billing for the specified instances within the last N hours
func (c *BillingClient) QueryBillingByHours(instances []InstanceInfo, hours int) (*BillingSummary, error) {
	now := time.Now()
	startTime := now.Add(-time.Duration(hours) * time.Hour)

	log.Debugf("Querying billing for %d instances, last %d hours (from %s to %s)",
		len(instances), hours, startTime.Format("2006-01-02 15:04"), now.Format("2006-01-02 15:04"))

	// Create instance ID to info map for quick lookup
	instanceMap := make(map[string]InstanceInfo)
	for _, inst := range instances {
		instanceMap[inst.InstanceID] = inst
	}

	// We may need to query multiple billing cycles if the time range spans months
	billingCycles := getBillingCycles(startTime, now)

	// Group billing items by instance
	instanceBillings := make(map[string]*InstanceBillingSummary)

	for _, cycle := range billingCycles {
		log.Debugf("Querying billing cycle: %s", cycle)

		// Query instance bill
		request := bssopenapi.CreateQueryInstanceBillRequest()
		request.Scheme = "https"
		request.BillingCycle = cycle
		request.ProductCode = "ecs"
		request.IsBillingItem = requests.NewBoolean(true)
		request.PageSize = requests.NewInteger(300)
		request.PageNum = requests.NewInteger(1)

		response, err := c.client.QueryInstanceBill(request)
		if err != nil {
			return nil, fmt.Errorf("failed to query instance bill for cycle %s: %w", cycle, err)
		}

		log.Debugf("Got %d billing items from API for cycle %s", len(response.Data.Items.Item), cycle)

		for _, item := range response.Data.Items.Item {
			// Skip if not in our instance list
			instInfo, exists := instanceMap[item.InstanceID]
			if !exists {
				continue
			}

			// Check if billing is within our time range
			// ServicePeriod format: "yyyyMMddHHmmss-yyyyMMddHHmmss" or BillingDate: "2006-01-02"
			if !isBillingInTimeRange(item.ServicePeriod, item.BillingDate, startTime, now) {
				continue
			}

			// Debug log to see actual API response fields
			log.Debugf("Billing item: InstanceID=%s, InstanceSpec=%s, BillingItem=%s, ServicePeriod=%s, PretaxAmount=%.4f",
				item.InstanceID, item.InstanceSpec, item.BillingItem, item.ServicePeriod, item.PretaxAmount)

			summary, exists := instanceBillings[item.InstanceID]
			if !exists {
				summary = &InstanceBillingSummary{
					InstanceID:   item.InstanceID,
					InstanceName: instInfo.InstanceName,
					Region:       instInfo.RegionID,
					InstanceSpec: item.InstanceSpec,
					Items:        []BillingItem{},
					TotalAmount:  0,
				}
				instanceBillings[item.InstanceID] = summary
			}

			// Update InstanceSpec if not set
			if summary.InstanceSpec == "" && item.InstanceSpec != "" {
				summary.InstanceSpec = item.InstanceSpec
			}

			// Format billing item name with InstanceSpec for compute resources
			billingItemName := formatBillingItemName(item.BillingItem, item.InstanceSpec)

			billingItem := BillingItem{
				InstanceID:      item.InstanceID,
				InstanceName:    instInfo.InstanceName,
				Region:          instInfo.RegionID,
				ProductCode:     item.ProductCode,
				ProductDetail:   item.ProductDetail,
				BillingItemName: billingItemName,
				InstanceSpec:    item.InstanceSpec,
				PretaxAmount:    item.PretaxAmount,
				Currency:        item.Currency,
			}

			summary.Items = append(summary.Items, billingItem)
			summary.TotalAmount += item.PretaxAmount
		}
	}

	// Build final summary
	result := &BillingSummary{
		StartTime:   startTime,
		EndTime:     now,
		Hours:       hours,
		Instances:   make([]InstanceBillingSummary, 0, len(instanceBillings)),
		TotalAmount: 0,
	}

	for _, summary := range instanceBillings {
		result.Instances = append(result.Instances, *summary)
		result.TotalAmount += summary.TotalAmount
	}

	// Calculate monthly estimate based on hourly rate
	if hours > 0 && result.TotalAmount > 0 {
		hourlyRate := result.TotalAmount / float64(hours)
		result.MonthlyEstimate = hourlyRate * 24 * 30 // 30 days per month
	}

	log.Infof("Found billing for %d instances in last %d hours, total: %.4f, monthly estimate: %.2f",
		len(result.Instances), hours, result.TotalAmount, result.MonthlyEstimate)

	return result, nil
}

// getBillingCycles returns the billing cycles (YYYY-MM) that cover the time range
func getBillingCycles(start, end time.Time) []string {
	cycles := make([]string, 0)
	current := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, start.Location())

	for !current.After(end) {
		cycles = append(cycles, current.Format("2006-01"))
		current = current.AddDate(0, 1, 0)
	}

	return cycles
}

// isBillingInTimeRange checks if a billing item falls within the specified time range
func isBillingInTimeRange(servicePeriod, billingDate string, start, end time.Time) bool {
	// Try to parse ServicePeriod first (format: "yyyyMMddHHmmss-yyyyMMddHHmmss")
	if servicePeriod != "" && len(servicePeriod) >= 14 {
		// Extract start time from ServicePeriod
		periodStart := servicePeriod[:14]
		t, err := time.ParseInLocation("20060102150405", periodStart, start.Location())
		if err == nil {
			return !t.Before(start) && !t.After(end)
		}
	}

	// Fall back to BillingDate (format: "2006-01-02")
	if billingDate != "" {
		t, err := time.ParseInLocation("2006-01-02", billingDate, start.Location())
		if err == nil {
			// For daily billing, check if the date is within range
			dayStart := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
			dayEnd := dayStart.Add(24 * time.Hour)
			return !dayEnd.Before(start) && !dayStart.After(end)
		}
	}

	// If we can't parse the time, include the item (conservative approach)
	return true
}

// formatBillingItemName formats the billing item name for display
// For compute resources, it includes the instance spec (SKU)
func formatBillingItemName(billingItem, instanceSpec string) string {
	// Map common billing item names to friendly display names
	switch billingItem {
	case "系统盘":
		return "系统盘"
	case "数据盘":
		return "数据盘"
	case "云服务器配置":
		// For compute resources, show the specific SKU
		if instanceSpec != "" {
			return fmt.Sprintf("计算 (%s)", instanceSpec)
		}
		return "计算资源"
	case "ImageOS":
		return "镜像费用"
	case "公网带宽":
		return "公网带宽"
	case "流量":
		return "公网流量"
	case "快照":
		return "快照"
	case "实例":
		if instanceSpec != "" {
			return fmt.Sprintf("实例 (%s)", instanceSpec)
		}
		return "实例"
	default:
		if billingItem != "" {
			return billingItem
		}
		return "其他费用"
	}
}