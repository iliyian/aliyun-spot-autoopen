package gcp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	log "github.com/sirupsen/logrus"
)

// PreemptibleInstance represents a GCP preemptible/spot VM instance
type PreemptibleInstance struct {
	InstanceName string
	Zone         string
	Status       string
	ExternalIP   string
	InternalIP   string
	MachineType  string
	Preemptible  bool
	SpotVM       bool // newer spot VM provisioning model
}

// ComputeClient wraps the GCP Compute Engine client
type ComputeClient struct {
	projectID   string
	client      *compute.InstancesClient
	zonesClient *compute.ZonesClient
	mu          sync.Mutex
}

// NewComputeClient creates a new GCP Compute Engine client
// credentialsJSON is the raw JSON content of a service account key; empty = use ADC
func NewComputeClient(projectID, credentialsJSON string) (*ComputeClient, error) {
	ctx := context.Background()

	var opts []option.ClientOption
	if credentialsJSON != "" {
		opts = append(opts, option.WithCredentialsJSON([]byte(credentialsJSON)))
	}

	instancesClient, err := compute.NewInstancesRESTClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCP instances client: %w", err)
	}

	zonesClient, err := compute.NewZonesRESTClient(ctx, opts...)
	if err != nil {
		instancesClient.Close()
		return nil, fmt.Errorf("failed to create GCP zones client: %w", err)
	}

	return &ComputeClient{
		projectID:   projectID,
		client:      instancesClient,
		zonesClient: zonesClient,
	}, nil
}

// Close closes the underlying clients
func (c *ComputeClient) Close() {
	if c.client != nil {
		c.client.Close()
	}
	if c.zonesClient != nil {
		c.zonesClient.Close()
	}
}

// GetAllZones returns all available zones in the project
func (c *ComputeClient) GetAllZones() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req := &computepb.ListZonesRequest{
		Project: c.projectID,
	}

	var zones []string
	it := c.zonesClient.List(ctx, req)
	for {
		zone, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to list zones: %w", err)
		}
		if zone.GetStatus() == "UP" {
			zones = append(zones, zone.GetName())
		}
	}

	return zones, nil
}

// GetPreemptibleInstances returns all preemptible/spot instances in the specified zone
func (c *ComputeClient) GetPreemptibleInstances(zone string) ([]*PreemptibleInstance, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req := &computepb.ListInstancesRequest{
		Project: c.projectID,
		Zone:    zone,
	}

	var instances []*PreemptibleInstance
	it := c.client.List(ctx, req)
	for {
		inst, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to list instances in zone %s: %w", zone, err)
		}

		scheduling := inst.GetScheduling()
		if scheduling == nil {
			continue
		}

		isPreemptible := scheduling.GetPreemptible()
		isSpot := scheduling.GetProvisioningModel() == "SPOT"

		if !isPreemptible && !isSpot {
			continue
		}

		pi := &PreemptibleInstance{
			InstanceName: inst.GetName(),
			Zone:         zone,
			Status:       inst.GetStatus(),
			MachineType:  extractMachineType(inst.GetMachineType()),
			Preemptible:  isPreemptible,
			SpotVM:       isSpot,
		}

		// Extract IPs
		for _, iface := range inst.GetNetworkInterfaces() {
			if pi.InternalIP == "" {
				pi.InternalIP = iface.GetNetworkIP()
			}
			for _, ac := range iface.GetAccessConfigs() {
				if pi.ExternalIP == "" && ac.GetNatIP() != "" {
					pi.ExternalIP = ac.GetNatIP()
				}
			}
		}

		instances = append(instances, pi)
	}

	return instances, nil
}

