package main

import (
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
		CacheTTL           time.Duration `yaml:"cache_ttl"`
		StatusTimeout      time.Duration `yaml:"status_timeout"`
		MaxConcurrent      int           `yaml:"max_concurrent"`
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
		CacheTTL:           config.Intelligence.CacheTTL,
		StatusTimeout:      config.Intelligence.StatusTimeout,
		MaxConcurrent:      config.Intelligence.MaxConcurrent,
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
								"cpu":      map[string]string{"type": "string"},
								"memory":   map[string]string{"type": "string"},
								"gpu":      map[string]string{"type": "boolean"},
								"budget":   map[string]string{"type": "number"},
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

// Tool implementations (placeholder for now)
func (s *MCPServer) handleGetProviderIntelligence(args map[string]interface{}) (interface{}, error) {
	// TODO: Implement using intelligence service
	return map[string]interface{}{
		"message": "Provider intelligence will be implemented",
		"args":    args,
	}, nil
}

func (s *MCPServer) handleSelectOptimalProvider(args map[string]interface{}) (interface{}, error) {
	// TODO: Implement provider selection logic
	return map[string]interface{}{
		"message": "Provider selection will be implemented",
		"args":    args,
	}, nil
}

func (s *MCPServer) handleGetMarketTrends(args map[string]interface{}) (interface{}, error) {
	// TODO: Implement market trends analysis
	return map[string]interface{}{
		"message": "Market trends analysis will be implemented",
		"args":    args,
	}, nil
}

// Health check endpoint
func (s *MCPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	health := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().UTC(),
		"version":   "1.0.0",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(health)
}

// Status endpoint for debugging
func (s *MCPServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	status := map[string]interface{}{
		"server": map[string]interface{}{
			"uptime": time.Since(startTime),
			"port":   s.config.Server.Port,
			"host":   s.config.Server.Host,
		},
		"intelligence": map[string]interface{}{
			"cache_size": s.intelligenceService.GetCacheStats(),
		},
		"config": s.config,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

var startTime = time.Now()

func main() {
	// Command line flags
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	// Load configuration
	config, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Create MCP server
	server, err := NewMCPServer(config)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	// Start HTTP server
	addr := fmt.Sprintf("%s:%d", config.Server.Host, config.Server.Port)
	httpServer := &http.Server{
		Addr:         addr,
		Handler:      server.router,
		ReadTimeout:  config.Server.Timeout,
		WriteTimeout: config.Server.Timeout,
	}

	// Graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		log.Println("🛑 Shutting down server...")
		httpServer.Close()
	}()

	// Start server
	log.Printf("🚀 Akash Provider Intelligence MCP Server starting on %s", addr)
	log.Printf("📊 Health check: http://%s/health", addr)
	log.Printf("🔧 Status: http://%s/status", addr)
	log.Printf("🛠️  Tools: http://%s/tools", addr)

	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server failed to start: %v", err)
	}

	log.Println("✅ Server stopped gracefully")
}
