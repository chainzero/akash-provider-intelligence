package intelligence

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chainzero/akash-provider-intelligence/internal/akash"
)

type Config struct {
	AkashGRPCEndpoint   string
	CacheTTL            time.Duration
	StatusTimeout       time.Duration
	MaxConcurrent       int
	HealthCheckInterval time.Duration
}

type Service struct {
	config      *Config
	akashClient *akash.Client
	cache       *ProviderCache
	mutex       sync.RWMutex
}

type ProviderCache struct {
	data       map[string]*CachedProvider
	lastUpdate time.Time
	mutex      sync.RWMutex
}

type CachedProvider struct {
	Info      *akash.ProviderInfo `json:"info"`
	CachedAt  time.Time           `json:"cached_at"`
	ExpiresAt time.Time           `json:"expires_at"`
}

type ProviderSelection struct {
	SelectedProvider string                 `json:"selected_provider"`
	Score            float64                `json:"score"`
	Reasoning        string                 `json:"reasoning"`
	AllProviders     []*akash.ProviderInfo  `json:"all_providers"`
	Criteria         SelectionCriteria      `json:"criteria"`
	Stats            map[string]interface{} `json:"stats"`
	QueryTime        time.Duration          `json:"query_time"`
}

type SelectionCriteria struct {
	Priority string  `json:"priority"`
	Budget   float64 `json:"budget"`
	Weights  Weights `json:"weights"`
}

type Weights struct {
	Price       float64 `json:"price"`
	Reliability float64 `json:"reliability"`
	Performance float64 `json:"performance"`
	Geographic  float64 `json:"geographic"`
}

type ScoredProvider struct {
	Provider  *akash.ProviderInfo `json:"provider"`
	Score     float64             `json:"score"`
	Breakdown ScoreBreakdown      `json:"breakdown"`
}

type ScoreBreakdown struct {
	HealthScore      float64 `json:"health_score"`
	PerformanceScore float64 `json:"performance_score"`
	GeographicScore  float64 `json:"geographic_score"`
	PriceScore       float64 `json:"price_score"`
	PriorityBonus    float64 `json:"priority_bonus"`
}

func NewService(config *Config) (*Service, error) {
	akashClient := akash.NewClient(config.AkashGRPCEndpoint)

	service := &Service{
		config:      config,
		akashClient: akashClient,
		cache: &ProviderCache{
			data:       make(map[string]*CachedProvider),
			lastUpdate: time.Time{},
		},
	}

	// Start background cache cleanup
	go service.cacheCleanupLoop()

	return service, nil
}

// Get provider intelligence with caching and concurrent queries
func (s *Service) GetProviderIntelligence(ctx context.Context, addresses []string) ([]*akash.ProviderInfo, error) {
	if len(addresses) == 0 {
		return []*akash.ProviderInfo{}, nil
	}

	start := time.Now()
	var results []*akash.ProviderInfo
	var toFetch []string

	// Check cache first
	s.cache.mutex.RLock()
	for _, addr := range addresses {
		if cached, exists := s.cache.data[addr]; exists && time.Now().Before(cached.ExpiresAt) {
			results = append(results, cached.Info)
		} else {
			toFetch = append(toFetch, addr)
		}
	}
	s.cache.mutex.RUnlock()

	// Fetch missing providers concurrently
	if len(toFetch) > 0 {
		freshData, err := s.akashClient.GetMultipleProviderInfo(ctx, toFetch)
		if err != nil {
			return results, fmt.Errorf("failed to fetch provider data: %w", err)
		}

		// Update cache
		s.cache.mutex.Lock()
		for _, info := range freshData {
			s.cache.data[info.Address] = &CachedProvider{
				Info:      info,
				CachedAt:  time.Now(),
				ExpiresAt: time.Now().Add(s.config.CacheTTL),
			}
		}
		s.cache.lastUpdate = time.Now()
		s.cache.mutex.Unlock()

		results = append(results, freshData...)
	}

	// Log performance
	queryTime := time.Since(start)
	fmt.Printf("🔍 Provider intelligence query completed: %d providers in %v (%d from cache, %d fresh)\n",
		len(results), queryTime, len(addresses)-len(toFetch), len(toFetch))

	return results, nil
}

