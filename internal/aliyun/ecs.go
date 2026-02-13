package aliyun

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	log "github.com/sirupsen/logrus"
)

// SpotInstance represents a spot instance
type SpotInstance struct {
	InstanceID       string
	InstanceName     string
	RegionID         string
	Status           string
	PublicIPAddress  string
	PrivateIPAddress string
	SpotStrategy     string
}

// ECSClient wraps the Aliyun ECS client
type ECSClient struct {
	accessKeyID     string
	accessKeySecret string
	clients         map[string]*ecs.Client // region -> client
	clientsMu       sync.RWMutex
}

// NewECSClient creates a new ECS client
func NewECSClient(accessKeyID, accessKeySecret string) *ECSClient {
	return &ECSClient{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		clients:         make(map[string]*ecs.Client),
	}
}

// getClient gets or creates an ECS client for the specified region
func (c *ECSClient) getClient(regionID string) (*ecs.Client, error) {
	// Try read lock first
	c.clientsMu.RLock()
	if client, ok := c.clients[regionID]; ok {
		c.clientsMu.RUnlock()
		return client, nil
	}
	c.clientsMu.RUnlock()

	// Need to create client, use write lock
	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()

	// Double check after acquiring write lock
	if client, ok := c.clients[regionID]; ok {
		return client, nil
	}

	client, err := ecs.NewClientWithAccessKey(regionID, c.accessKeyID, c.accessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("failed to create ECS client for region %s: %w", regionID, err)
	}

	c.clients[regionID] = client
	return client, nil
}

// GetAllRegions returns all available regions
func (c *ECSClient) GetAllRegions() ([]string, error) {
	// Use cn-hangzhou as default region to query all regions
	client, err := c.getClient("cn-hangzhou")
	if err != nil {
		return nil, err
	}

	request := ecs.CreateDescribeRegionsRequest()
	request.Scheme = "https"

	response, err := client.DescribeRegions(request)
	if err != nil {
		return nil, fmt.Errorf("failed to describe regions: %w", err)
	}

	regions := make([]string, 0, len(response.Regions.Region))
	for _, region := range response.Regions.Region {
		regions = append(regions, region.RegionId)
	}

	return regions, nil
}

// GetSpotInstances returns all spot instances in the specified region
func (c *ECSClient) GetSpotInstances(regionID string) ([]*SpotInstance, error) {
	client, err := c.getClient(regionID)
	if err != nil {
		return nil, err
	}

	var instances []*SpotInstance
	pageNumber := 1
	pageSize := 100

	for {
		request := ecs.CreateDescribeInstancesRequest()
		request.Scheme = "https"
		request.RegionId = regionID
		request.PageNumber = requests.NewInteger(pageNumber)
		request.PageSize = requests.NewInteger(pageSize)
		// Filter for pay-as-you-go instances (spot instances are a type of pay-as-you-go)
		request.InstanceChargeType = "PostPaid"

		response, err := client.DescribeInstances(request)
		if err != nil {
			return nil, fmt.Errorf("failed to describe instances in region %s: %w", regionID, err)
		}

		for _, inst := range response.Instances.Instance {
			// Filter for spot instances only
			if inst.SpotStrategy != "NoSpot" && inst.SpotStrategy != "" {
				var publicIP, privateIP string
				if len(inst.PublicIpAddress.IpAddress) > 0 {
					publicIP = inst.PublicIpAddress.IpAddress[0]
				}
				// Check EIP
				if publicIP == "" && inst.EipAddress.IpAddress != "" {
					publicIP = inst.EipAddress.IpAddress
				}
				if len(inst.InnerIpAddress.IpAddress) > 0 {
					privateIP = inst.InnerIpAddress.IpAddress[0]
				}
				if privateIP == "" && len(inst.VpcAttributes.PrivateIpAddress.IpAddress) > 0 {
					privateIP = inst.VpcAttributes.PrivateIpAddress.IpAddress[0]
				}

				instances = append(instances, &SpotInstance{
					InstanceID:       inst.InstanceId,
					InstanceName:     inst.InstanceName,
					RegionID:         regionID,
					Status:           inst.Status,
					PublicIPAddress:  publicIP,
					PrivateIPAddress: privateIP,
					SpotStrategy:     inst.SpotStrategy,
				})
			}
		}

		// Check if there are more pages
		if len(response.Instances.Instance) < pageSize {
			break
		}
		pageNumber++
	}

	return instances, nil
}

// GetInstanceStatus returns the current status of an instance
func (c *ECSClient) GetInstanceStatus(regionID, instanceID string) (string, error) {
	client, err := c.getClient(regionID)
	if err != nil {
		return "", err
	}

	request := ecs.CreateDescribeInstanceStatusRequest()
	request.Scheme = "https"
	request.RegionId = regionID
	request.InstanceId = &[]string{instanceID}

	response, err := client.DescribeInstanceStatus(request)
	if err != nil {
		return "", fmt.Errorf("failed to get instance status: %w", err)
	}

	if len(response.InstanceStatuses.InstanceStatus) == 0 {
		return "", fmt.Errorf("instance %s not found", instanceID)
	}

	return response.InstanceStatuses.InstanceStatus[0].Status, nil
}

