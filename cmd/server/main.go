package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chainzero/akash-provider-intelligence/internal/intelligence"
	"github.com/gorilla/mux"
	"gopkg.in/yaml.v2"
)

type Config struct {
	Server struct {
		Port    int           `yaml:"port"`
		Host    string        `yaml:"host"`
		Timeout time.Duration `yaml:"timeout"`
	} `yaml:"server"`

	Akash struct {
		GRPCEndpoint string `yaml:"grpc_endpoint"`
		RPCEndpoint  string `yaml:"rpc_endpoint"`
		ChainID      string `yaml:"chain_id"`
	} `yaml:"akash"`

	Intelligence struct {
		CacheTTL            time.Duration `yaml:"cache_ttl"`
		StatusTimeout       time.Duration `yaml:"status_timeout"`
		MaxConcurrent       int           `yaml:"max_concurrent"`
		HealthCheckInterval time.Duration `yaml:"health_check_interval"`
	} `yaml:"intelligence"`

	Logging struct {
		Level  string `yaml:"level"`
		Format string `yaml:"format"`
	} `yaml:"logging"`

	SelectionWeights struct {
		Price       float64 `yaml:"price"`
		Reliability float64 `yaml:"reliability"`
		Performance float64 `yaml:"performance"`
		Geographic  float64 `yaml:"geographic"`
	} `yaml:"selection_weights"`
}

type MCPServer struct {
	config              *Config
	intelligenceService *intelligence.Service
	router              *mux.Router
}

func loadConfig(configPath string) (*Config, error) {
	config := &Config{}

	file, err := os.Open(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	if err := decoder.Decode(config); err != nil {
		return nil, fmt.Errorf("failed to decode config: %w", err)
	}

	return config, nil
}

func NewMCPServer(config *Config) (*MCPServer, error) {
	// Initialize intelligence service
	intelService, err := intelligence.NewService(&intelligence.Config{
		AkashGRPCEndpoint:   config.Akash.GRPCEndpoint,
		CacheTTL:            config.Intelligence.CacheTTL,
		StatusTimeout:       config.Intelligence.StatusTimeout,
		MaxConcurrent:       config.Intelligence.MaxConcurrent,
		HealthCheckInterval: config.Intelligence.HealthCheckInterval,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create intelligence service: %w", err)
	}

	server := &MCPServer{
		config:              config,
		intelligenceService: intelService,
		router:              mux.NewRouter(),
	}

	server.setupRoutes()
	return server, nil
}

func (s *MCPServer) setupRoutes() {
	// MCP Protocol endpoints
	s.router.HandleFunc("/tools", s.handleTools).Methods("GET")
	s.router.HandleFunc("/call", s.handleToolCall).Methods("POST")

	// Health check endpoint
	s.router.HandleFunc("/health", s.handleHealth).Methods("GET")

	// Status endpoint for debugging
	s.router.HandleFunc("/status", s.handleStatus).Methods("GET")

	// CORS middleware for web clients
	s.router.Use(corsMiddleware)
}

// CORS middleware
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// MCP Tools response
func (s *MCPServer) handleTools(w http.ResponseWriter, r *http.Request) {
	tools := map[string]interface{}{
		"tools": []map[string]interface{}{
			{
				"name":        "get_provider_intelligence",
				"description": "Get comprehensive intelligence data for Akash providers",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"provider_addresses": map[string]interface{}{
							"type":        "array",
							"items":       map[string]string{"type": "string"},
							"description": "List of provider addresses to analyze",
						},
					},
					"required": []string{"provider_addresses"},
				},
			},
			{
				"name":        "select_optimal_provider",
				"description": "Choose the best provider based on requirements and available intelligence",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"requirements": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"cpu":    map[string]string{"type": "string"},
								"memory": map[string]string{"type": "string"},
								"gpu":    map[string]string{"type": "boolean"},
								"budget": map[string]string{"type": "number"},
								"priority": map[string]interface{}{
									"type": "string",
									"enum": []string{"cost", "performance", "reliability"},
								},
							},
						},
						"provider_bids": map[string]interface{}{
							"type":        "array",
							"description": "Array of bid data with provider addresses and prices",
						},
					},
					"required": []string{"requirements", "provider_bids"},
				},
			},
			{
				"name":        "get_market_trends",
				"description": "Get current market trends and pricing analysis",
				"parameters": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"timeframe": map[string]interface{}{
							"type":        "string",
							"description": "Time period for analysis (1h, 24h, 7d)",
							"default":     "24h",
						},
					},
				},
			},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tools)
}