// Select optimal provider based on criteria with detailed scoring
func (s *Service) SelectOptimalProvider(ctx context.Context, addresses []string, criteria SelectionCriteria) (*ProviderSelection, error) {
	start := time.Now()

	// Get provider intelligence
	providers, err := s.GetProviderIntelligence(ctx, addresses)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider intelligence: %w", err)
	}

	if len(providers) == 0 {
		return nil, fmt.Errorf("no provider data available")
	}

	// Score each provider with detailed breakdown
	scoredProviders := make([]ScoredProvider, 0, len(providers))
	for _, provider := range providers {
		score, breakdown := s.scoreProviderWithBreakdown(provider, criteria)
		scoredProviders = append(scoredProviders, ScoredProvider{
			Provider:  provider,
			Score:     score,
			Breakdown: breakdown,
		})
	}

	// Sort by score (highest first)
	sort.Slice(scoredProviders, func(i, j int) bool {
		return scoredProviders[i].Score > scoredProviders[j].Score
	})

	// Build selection result
	best := scoredProviders[0]
	reasoning := s.buildDetailedReasoning(best, scoredProviders, criteria)
	stats := s.akashClient.GetProviderStats(providers)

	return &ProviderSelection{
		SelectedProvider: best.Provider.Address,
		Score:            best.Score,
		Reasoning:        reasoning,
		AllProviders:     providers,
		Criteria:         criteria,
		Stats:            stats,
		QueryTime:        time.Since(start),
	}, nil
}

// Score a provider with detailed breakdown
func (s *Service) scoreProviderWithBreakdown(provider *akash.ProviderInfo, criteria SelectionCriteria) (float64, ScoreBreakdown) {
	breakdown := ScoreBreakdown{}

	// Health score component (base reliability)
	breakdown.HealthScore = provider.HealthScore
	score := breakdown.HealthScore * criteria.Weights.Reliability

	// Performance score (response time and resources)
	breakdown.PerformanceScore = s.calculatePerformanceScore(provider)
	score += breakdown.PerformanceScore * criteria.Weights.Performance

	// Geographic score (based on attributes)
	breakdown.GeographicScore = s.calculateGeographicScore(provider)
	score += breakdown.GeographicScore * criteria.Weights.Geographic

	// Price component (placeholder - would need bid data)
	breakdown.PriceScore = s.calculatePriceScore(provider)
	score += breakdown.PriceScore * criteria.Weights.Price

	// Priority adjustments
	breakdown.PriorityBonus = s.calculatePriorityBonus(provider, criteria.Priority)
	score += breakdown.PriorityBonus

	return score, breakdown
}

// Calculate performance score based on response time and resources
func (s *Service) calculatePerformanceScore(provider *akash.ProviderInfo) float64 {
	score := 0.0

	// Response time scoring (50% of performance score)
	if provider.StatusQueryTime > 0 {
		if provider.StatusQueryTime < 300*time.Millisecond {
			score += 0.5
		} else if provider.StatusQueryTime < 500*time.Millisecond {
			score += 0.45
		} else if provider.StatusQueryTime < 1*time.Second {
			score += 0.4
		} else if provider.StatusQueryTime < 2*time.Second {
			score += 0.3
		} else if provider.StatusQueryTime < 3*time.Second {
			score += 0.2
		} else {
			score += 0.1
		}
	}

	// Resource availability scoring (30% of performance score)
	if provider.ClusterInfo != nil {
		if provider.ClusterInfo.AvailableNodes > 0 {
			score += 0.15
		}

		// Score based on available resources
		available := provider.ClusterInfo.AvailableResources
		if available.CPU > 1000 { // More than 1 CPU available
			score += 0.075
		}
		if available.Memory > 1024*1024*1024 { // More than 1GB available
			score += 0.075
		}
	}

	// Blockchain query performance (20% of performance score)
	if provider.BlockchainQueryTime > 0 && provider.BlockchainQueryTime < 2*time.Second {
		score += 0.2
	} else if provider.BlockchainQueryTime > 0 && provider.BlockchainQueryTime < 5*time.Second {
		score += 0.1
	}

	return score
}