// GetInstance returns detailed information about an instance
func (c *ECSClient) GetInstance(regionID, instanceID string) (*SpotInstance, error) {
	client, err := c.getClient(regionID)
	if err != nil {
		return nil, err
	}

	request := ecs.CreateDescribeInstancesRequest()
	request.Scheme = "https"
	request.RegionId = regionID
	request.InstanceIds = fmt.Sprintf(`["%s"]`, instanceID)

	response, err := client.DescribeInstances(request)
	if err != nil {
		return nil, fmt.Errorf("failed to get instance: %w", err)
	}

	if len(response.Instances.Instance) == 0 {
		return nil, fmt.Errorf("instance %s not found", instanceID)
	}

	inst := response.Instances.Instance[0]
	var publicIP, privateIP string
	if len(inst.PublicIpAddress.IpAddress) > 0 {
		publicIP = inst.PublicIpAddress.IpAddress[0]
	}
	if publicIP == "" && inst.EipAddress.IpAddress != "" {
		publicIP = inst.EipAddress.IpAddress
	}
	if len(inst.InnerIpAddress.IpAddress) > 0 {
		privateIP = inst.InnerIpAddress.IpAddress[0]
	}
	if privateIP == "" && len(inst.VpcAttributes.PrivateIpAddress.IpAddress) > 0 {
		privateIP = inst.VpcAttributes.PrivateIpAddress.IpAddress[0]
	}

	return &SpotInstance{
		InstanceID:       inst.InstanceId,
		InstanceName:     inst.InstanceName,
		RegionID:         regionID,
		Status:           inst.Status,
		PublicIPAddress:  publicIP,
		PrivateIPAddress: privateIP,
		SpotStrategy:     inst.SpotStrategy,
	}, nil
}

// StartInstance starts an instance
func (c *ECSClient) StartInstance(regionID, instanceID string) error {
	client, err := c.getClient(regionID)
	if err != nil {
		return err
	}

	request := ecs.CreateStartInstanceRequest()
	request.Scheme = "https"
	request.InstanceId = instanceID

	_, err = client.StartInstance(request)
	if err != nil {
		// Check if instance is already running or starting
		if strings.Contains(err.Error(), "IncorrectInstanceStatus") {
			log.Warnf("Instance %s is not in stopped state, skipping start", instanceID)
			return nil
		}
		return fmt.Errorf("failed to start instance %s: %w", instanceID, err)
	}

	return nil
}

// StopInstance stops an instance with the specified stopped mode
// stoppedMode can be "StopCharging" (cost-saving) or "KeepCharging"
func (c *ECSClient) StopInstance(regionID, instanceID, stoppedMode string) error {
	client, err := c.getClient(regionID)
	if err != nil {
		return err
	}

	request := ecs.CreateStopInstanceRequest()
	request.Scheme = "https"
	request.InstanceId = instanceID
	request.StoppedMode = stoppedMode

	_, err = client.StopInstance(request)
	if err != nil {
		// Check if instance is already stopped
		if strings.Contains(err.Error(), "IncorrectInstanceStatus") {
			log.Warnf("Instance %s is not in running state, skipping stop", instanceID)
			return nil
		}
		return fmt.Errorf("failed to stop instance %s: %w", instanceID, err)
	}

	return nil
}

// DiscoverAllSpotInstances discovers all spot instances across all regions
func (c *ECSClient) DiscoverAllSpotInstances() ([]*SpotInstance, error) {
	log.Info("Fetching all regions...")
	regions, err := c.GetAllRegions()
	if err != nil {
		return nil, err
	}
	log.Infof("Found %d regions, scanning for spot instances...", len(regions))

	// Use concurrent scanning for faster discovery
	var (
		allInstances []*SpotInstance
		mu           sync.Mutex
		wg           sync.WaitGroup
		semaphore    = make(chan struct{}, 10) // Limit concurrent requests
	)

	startTime := time.Now()
	scannedCount := 0
	var scannedMu sync.Mutex

	for _, region := range regions {
		wg.Add(1)
		go func(regionID string) {
			defer wg.Done()
			semaphore <- struct{}{}        // Acquire
			defer func() { <-semaphore }() // Release

			instances, err := c.GetSpotInstances(regionID)

			scannedMu.Lock()
			scannedCount++
			progress := scannedCount
			scannedMu.Unlock()

			if err != nil {
				log.Debugf("[%d/%d] Region %s: error - %v", progress, len(regions), regionID, err)
				return
			}

			if len(instances) > 0 {
				mu.Lock()
				allInstances = append(allInstances, instances...)
				mu.Unlock()
				log.Infof("[%d/%d] Region %s: found %d spot instance(s)", progress, len(regions), regionID, len(instances))
			} else {
				log.Debugf("[%d/%d] Region %s: no spot instances", progress, len(regions), regionID)
			}
		}(region)
	}

	wg.Wait()
	log.Infof("Scan completed in %.1f seconds", time.Since(startTime).Seconds())

	return allInstances, nil
}
