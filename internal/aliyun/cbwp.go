package aliyun

import (
	"encoding/json"
	"fmt"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	log "github.com/sirupsen/logrus"
)

// CBWPClient wraps Aliyun VPC API calls for Common Bandwidth Package operations
type CBWPClient struct {
	accessKeyID     string
	accessKeySecret string
}

// BandwidthPackage represents a common bandwidth package
type BandwidthPackage struct {
	BandwidthPackageID string
	Name               string
	Bandwidth          string
	RegionID           string
	Status             string
}

// EIPInfo represents an Elastic IP address
type EIPInfo struct {
	AllocationID       string
	IPAddress          string
	BandwidthPackageID string // non-empty if EIP is in a bandwidth package
	InstanceID         string
	RegionID           string
	Status             string
}

// NewCBWPClient creates a new CBWP client
func NewCBWPClient(accessKeyID, accessKeySecret string) *CBWPClient {
	return &CBWPClient{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
	}
}

// newVPCRequest creates a CommonRequest for VPC API
func (c *CBWPClient) newVPCRequest(regionID, apiName string) *requests.CommonRequest {
	request := requests.NewCommonRequest()
	request.Method = "POST"
	request.Scheme = "https"
	request.Domain = "vpc.aliyuncs.com"
	request.Version = "2016-04-28"
	request.ApiName = apiName
	request.QueryParams["RegionId"] = regionID
	return request
}

// newClient creates an SDK client for the specified region
func (c *CBWPClient) newClient(regionID string) (*sdk.Client, error) {
	client, err := sdk.NewClientWithAccessKey(regionID, c.accessKeyID, c.accessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("failed to create SDK client for region %s: %w", regionID, err)
	}
	return client, nil
}

// DescribeEipAddresses queries EIP addresses associated with an instance
func (c *CBWPClient) DescribeEipAddresses(regionID, instanceID string) ([]*EIPInfo, error) {
	client, err := c.newClient(regionID)
	if err != nil {
		return nil, err
	}

	request := c.newVPCRequest(regionID, "DescribeEipAddresses")
	request.QueryParams["AssociatedInstanceType"] = "EcsInstance"
	request.QueryParams["AssociatedInstanceId"] = instanceID
	request.QueryParams["PageSize"] = "50"

	resp, err := client.ProcessCommonRequest(request)
	if err != nil {
		return nil, fmt.Errorf("failed to describe EIP addresses: %w", err)
	}

	var result struct {
		EipAddresses struct {
			EipAddress []struct {
				AllocationId       string `json:"AllocationId"`
				IpAddress          string `json:"IpAddress"`
				BandwidthPackageId string `json:"BandwidthPackageId"`
				InstanceId         string `json:"InstanceId"`
				Status             string `json:"Status"`
			} `json:"EipAddress"`
		} `json:"EipAddresses"`
	}

	if err := json.Unmarshal(resp.GetHttpContentBytes(), &result); err != nil {
		return nil, fmt.Errorf("failed to parse EIP response: %w", err)
	}

	var eips []*EIPInfo
	for _, eip := range result.EipAddresses.EipAddress {
		eips = append(eips, &EIPInfo{
			AllocationID:       eip.AllocationId,
			IPAddress:          eip.IpAddress,
			BandwidthPackageID: eip.BandwidthPackageId,
			InstanceID:         eip.InstanceId,
			RegionID:           regionID,
			Status:             eip.Status,
		})
	}

	log.Debugf("Found %d EIPs for instance %s in region %s", len(eips), instanceID, regionID)
	return eips, nil
}

// DescribeCommonBandwidthPackages queries common bandwidth packages in a region
func (c *CBWPClient) DescribeCommonBandwidthPackages(regionID string) ([]*BandwidthPackage, error) {
	client, err := c.newClient(regionID)
	if err != nil {
		return nil, err
	}

	request := c.newVPCRequest(regionID, "DescribeCommonBandwidthPackages")
	request.QueryParams["PageSize"] = "50"

	resp, err := client.ProcessCommonRequest(request)
	if err != nil {
		return nil, fmt.Errorf("failed to describe bandwidth packages: %w", err)
	}

	var result struct {
		CommonBandwidthPackages struct {
			CommonBandwidthPackage []struct {
				BandwidthPackageId string `json:"BandwidthPackageId"`
				Name               string `json:"Name"`
				Bandwidth          string `json:"Bandwidth"`
				RegionId           string `json:"RegionId"`
				Status             string `json:"Status"`
			} `json:"CommonBandwidthPackage"`
		} `json:"CommonBandwidthPackages"`
	}

	if err := json.Unmarshal(resp.GetHttpContentBytes(), &result); err != nil {
		return nil, fmt.Errorf("failed to parse bandwidth packages response: %w", err)
	}

	var packages []*BandwidthPackage
	for _, pkg := range result.CommonBandwidthPackages.CommonBandwidthPackage {
		packages = append(packages, &BandwidthPackage{
			BandwidthPackageID: pkg.BandwidthPackageId,
			Name:               pkg.Name,
			Bandwidth:          pkg.Bandwidth,
			RegionID:           pkg.RegionId,
			Status:             pkg.Status,
		})
	}

	log.Debugf("Found %d bandwidth packages in region %s", len(packages), regionID)
	return packages, nil
}

// AddCommonBandwidthPackageIp adds an EIP to a common bandwidth package
func (c *CBWPClient) AddCommonBandwidthPackageIp(regionID, bandwidthPackageID, eipID string) error {
	client, err := c.newClient(regionID)
	if err != nil {
		return err
	}

	request := c.newVPCRequest(regionID, "AddCommonBandwidthPackageIp")
	request.QueryParams["BandwidthPackageId"] = bandwidthPackageID
	request.QueryParams["IpInstanceId"] = eipID

	_, err = client.ProcessCommonRequest(request)
	if err != nil {
		return fmt.Errorf("failed to add EIP %s to bandwidth package %s: %w", eipID, bandwidthPackageID, err)
	}

	log.Infof("Successfully added EIP %s to bandwidth package %s", eipID, bandwidthPackageID)
	return nil
}

// RemoveCommonBandwidthPackageIp removes an EIP from a common bandwidth package
func (c *CBWPClient) RemoveCommonBandwidthPackageIp(regionID, bandwidthPackageID, eipID string) error {
	client, err := c.newClient(regionID)
	if err != nil {
		return err
	}

	request := c.newVPCRequest(regionID, "RemoveCommonBandwidthPackageIp")
	request.QueryParams["BandwidthPackageId"] = bandwidthPackageID
	request.QueryParams["IpInstanceId"] = eipID

	_, err = client.ProcessCommonRequest(request)
	if err != nil {
		return fmt.Errorf("failed to remove EIP %s from bandwidth package %s: %w", eipID, bandwidthPackageID, err)
	}

	log.Infof("Successfully removed EIP %s from bandwidth package %s", eipID, bandwidthPackageID)
	return nil
}
