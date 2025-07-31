package akash

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	providertypes "github.com/akash-network/akash-api/go/node/provider/v1beta3"
	"golang.org/x/sync/semaphore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Client struct {
	grpcEndpoint string
	httpClient   *http.Client
	semaphore    *semaphore.Weighted
}

type ProviderInfo struct {
	Address             string            `json:"address"`
	HostURI             string            `json:"host_uri"`
	Attributes          map[string]string `json:"attributes"`
	LastSeen            time.Time         `json:"last_seen"`
	StatusEndpoint      string            `json:"status_endpoint,omitempty"`
	ClusterInfo         *ClusterStatus    `json:"cluster_info,omitempty"`
	ResponseTime        time.Duration     `json:"response_time"`
	HealthScore         float64           `json:"health_score"`
	Error               string            `json:"error,omitempty"`
	BlockchainQueryTime time.Duration     `json:"blockchain_query_time"`
	StatusQueryTime     time.Duration     `json:"status_query_time"`
}

type ClusterStatus struct {
	ActiveLeases       int                    `json:"active_leases"`
	Inventory          map[string]interface{} `json:"inventory"`
	PublicHostname     string                 `json:"public_hostname"`
	AvailableNodes     int                    `json:"available_nodes"`
	TotalResources     ResourceSummary        `json:"total_resources"`
	AvailableResources ResourceSummary        `json:"available_resources"`
}

type ResourceSummary struct {
	CPU     int64 `json:"cpu"`
	Memory  int64 `json:"memory"`
	Storage int64 `json:"storage"`
	GPU     int   `json:"gpu"`
}

func NewClient(grpcEndpoint string) *Client {
	return &Client{
		grpcEndpoint: grpcEndpoint,
		httpClient: &http.Client{
			Timeout: 5 * time.Second, // Aggressive timeout for fast failures
		},
		semaphore: semaphore.NewWeighted(10), // Max 10 concurrent queries
	}
}

