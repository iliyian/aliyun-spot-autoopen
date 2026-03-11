package aliyun

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	log "github.com/sirupsen/logrus"
)

// TrafficClient wraps the Aliyun CDT client for traffic queries
type TrafficClient struct {
	client *sdk.Client
}

// NewTrafficClient creates a new CDT traffic client
func NewTrafficClient(accessKeyID, accessKeySecret string) (*TrafficClient, error) {
	// CDT API uses cn-hangzhou as the default region
	client, err := sdk.NewClientWithAccessKey("cn-hangzhou", accessKeyID, accessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("failed to create CDT client: %w", err)
	}

	return &TrafficClient{
		client: client,
	}, nil
}

// ProductTrafficDetail represents traffic detail for a specific product
type ProductTrafficDetail struct {
	Product string `json:"Product"` // eip, ipv6bandwidth, cbwp, etc.
	Traffic int64  `json:"Traffic"` // Traffic in bytes
}

// TrafficTierDetail represents traffic tier information
type TrafficTierDetail struct {
	Tier           int   `json:"Tier"`
	Traffic        int64 `json:"Traffic"`
	LowestTraffic  int64 `json:"LowestTraffic"`
	HighestTraffic int64 `json:"HighestTraffic"`
}

// RegionTrafficDetail represents traffic detail for a specific region
type RegionTrafficDetail struct {
	BusinessRegionId      string                 `json:"BusinessRegionId"`
	ISPType               string`json:"ISPType"`
	Traffic               int64                  `json:"Traffic"`
	ProductTrafficDetails []ProductTrafficDetail `json:"ProductTrafficDetails"`
	TrafficTierDetails    []TrafficTierDetail    `json:"TrafficTierDetails"`
}

// TrafficSummary represents the traffic summary
type TrafficSummary struct {
	StartTime          time.Time
	EndTime            time.Time
	BillingCycle       string // YYYY-MM
	AccountLabel       string // 账号标签
	ChinaMainland      TrafficRegionSummary
	NonChinaMainland   TrafficRegionSummary
	TotalTraffic       int64
	TotalTrafficGB     float64
	RegionDetails      []RegionTrafficDetail
}

// TrafficRegionSummary represents traffic summary for a region group
type TrafficRegionSummary struct {
	Traffic        int64
	TrafficGB      float64
	Regions        []string
	RegionCount    int
	ProductDetails map[string]int64 // product -> traffic in bytes
}

// CDT API response structure
type cdtInternetTrafficResponse struct {
	RequestId      string                `json:"RequestId"`
	TrafficDetails []RegionTrafficDetail `json:"TrafficDetails"`
}

// ChinaMainlandRegions defines regions that are considered China Mainland
var ChinaMainlandRegions = map[string]bool{
	"cn-qingdao":            true,
	"cn-beijing":            true,
	"cn-zhangjiakou":        true,
	"cn-huhehaote":          true,
	"cn-wulanchabu":         true,
	"cn-hangzhou":           true,
	"cn-shanghai":           true,
	"cn-nanjing":            true,
	"cn-fuzhou":             true,
	"cn-shenzhen":           true,
	"cn-heyuan":             true,
	"cn-guangzhou":          true,
	"cn-chengdu":            true,
	"cn-nanjing-finance":    true,
	"cn-shanghai-finance-1": true,
	"cn-shenzhen-finance-1": true,
}

// IsChinaMainlandRegion checks if a region is in China Mainland
func IsChinaMainlandRegion(regionId string) bool {
	// Check exact match first
	if ChinaMainlandRegions[regionId] {
		return true
	}
	// Check prefix for any cn- region (except cn-hongkong)
	if strings.HasPrefix(regionId, "cn-") && regionId != "cn-hongkong" {
		return true
	}
	return false
}

// QueryInternetTraffic queries internet traffic for the current month
func (c *TrafficClient) QueryInternetTraffic(accountLabel string) (*TrafficSummary, error) {
	now := time.Now()
	startTime := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	endTime := now

	return c.QueryInternetTrafficByTimeRange(startTime, endTime, accountLabel)
}

