package intelligence

import (
	"time"
)

type Config struct {
	AkashGRPCEndpoint   string
	CacheTTL           time.Duration
	StatusTimeout      time.Duration
	MaxConcurrent      int
	HealthCheckInterval time.Duration
}

type Service struct {
	config *Config
	cache  map[string]interface{}
}

func NewService(config *Config) (*Service, error) {
	return &Service{
		config: config,
		cache:  make(map[string]interface{}),
	}, nil
}

func (s *Service) GetCacheStats() map[string]interface{} {
	return map[string]interface{}{
		"entries": len(s.cache),
		"ttl":     s.config.CacheTTL.String(),
	}
}