// MCP Tool call handler
func (s *MCPServer) handleToolCall(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Tool      string                 `json:"tool"`
		Arguments map[string]interface{} `json:"arguments"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	var response interface{}
	var err error

	switch request.Tool {
	case "get_provider_intelligence":
		response, err = s.handleGetProviderIntelligence(request.Arguments)
	case "select_optimal_provider":
		response, err = s.handleSelectOptimalProvider(request.Arguments)
	case "get_market_trends":
		response, err = s.handleGetMarketTrends(request.Arguments)
	default:
		http.Error(w, fmt.Sprintf("Unknown tool: %s", request.Tool), http.StatusBadRequest)
		return
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": response,
			},
		},
	})
}

// Tool: Get Provider Intelligence
func (s *MCPServer) handleGetProviderIntelligence(args map[string]interface{}) (interface{}, error) {
	// Extract provider addresses from arguments
	addresses, ok := args["provider_addresses"]
	if !ok {
		return nil, fmt.Errorf("provider_addresses argument is required")
	}

	addressList, ok := addresses.([]interface{})
	if !ok {
		return nil, fmt.Errorf("provider_addresses must be an array")
	}

	// Convert to string slice
	var providerAddresses []string
	for _, addr := range addressList {
		if strAddr, ok := addr.(string); ok {
			providerAddresses = append(providerAddresses, strAddr)
		}
	}

	if len(providerAddresses) == 0 {
		return nil, fmt.Errorf("no valid provider addresses provided")
	}

	// Use the intelligence service to get provider info
	ctx := context.Background()
	providers, err := s.intelligenceService.GetProviderIntelligence(ctx, providerAddresses)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider intelligence: %w", err)
	}

	return providers, nil
}

// Tool: Select Optimal Provider
func (s *MCPServer) handleSelectOptimalProvider(args map[string]interface{}) (interface{}, error) {
	// Extract requirements
	requirements, ok := args["requirements"]
	if !ok {
		return nil, fmt.Errorf("requirements argument is required")
	}

	reqMap, ok := requirements.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("requirements must be an object")
	}

	// Extract provider bids
	providerBids, ok := args["provider_bids"]
	if !ok {
		return nil, fmt.Errorf("provider_bids argument is required")
	}

	bidsList, ok := providerBids.([]interface{})
	if !ok {
		return nil, fmt.Errorf("provider_bids must be an array")
	}

	// Extract provider addresses from bids
	var addresses []string
	for _, bid := range bidsList {
		if bidMap, ok := bid.(map[string]interface{}); ok {
			if provider, exists := bidMap["provider"]; exists {
				if providerStr, ok := provider.(string); ok {
					addresses = append(addresses, providerStr)
				}
			}
		}
	}

	if len(addresses) == 0 {
		return nil, fmt.Errorf("no valid provider addresses found in bids")
	}

	// Build selection criteria
	criteria := intelligence.SelectionCriteria{
		Weights: intelligence.Weights{
			Price:       s.config.SelectionWeights.Price,
			Reliability: s.config.SelectionWeights.Reliability,
			Performance: s.config.SelectionWeights.Performance,
			Geographic:  s.config.SelectionWeights.Geographic,
		},
	}

	// Set priority from requirements
	if priority, ok := reqMap["priority"]; ok {
		if priorityStr, ok := priority.(string); ok {
			criteria.Priority = priorityStr
		}
	}

	// Set budget from requirements
	if budget, ok := reqMap["budget"]; ok {
		if budgetFloat, ok := budget.(float64); ok {
			criteria.Budget = budgetFloat
		}
	}

	// Use intelligence service to select optimal provider
	ctx := context.Background()
	selection, err := s.intelligenceService.SelectOptimalProvider(ctx, addresses, criteria)
	if err != nil {
		return nil, fmt.Errorf("failed to select optimal provider: %w", err)
	}

	return selection, nil
}

// Tool: Get Market Trends - PLACEHOLDER FOR NOW
func (s *MCPServer) handleGetMarketTrends(args map[string]interface{}) (interface{}, error) {
	timeframe := "24h"
	if tf, ok := args["timeframe"]; ok {
		if tfStr, ok := tf.(string); ok {
			timeframe = tfStr
		}
	}

	// For now, return basic market analysis
	// This could be enhanced with historical data
	return map[string]interface{}{
		"timeframe": timeframe,
		"message":   "Market trends analysis - would integrate with historical provider data",
		"status":    "placeholder_implementation",
	}, nil
}