// Get multiple providers intelligence concurrently - THIS IS THE KEY PERFORMANCE FEATURE
func (c *Client) GetMultipleProviderInfo(ctx context.Context, addresses []string) ([]*ProviderInfo, error) {
	if len(addresses) == 0 {
		return []*ProviderInfo{}, nil
	}

	// Create context with timeout for the entire operation
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	results := make([]*ProviderInfo, len(addresses))
	var wg sync.WaitGroup

	// Launch concurrent queries
	for i, addr := range addresses {
		wg.Add(1)
		go func(index int, address string) {
			defer wg.Done()

			// Acquire semaphore to limit concurrency
			if err := c.semaphore.Acquire(ctx, 1); err != nil {
				results[index] = &ProviderInfo{
					Address:     address,
					LastSeen:    time.Now(),
					Error:       "concurrency limit exceeded",
					HealthScore: 0.0,
				}
				return
			}
			defer c.semaphore.Release(1)

			// Query provider with timeout
			info, err := c.GetProviderInfo(ctx, address)
			if err != nil {
				info = &ProviderInfo{
					Address:     address,
					LastSeen:    time.Now(),
					Error:       err.Error(),
					HealthScore: 0.0,
				}
			}
			results[index] = info
		}(i, addr)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	return results, nil
}

// Get provider information from blockchain and status endpoint
func (c *Client) GetProviderInfo(ctx context.Context, providerAddr string) (*ProviderInfo, error) {
	info := &ProviderInfo{
		Address:  providerAddr,
		LastSeen: time.Now(),
	}

	// Shorter timeout for individual queries
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	// Step 1: Query blockchain for provider info
	blockchainStart := time.Now()
	provider, err := c.queryBlockchainProvider(ctx, providerAddr)
	info.BlockchainQueryTime = time.Since(blockchainStart)

	if err != nil {
		return info, fmt.Errorf("blockchain query failed: %w", err)
	}

	// Parse blockchain data
	info.HostURI = provider.HostURI
	info.Attributes = make(map[string]string)
	for _, attr := range provider.Attributes {
		info.Attributes[attr.Key] = attr.Value
	}

	// Step 2: Query provider status endpoint if available
	if provider.HostURI != "" {
		info.StatusEndpoint = provider.HostURI

		// Create shorter timeout context for status query
		statusCtx, statusCancel := context.WithTimeout(ctx, 3*time.Second)
		defer statusCancel()

		statusStart := time.Now()
		clusterInfo, err := c.queryProviderStatus(statusCtx, provider.HostURI)
		info.StatusQueryTime = time.Since(statusStart)
		info.ResponseTime = info.StatusQueryTime // For backward compatibility

		if err != nil {
			info.Error = err.Error()
			info.HealthScore = c.calculatePartialHealthScore(info)
		} else {
			info.ClusterInfo = clusterInfo
			info.HealthScore = c.calculateHealthScore(info)
		}
	} else {
		info.HealthScore = c.calculatePartialHealthScore(info)
	}

	return info, nil
}

// Query provider from Akash blockchain
func (c *Client) queryBlockchainProvider(ctx context.Context, providerAddr string) (*providertypes.Provider, error) {
	conn, err := grpc.DialContext(ctx, c.grpcEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to gRPC %s: %w", c.grpcEndpoint, err)
	}
	defer conn.Close()

	client := providertypes.NewQueryClient(conn)
	resp, err := client.Provider(ctx, &providertypes.QueryProviderRequest{
		Owner: providerAddr,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query provider %s: %w", providerAddr, err)
	}

	return &resp.Provider, nil
}

// Query provider status endpoint
func (c *Client) queryProviderStatus(ctx context.Context, hostURI string) (*ClusterStatus, error) {
	statusURL := fmt.Sprintf("%s/status", hostURI)

	// Create request with context
	req, err := http.NewRequestWithContext(ctx, "GET", statusURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to query status endpoint %s: %w", statusURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status endpoint returned %d for %s", resp.StatusCode, statusURL)
	}

	var status struct {
		Cluster struct {
			Leases    int                    `json:"leases"`
			Inventory map[string]interface{} `json:"inventory"`
		} `json:"cluster"`
		ClusterPublicHostname string `json:"cluster_public_hostname"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("failed to decode status response from %s: %w", statusURL, err)
	}

	clusterInfo := &ClusterStatus{
		ActiveLeases:   status.Cluster.Leases,
		Inventory:      status.Cluster.Inventory,
		PublicHostname: status.ClusterPublicHostname,
	}

	// Parse inventory for resource summary
	clusterInfo.TotalResources, clusterInfo.AvailableResources = c.parseInventory(status.Cluster.Inventory)

	// Count available nodes
	if inventory, ok := status.Cluster.Inventory["available"]; ok {
		if availableData, ok := inventory.(map[string]interface{}); ok {
			if nodes, ok := availableData["nodes"]; ok {
				if nodesList, ok := nodes.([]interface{}); ok {
					clusterInfo.AvailableNodes = len(nodesList)
				}
			}
		}
	}

	return clusterInfo, nil
}

// Parse inventory data to extract resource summaries
func (c *Client) parseInventory(inventory map[string]interface{}) (ResourceSummary, ResourceSummary) {
	var total, available ResourceSummary

	// Parse available resources
	if availableData, ok := inventory["available"]; ok {
		if availableMap, ok := availableData.(map[string]interface{}); ok {
			if nodes, ok := availableMap["nodes"]; ok {
				if nodesList, ok := nodes.([]interface{}); ok {
					for _, node := range nodesList {
						if nodeMap, ok := node.(map[string]interface{}); ok {
							if availableRes, ok := nodeMap["available"]; ok {
								if resMap, ok := availableRes.(map[string]interface{}); ok {
									available.CPU += parseResourceValue(resMap, "cpu")
									available.Memory += parseResourceValue(resMap, "memory")
									available.Storage += parseResourceValue(resMap, "storage_ephemeral")
									available.GPU += int(parseResourceValue(resMap, "gpu"))
								}
							}
							if allocatableRes, ok := nodeMap["allocatable"]; ok {
								if resMap, ok := allocatableRes.(map[string]interface{}); ok {
									total.CPU += parseResourceValue(resMap, "cpu")
									total.Memory += parseResourceValue(resMap, "memory")
									total.Storage += parseResourceValue(resMap, "storage_ephemeral")
									total.GPU += int(parseResourceValue(resMap, "gpu"))
								}
							}
						}
					}
				}
			}
		}
	}

	return total, available
}

// Helper function to parse resource values
func parseResourceValue(resMap map[string]interface{}, key string) int64 {
	if val, ok := resMap[key]; ok {
		switch v := val.(type) {
		case float64:
			return int64(v)
		case int64:
			return v
		case int:
			return int64(v)
		case string:
			// Handle string values like "1000m" for CPU
			return 0 // Could implement string parsing if needed
		}
	}
	return 0
}

// Calculate health score based on available data
func (c *Client) calculateHealthScore(info *ProviderInfo) float64 {
	score := 0.0

	// Base score for having blockchain data (20%)
	score += 0.2

	// Response time scoring (30%)
	if info.StatusQueryTime > 0 {
		if info.StatusQueryTime < 500*time.Millisecond {
			score += 0.3
		} else if info.StatusQueryTime < 1*time.Second {
			score += 0.25
		} else if info.StatusQueryTime < 2*time.Second {
			score += 0.2
		} else if info.StatusQueryTime < 3*time.Second {
			score += 0.15
		} else {
			score += 0.1
		}
	}

	// Active leases scoring (40%) - more leases = more reliable
	if info.ClusterInfo != nil {
		leases := info.ClusterInfo.ActiveLeases
		if leases > 100 {
			score += 0.4
		} else if leases > 50 {
			score += 0.35
		} else if leases > 20 {
			score += 0.3
		} else if leases > 10 {
			score += 0.25
		} else if leases > 5 {
			score += 0.2
		} else if leases > 0 {
			score += 0.1
		}
	}

	// Resource availability scoring (10%)
	if info.ClusterInfo != nil && info.ClusterInfo.AvailableNodes > 0 {
		score += 0.1
	}

	return score
}

// Calculate partial health score when status endpoint is unavailable
func (c *Client) calculatePartialHealthScore(info *ProviderInfo) float64 {
	score := 0.2 // Base score for blockchain data

	// Check blockchain query performance
	if info.BlockchainQueryTime > 0 && info.BlockchainQueryTime < 2*time.Second {
		score += 0.1
	}

	// Check for provider tier/attributes
	if tier, ok := info.Attributes["tier"]; ok && tier == "enterprise" {
		score += 0.1
	}

	// Check for GPU capabilities
	if _, hasGPU := info.Attributes["capabilities/gpu/vendor/nvidia"]; hasGPU {
		score += 0.05
	}

	// Check for region
	if _, hasRegion := info.Attributes["region"]; hasRegion {
		score += 0.05
	}

	// Small penalty for unreachable status endpoint
	if info.Error != "" {
		score -= 0.1
		if score < 0 {
			score = 0
		}
	}

	return score
}

// Get provider statistics summary
func (c *Client) GetProviderStats(providers []*ProviderInfo) map[string]interface{} {
	stats := map[string]interface{}{
		"total_providers":       len(providers),
		"healthy_providers":     0,
		"providers_with_status": 0,
		"average_response_time": time.Duration(0),
		"total_active_leases":   0,
		"providers_by_region":   make(map[string]int),
		"providers_with_gpu":    0,
	}

	if len(providers) == 0 {
		return stats
	}

	var totalResponseTime time.Duration
	var responseTimeCount int
	regionCount := make(map[string]int)

	for _, provider := range providers {
		// Count healthy providers
		if provider.HealthScore > 0.5 {
			stats["healthy_providers"] = stats["healthy_providers"].(int) + 1
		}

		// Count providers with status
		if provider.ClusterInfo != nil {
			stats["providers_with_status"] = stats["providers_with_status"].(int) + 1
			stats["total_active_leases"] = stats["total_active_leases"].(int) + provider.ClusterInfo.ActiveLeases
		}

		// Calculate average response time
		if provider.StatusQueryTime > 0 {
			totalResponseTime += provider.StatusQueryTime
			responseTimeCount++
		}

		// Count by region
		if region, ok := provider.Attributes["region"]; ok {
			regionCount[region]++
		}

		// Count GPU providers
		for key := range provider.Attributes {
			if key == "capabilities/gpu/vendor/nvidia" {
				stats["providers_with_gpu"] = stats["providers_with_gpu"].(int) + 1
				break
			}
		}
	}

	// Calculate average response time
	if responseTimeCount > 0 {
		stats["average_response_time"] = totalResponseTime / time.Duration(responseTimeCount)
	}

	stats["providers_by_region"] = regionCount

	return stats
}