// extractMachineType extracts the machine type name from the full URL
func extractMachineType(machineTypeURL string) string {
	parts := strings.Split(machineTypeURL, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return machineTypeURL
}

// GetInstanceStatus returns the current status of an instance
func (c *ComputeClient) GetInstanceStatus(zone, instanceName string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req := &computepb.GetInstanceRequest{
		Project:  c.projectID,
		Zone:     zone,
		Instance: instanceName,
	}

	inst, err := c.client.Get(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to get instance %s: %w", instanceName, err)
	}

	return inst.GetStatus(), nil
}

// StartInstance starts a stopped/terminated instance
func (c *ComputeClient) StartInstance(zone, instanceName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	req := &computepb.StartInstanceRequest{
		Project:  c.projectID,
		Zone:     zone,
		Instance: instanceName,
	}

	op, err := c.client.Start(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to start instance %s: %w", instanceName, err)
	}

	// Wait for the operation to complete
	if err := op.Wait(ctx); err != nil {
		return fmt.Errorf("failed waiting for instance %s to start: %w", instanceName, err)
	}

	return nil
}

// StopInstance stops a running instance
func (c *ComputeClient) StopInstance(zone, instanceName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	req := &computepb.StopInstanceRequest{
		Project:  c.projectID,
		Zone:     zone,
		Instance: instanceName,
	}

	op, err := c.client.Stop(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to stop instance %s: %w", instanceName, err)
	}

	if err := op.Wait(ctx); err != nil {
		return fmt.Errorf("failed waiting for instance %s to stop: %w", instanceName, err)
	}

	return nil
}

// GetInstance returns detailed information about an instance
func (c *ComputeClient) GetInstance(zone, instanceName string) (*PreemptibleInstance, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req := &computepb.GetInstanceRequest{
		Project:  c.projectID,
		Zone:     zone,
		Instance: instanceName,
	}

	inst, err := c.client.Get(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get instance %s: %w", instanceName, err)
	}

	scheduling := inst.GetScheduling()
	pi := &PreemptibleInstance{
		InstanceName: inst.GetName(),
		Zone:         zone,
		Status:       inst.GetStatus(),
		MachineType:  extractMachineType(inst.GetMachineType()),
		Preemptible:  scheduling.GetPreemptible(),
		SpotVM:       scheduling.GetProvisioningModel() == "SPOT",
	}

	for _, iface := range inst.GetNetworkInterfaces() {
		if pi.InternalIP == "" {
			pi.InternalIP = iface.GetNetworkIP()
		}
		for _, ac := range iface.GetAccessConfigs() {
			if pi.ExternalIP == "" && ac.GetNatIP() != "" {
				pi.ExternalIP = ac.GetNatIP()
			}
		}
	}

	return pi, nil
}

// DiscoverAllPreemptibleInstances discovers all preemptible/spot instances across specified zones
// If zones is empty, it discovers across all zones
func (c *ComputeClient) DiscoverAllPreemptibleInstances(zones []string) ([]*PreemptibleInstance, error) {
	if len(zones) == 0 {
		var err error
		zones, err = c.GetAllZones()
		if err != nil {
			return nil, err
		}
	}

	log.Infof("GCP: Scanning %d zones for preemptible/spot instances...", len(zones))

	var (
		allInstances []*PreemptibleInstance
		mu           sync.Mutex
		wg           sync.WaitGroup
		semaphore    = make(chan struct{}, 10)
	)

	startTime := time.Now()
	scannedCount := 0
	var scannedMu sync.Mutex

	for _, zone := range zones {
		wg.Add(1)
		go func(z string) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			instances, err := c.GetPreemptibleInstances(z)

			scannedMu.Lock()
			scannedCount++
			progress := scannedCount
			scannedMu.Unlock()

			if err != nil {
				log.Debugf("GCP [%d/%d] Zone %s: error - %v", progress, len(zones), z, err)
				return
			}

			if len(instances) > 0 {
				mu.Lock()
				allInstances = append(allInstances, instances...)
				mu.Unlock()
				log.Infof("GCP [%d/%d] Zone %s: found %d preemptible instance(s)", progress, len(zones), z, len(instances))
			} else {
				log.Debugf("GCP [%d/%d] Zone %s: no preemptible instances", progress, len(zones), z)
			}
		}(zone)
	}

	wg.Wait()
	log.Infof("GCP: Scan completed in %.1f seconds", time.Since(startTime).Seconds())

	return allInstances, nil
}