// Calculate geographic score based on provider attributes
func (s *Service) calculateGeographicScore(provider *akash.ProviderInfo) float64 {
	// Default neutral score
	score := 0.5

	if region, ok := provider.Attributes["region"]; ok {
		// Prefer US regions (example preference - could be configurable)
		switch region {
		case "us-west-1", "us-west-2":
			score = 0.95
		case "us-east-1", "us-east-2":
			score = 0.9
		case "us-central-1":
			score = 0.85
		case "eu-west-1", "eu-central-1":
			score = 0.75
		case "ap-southeast-1", "ap-northeast-1":
			score = 0.7
		default:
			score = 0.6
		}
	}

	// Bonus for datacenter info
	if _, hasDatacenter := provider.Attributes["datacenter"]; hasDatacenter {
		score += 0.05
	}

	return score
}

// Calculate price score (placeholder for bid-based pricing)
func (s *Service) calculatePriceScore(provider *akash.ProviderInfo) float64 {
	// Placeholder scoring - would integrate with bid data
	// For now, use some heuristics based on provider characteristics

	score := 0.5 // Default neutral score

	// Providers with more active leases might be slightly more expensive but more reliable
	if provider.ClusterInfo != nil {
		leases := provider.ClusterInfo.ActiveLeases
		if leases > 100 {
			score = 0.4 // Slightly lower price score (higher cost) but proven reliability
		} else if leases > 50 {
			score = 0.45
		} else if leases > 20 {
			score = 0.5
		} else if leases > 5 {
			score = 0.55
		} else {
			score = 0.6 // Higher price score (lower cost) for newer providers
		}
	}

	// GPU providers might be more expensive
	for key := range provider.Attributes {
		if key == "capabilities/gpu/vendor/nvidia" {
			score -= 0.1 // GPU providers typically cost more
			break
		}
	}

	// Ensure score stays within bounds
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}

	return score
}

// Calculate priority bonus based on selection criteria
func (s *Service) calculatePriorityBonus(provider *akash.ProviderInfo, priority string) float64 {
	bonus := 0.0

	switch priority {
	case "reliability":
		if provider.HealthScore > 0.8 {
			bonus = 0.2
		} else if provider.HealthScore > 0.6 {
			bonus = 0.1
		}
	case "performance":
		if provider.StatusQueryTime > 0 && provider.StatusQueryTime < 500*time.Millisecond {
			bonus = 0.2
		} else if provider.StatusQueryTime > 0 && provider.StatusQueryTime < 1*time.Second {
			bonus = 0.1
		}
	case "cost":
		// Favor providers with fewer active leases (potentially lower cost)
		if provider.ClusterInfo != nil && provider.ClusterInfo.ActiveLeases < 20 {
			bonus = 0.2
		} else if provider.ClusterInfo != nil && provider.ClusterInfo.ActiveLeases < 50 {
			bonus = 0.1
		}
	}

	return bonus
}

