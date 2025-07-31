# Akash Provider Intelligence MCP Server

🚀 **Intelligent provider selection for Akash Network deployments using Model Context Protocol (MCP)**

This server provides AI-powered provider intelligence and selection for Akash Network deployments. It gathers real-time data from provider status endpoints and blockchain queries to make optimal provider selection decisions.

## 🎯 Features

- **🔍 Real-time Provider Intelligence**: Queries Akash blockchain and provider status endpoints
- **⚡ Concurrent Processing**: Handles multiple providers simultaneously for fast response times
- **🧠 AI-Powered Selection**: Multi-criteria scoring with configurable weights
- **💾 Intelligent Caching**: Reduces redundant queries with TTL-based caching
- **📊 Comprehensive Metrics**: Health scoring, performance analysis, and resource availability
- **🌐 Geographic Optimization**: Region-aware provider selection
- **🔄 MCP Protocol**: Standard interface for AI model integration

## 🏗️ Architecture

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   Your App      │────│   MCP Server     │────│  Akash Network  │
│                 │    │ (Intelligence)   │    │  (Providers)    │
└─────────────────┘    └──────────────────┘    └─────────────────┘
```

**Performance**: Concurrent provider queries reduce response time from 1000ms+ sequential to ~200-300ms parallel.

## 🚀 Quick Start

### Prerequisites

- Go 1.21+
- Access to Akash Network gRPC endpoints
- Ubuntu/Linux environment (tested on Ubuntu 22.04)

### Installation

```bash
# Clone the repository
git clone https://github.com/chainzero/akash-provider-intelligence.git
cd akash-provider-intelligence

# Install dependencies
go mod tidy

# Build the server
go build -o bin/mcp-server cmd/server/main.go

# Run the server
./bin/mcp-server -config config.yaml
```

### Configuration

Copy and customize the configuration:

```yaml
server:
  port: 8080
  host: "0.0.0.0"
  timeout: 30s

akash:
  grpc_endpoint: "34.135.123.180:9090"
  rpc_endpoint: "https://rpc.akashnet.net:443"
  chain_id: "akashnet-2"

intelligence:
  cache_ttl: "5m"
  status_timeout: "5s"
  max_concurrent: 10
  health_check_interval: "2m"

selection_weights:
  price: 0.4
  reliability: 0.3
  performance: 0.2
  geographic: 0.1
```

## 🛠️ MCP Tools

The server exposes three main tools:

### 1. `get_provider_intelligence`
Get comprehensive intelligence data for specific providers.

```json
{
  "tool": "get_provider_intelligence",
  "arguments": {
    "provider_addresses": ["akash1abc...", "akash1def..."]
  }
}
```

### 2. `select_optimal_provider`
Choose the best provider based on requirements and intelligence.

```json
{
  "tool": "select_optimal_provider",
  "arguments": {
    "requirements": {
      "cpu": "2000m",
      "memory": "4Gi",
      "gpu": true,
      "priority": "reliability"
    },
    "provider_bids": [...]
  }
}
```

### 3. `get_market_trends`
Analyze market trends and pricing patterns.

```json
{
  "tool": "get_market_trends",
  "arguments": {
    "timeframe": "24h"
  }
}
```

## 📊 API Endpoints

- `GET /health` - Health check
- `GET /status` - Server status and metrics
- `GET /tools` - Available MCP tools
- `POST /call` - Execute MCP tool

## 🔧 Usage Examples

### Test the Server
```bash
# Health check
curl http://localhost:8080/health

# Get available tools
curl http://localhost:8080/tools

# Get provider intelligence
curl -X POST http://localhost:8080/call \
  -H "Content-Type: application/json" \
  -d '{
    "tool": "get_provider_intelligence",
    "arguments": {
      "provider_addresses": ["akash1tasw683g5zhnctkhy003ezwndum2vzzpg7s33h"]
    }
  }'
```

### Integration with Your App
```go
// Example Go integration
resp, err := http.Post("http://mcp-server:8080/call", 
    "application/json",
    strings.NewReader(`{
        "tool": "select_optimal_provider",
        "arguments": {
            "requirements": {"priority": "performance"},
            "provider_bids": [...]
        }
    }`))
```

## 🏭 Production Deployment

### Using Systemd
```bash
# Create systemd service
sudo tee /etc/systemd/system/akash-mcp.service > /dev/null << 'EOF'
[Unit]
Description=Akash Provider Intelligence MCP Server
After=network.target

[Service]
Type=simple
User=ubuntu
WorkingDirectory=/opt/akash-provider-intelligence
ExecStart=/opt/akash-provider-intelligence/bin/mcp-server -config config.yaml
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl enable akash-mcp
sudo systemctl start akash-mcp
```

### Using Docker
```dockerfile
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN go mod tidy && go build -o mcp-server cmd/server/main.go

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/mcp-server .
COPY --from=builder /app/config.yaml .
EXPOSE 8080
CMD ["./mcp-server", "-config", "config.yaml"]
```

## 📈 Performance Characteristics

- **Concurrent Queries**: Up to 10 simultaneous provider checks
- **Response Time**: 200-300ms for 5 providers (vs 1000ms+ sequential)
- **Cache Hit Rate**: ~80% for frequently requested providers
- **Memory Usage**: ~1.5MB baseline, scales with cache size
- **CPU Usage**: Minimal during idle, brief spikes during queries

## 🏗️ Architecture Details

### Provider Intelligence Gathering
1. **Blockchain Query**: Provider attributes, host URI, reputation
2. **Status Endpoint Query**: Active leases, resource availability, cluster health
3. **Health Scoring**: Multi-factor scoring algorithm
4. **Caching**: TTL-based cache to reduce redundant queries

### Selection Algorithm
- **Multi-criteria scoring**: Price, reliability, performance, geographic
- **Configurable weights**: Adjust importance of each factor
- **Priority bonuses**: Boost scores based on deployment priorities
- **Detailed reasoning**: Human-readable selection explanations