// QueryInternetTrafficByTimeRange queries internet traffic for a specific time range
func (c *TrafficClient) QueryInternetTrafficByTimeRange(startTime, endTime time.Time, accountLabel string) (*TrafficSummary, error) {
	request := requests.NewCommonRequest()
	request.Method = "POST"
	request.Scheme = "https"
	request.Domain = "cdt.aliyuncs.com"
	request.Version = "2021-08-13"
	request.ApiName = "ListCdtInternetTraffic"

	request.QueryParams["StartTime"] = startTime.Format("2006-01-02T15:04:05Z")
	request.QueryParams["EndTime"] = endTime.Format("2006-01-02T15:04:05Z")

	log.Debugf("[%s] Querying CDT traffic from %s to %s", accountLabel, startTime.Format("2006-01-02"), endTime.Format("2006-01-02"))

	response, err := c.client.ProcessCommonRequest(request)
	if err != nil {
		return nil, fmt.Errorf("failed to query CDT traffic: %w", err)
	}

	if response.GetHttpStatus() != 200 {
		return nil, fmt.Errorf("CDT API returned status %d: %s", response.GetHttpStatus(), string(response.GetHttpContentBytes()))
	}

	var cdtResponse cdtInternetTrafficResponse
	if err := json.Unmarshal(response.GetHttpContentBytes(), &cdtResponse); err != nil {
		return nil, fmt.Errorf("failed to parse CDT response: %w", err)
	}

	// Build summary
	summary := &TrafficSummary{
		StartTime:     startTime,
		EndTime:       endTime,
		BillingCycle:  startTime.Format("2006-01"),
		AccountLabel:  accountLabel,
		RegionDetails: cdtResponse.TrafficDetails,
		ChinaMainland: TrafficRegionSummary{
			ProductDetails: make(map[string]int64),
		},
		NonChinaMainland: TrafficRegionSummary{
			ProductDetails: make(map[string]int64),
		},
	}

	// Categorize traffic by region
	for _, detail := range cdtResponse.TrafficDetails {
		summary.TotalTraffic += detail.Traffic

		if IsChinaMainlandRegion(detail.BusinessRegionId) {
			summary.ChinaMainland.Traffic += detail.Traffic
			summary.ChinaMainland.Regions = append(summary.ChinaMainland.Regions, detail.BusinessRegionId)
			summary.ChinaMainland.RegionCount++
			for _, pd := range detail.ProductTrafficDetails {
				summary.ChinaMainland.ProductDetails[pd.Product] += pd.Traffic
			}
		} else {
			summary.NonChinaMainland.Traffic += detail.Traffic
			summary.NonChinaMainland.Regions = append(summary.NonChinaMainland.Regions, detail.BusinessRegionId)
			summary.NonChinaMainland.RegionCount++
			for _, pd := range detail.ProductTrafficDetails {
				summary.NonChinaMainland.ProductDetails[pd.Product] += pd.Traffic
			}
		}
	}

	// Convert to GB
	summary.TotalTrafficGB = float64(summary.TotalTraffic) / (1024 * 1024 * 1024)
	summary.ChinaMainland.TrafficGB = float64(summary.ChinaMainland.Traffic) / (1024 * 1024 * 1024)
	summary.NonChinaMainland.TrafficGB = float64(summary.NonChinaMainland.Traffic) / (1024 * 1024 * 1024)

	log.Infof("[%s] Traffic summary: Total=%.2f GB, China Mainland=%.2f GB (%d regions), Non-China=%.2f GB (%d regions)",
		accountLabel,
		summary.TotalTrafficGB,
		summary.ChinaMainland.TrafficGB, summary.ChinaMainland.RegionCount,
		summary.NonChinaMainland.TrafficGB, summary.NonChinaMainland.RegionCount)

	return summary, nil
}

// FormatTrafficSize formats traffic size in human-readable format
func FormatTrafficSize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)

	switch {
	case bytes >= TB:
		return fmt.Sprintf("%.2f TB", float64(bytes)/TB)
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// GetRegionDisplayName returns a friendly display name for a region
func GetRegionDisplayName(regionId string) string {
	regionNames := map[string]string{
		// China Mainland
		"cn-qingdao":            "青岛",
		"cn-beijing":            "北京",
		"cn-zhangjiakou":        "张家口",
		"cn-huhehaote":          "呼和浩特",
		"cn-wulanchabu":         "乌兰察布",
		"cn-hangzhou":           "杭州",
		"cn-shanghai":           "上海",
		"cn-nanjing":            "南京",
		"cn-fuzhou":             "福州",
		"cn-shenzhen":           "深圳",
		"cn-heyuan":             "河源",
		"cn-guangzhou":          "广州",
		"cn-chengdu":            "成都",
		// Non-China Mainland
		"cn-hongkong":           "香港",
		"ap-northeast-1":        "日本(东京)",
		"ap-northeast-2":        "韩国(首尔)",
		"ap-southeast-1":        "新加坡",
		"ap-southeast-2":        "澳大利亚(悉尼)",
		"ap-southeast-3":        "马来西亚(吉隆坡)",
		"ap-southeast-5":        "印度尼西亚(雅加达)",
		"ap-southeast-6":        "菲律宾(马尼拉)",
		"ap-southeast-7":        "泰国(曼谷)",
		"ap-south-1":            "印度(孟买)",
		"us-east-1":             "美国(弗吉尼亚)",
		"us-west-1":             "美国(硅谷)",
		"eu-west-1":             "英国(伦敦)",
		"eu-central-1":          "德国(法兰克福)",
		"me-east-1":             "阿联酋(迪拜)",
	}

	if name, ok := regionNames[regionId]; ok {
		return name
	}
	return regionId
}