// Build detailed human-readable reasoning for the selection
func (s *Service) buildDetailedReasoning(best ScoredProvider, all []ScoredProvider, criteria SelectionCriteria) string {
	reasoning := fmt.Sprintf("🎯 Selected provider %s with overall score %.3f\n\n",
		best.Provider.Address, best.Score)

	reasoning += "📊 Score Breakdown:\n"
	reasoning += fmt.Sprintf("  • Health/Reliability: %.3f (weight: %.1f%%)\n",
		best.Breakdown.HealthScore, criteria.Weights.Reliability*100)
	reasoning += fmt.Sprintf("  • Performance: %.3f (weight: %.1f%%)\n",
		best.Breakdown.PerformanceScore, criteria.Weights.Performance*100)
	reasoning += fmt.Sprintf("  • Geographic: %.3f (weight: %.1f%%)\n",
		best.Breakdown.GeographicScore, criteria.Weights.Geographic*100)
	reasoning += fmt.Sprintf("  • Price: %.3f (weight: %.1f%%)\n",
		best.Breakdown.PriceScore, criteria.Weights.Price*100)

	if best.Breakdown.PriorityBonus > 0 {
		reasoning += fmt.Sprintf("  • Priority Bonus (%s): +%.3f\n",
			criteria.Priority, best.Breakdown.PriorityBonus)
	}

	reasoning += "\n🔍 Provider Details:\n"

	// Health/reliability info
	if best.Provider.ClusterInfo != nil {
		reasoning += fmt.Sprintf("  • %d active leases (reliability indicator)\n",
			best.Provider.ClusterInfo.ActiveLeases)
	}

	// Performance info
	if best.Provider.StatusQueryTime > 0 {
		reasoning += fmt.Sprintf("  • %v status endpoint response time\n",
			best.Provider.StatusQueryTime)
	}

	if best.Provider.BlockchainQueryTime > 0 {
		reasoning += fmt.Sprintf("  • %v blockchain query time\n",
			best.Provider.BlockchainQueryTime)
	}

	if best.Provider.ClusterInfo != nil && best.Provider.ClusterInfo.AvailableNodes > 0 {
		reasoning += fmt.Sprintf("  • %d available nodes\n",
			best.Provider.ClusterInfo.AvailableNodes)
	}

	// Regional info
	if region, ok := best.Provider.Attributes["region"]; ok {
		reasoning += fmt.Sprintf("  • Located in %s region\n", region)
	}

	// GPU capabilities
	for key := range best.Provider.Attributes {
		if key == "capabilities/gpu/vendor/nvidia" {
			reasoning += "  • NVIDIA GPU capabilities available\n"
			break
		}
	}

	// Resource availability
	if best.Provider.ClusterInfo != nil {
		available := best.Provider.ClusterInfo.AvailableResources
		if available.CPU > 0 || available.Memory > 0 || available.GPU > 0 {
			reasoning += "  • Available resources: "
			parts := []string{}
			if available.CPU > 0 {
				parts = append(parts, fmt.Sprintf("CPU: %d", available.CPU))
			}
			if available.Memory > 0 {
				parts = append(parts, fmt.Sprintf("Memory: %.1fGB", float64(available.Memory)/(1024*1024*1024)))
			}
			if available.GPU > 0 {
				parts = append(parts, fmt.Sprintf("GPU: %d", available.GPU))
			}
			reasoning += fmt.Sprintf("%s\n", strings.Join(parts, ", "))
		}
	}

	// Comparison with alternatives
	if len(all) > 1 {
		reasoning += fmt.Sprintf("\n📈 Competitive Analysis:\n")
		reasoning += fmt.Sprintf("  • Ranked #1 out of %d providers\n", len(all))

		if len(all) > 1 {
			secondBest := all[1]
			scoreDiff := best.Score - secondBest.Score
			reasoning += fmt.Sprintf("  • Score advantage over #2: +%.3f (%.1f%% better)\n",
				scoreDiff, (scoreDiff/secondBest.Score)*100)
		}
	}

	// Error information if any
	if best.Provider.Error != "" {
		reasoning += fmt.Sprintf("\n⚠️  Note: %s\n", best.Provider.Error)
	}

	return reasoning
}

// Get cache statistics
func (s *Service) GetCacheStats() map[string]interface{} {
	s.cache.mutex.RLock()
	defer s.cache.mutex.RUnlock()

	stats := map[string]interface{}{
		"entries":     len(s.cache.data),
		"ttl":         s.config.CacheTTL.String(),
		"last_update": s.cache.lastUpdate,
	}

	// Add cache hit ratios, expiry info, etc.
	var expired, valid int
	now := time.Now()
	for _, cached := range s.cache.data {
		if now.After(cached.ExpiresAt) {
			expired++
		} else {
			valid++
		}
	}

	stats["valid_entries"] = valid
	stats["expired_entries"] = expired

	return stats
}

// Background cache cleanup loop
func (s *Service) cacheCleanupLoop() {
	ticker := time.NewTicker(s.config.HealthCheckInterval)
	defer ticker.Stop()

	for range ticker.C {
		s.cleanupExpiredCache()
	}
}

// Clear expired cache entries
func (s *Service) cleanupExpiredCache() {
	s.cache.mutex.Lock()
	defer s.cache.mutex.Unlock()

	now := time.Now()
	initialCount := len(s.cache.data)

	for addr, cached := range s.cache.data {
		if now.After(cached.ExpiresAt) {
			delete(s.cache.data, addr)
		}
	}

	cleanedCount := initialCount - len(s.cache.data)
	if cleanedCount > 0 {
		fmt.Printf("🧹 Cache cleanup: removed %d expired entries, %d remaining\n",
			cleanedCount, len(s.cache.data))
	}
}
