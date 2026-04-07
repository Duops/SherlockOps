# SherlockOps

AI-powered alert analysis service. Receives alerts from monitoring systems, analyzes them with LLM + infrastructure tools, and delivers diagnostics to Slack, Telegram, or MS Teams.

## How it works

```
Alertmanager/Grafana/Zabbix/Datadog
         │
         │ POST /webhook/alertmanager
         │ Headers: X-Environment: prod
         │          X-Channel-Telegram: 437775679
         ▼
   ┌──────────────────┐
   │  SherlockOps     │
   │                  │
   │  Phase 1 (<100ms)│──→ Post alert to Slack/Telegram/Teams
   │                  │
   │  Phase 2 (5-30s) │──→ LLM analysis with tool calls:
   │   │ Prometheus   │    - Query metrics
   │   │ Loki         │    - Check logs
   │   │ Kubernetes   │    - Get pod status
   │   │ MCP servers  │    - Any MCP tool
   │                  │
   │  Deliver result  │──→ Slack: thread reply
   │                  │    Telegram: edit message
   │                  │    Teams: update card
   └──────────────────┘
```

## Quick Start

```bash
git clone https://github.com/your-org/sherlockops.git
cd sherlockops

# 1. Configure secrets
cp .env.example .env
# Edit .env — add your LLM API key and messenger tokens

# 2. Configure settings
cp config/config.example.yaml config.yaml
# Edit config.yaml — enable messengers, tools, environments

# 3. Run
docker compose up -d --build

# 4. Test
curl -X POST http://localhost:8080/webhook/alertmanager \
  -H "Content-Type: application/json" \
  -H "X-Channel-Telegram: YOUR_CHAT_ID" \
  -d '{
    "status": "firing",
    "alerts": [{
      "status": "firing",
      "labels": {"alertname": "HighCPU", "severity": "warning"},
      "annotations": {"summary": "CPU usage > 90%"},
      "startsAt": "2026-03-31T00:00:00Z",
      "fingerprint": "test123"
    }]
  }'
```

## Features

### Alert Sources (Webhook Receivers)
| Source | Endpoint | Format |
|--------|----------|--------|
| Alertmanager | `POST /webhook/alertmanager` | Alertmanager v4 |
| Grafana | `POST /webhook/grafana` | Grafana Alerting |
| Zabbix | `POST /webhook/zabbix` | Zabbix media type |
| Datadog | `POST /webhook/datadog` | Datadog webhook |
| ELK | `POST /webhook/elk` | ElastAlert / Watcher |
| Loki | `POST /webhook/loki` | Loki ruler (AM format) |
| Generic | `POST /webhook/generic` | Any JSON |

### LLM Providers
| Provider | Config `provider` | Notes |
|----------|------------------|-------|
| Anthropic Claude | `claude` | claude-sonnet-4-6, claude-haiku, claude-opus |
| OpenAI | `openai` | gpt-4o, gpt-4o-mini |
| Ollama | `openai-compatible` | Set `base_url: http://localhost:11434/v1` |
| vLLM | `openai-compatible` | Set `base_url: http://gpu:8000/v1` |
| Azure OpenAI | `openai-compatible` | Set `base_url` to your deployment |

### Built-in Tools (LLM can use during analysis)
| Tool | What it does |
|------|-------------|
| Prometheus / VictoriaMetrics | PromQL / MetricsQL queries |
| Loki | LogQL log queries |
| Kubernetes | Pod status, logs, events |
| VMware vSphere | VMs, hosts, datastores |
| AWS CloudWatch | Metrics, alarms, logs, EC2 |
| GCP Monitoring | MQL queries, alerts, logging, compute |
| Azure Monitor | Metrics, alerts, log analytics, VMs |
| PostgreSQL | Active queries, locks, replication, slow queries |
| MongoDB | serverStatus, currentOp, rs.status, stats |
| Yandex Cloud | Compute, Monitoring, Managed DBs (PG/MySQL/Redis/Mongo/CH/Kafka), VPC |
| DigitalOcean | Droplets, Monitoring, DOKS, Managed DBs, Alerts |
| MCP Servers | Any MCP-compatible server |

### Messengers
| Messenger | Modes | Phase 2 behavior |
|-----------|-------|-------------------|
| Slack | Webhook + Socket Mode listener | Thread reply |
| Telegram | Webhook + Long-poll listener | Edit message |
| MS Teams | Incoming Webhook + Bot Framework | Update card |

## Configuration

### Environment Variables (.env)
```bash
# Required
LLM_API_KEY=sk-ant-api03-xxxxx           # LLM provider API key

# Slack (optional — omit what you don't need)
SLACK_BOT_TOKEN=xoxb-xxxxx               # Required for Slack: post messages, reply in threads
SLACK_APP_TOKEN=xapp-xxxxx               # Optional: enables listener mode (Socket Mode)
SLACK_SIGNING_SECRET=xxxxx               # Optional: webhook signature verification

# Telegram (optional)
TELEGRAM_BOT_TOKEN=123456:ABC-xxxxx      # Telegram bot token

# MS Teams (optional)
TEAMS_WEBHOOK_URL=https://...            # Simple mode: incoming webhook URL
TEAMS_CLIENT_ID=xxxxx                    # Bot Framework mode: Azure AD app
TEAMS_CLIENT_SECRET=xxxxx                # Bot Framework mode: Azure AD secret
```

**Slack modes:**
- Only `SLACK_BOT_TOKEN` → webhook mode (posts alerts, replies in threads)
- `SLACK_BOT_TOKEN` + `SLACK_APP_TOKEN` → webhook + listener mode (also watches channels for alerts)

### Webhook Headers (set in Alertmanager config)
| Header | Purpose | Example |
|--------|---------|---------|
| `X-Environment` | Select tool set | `prod`, `dev`, `staging` |
| `X-Channel-Slack` | Target Slack channel | `#prod-alerts` |
| `X-Channel-Telegram` | Target Telegram chat | `437775679` |
| `X-Channel-Teams` | Target Teams channel | `channel-id` |

### Alertmanager Integration

```yaml
# alertmanager.yml
receivers:
  - name: 'prod-alerts'
    webhook_configs:
      - url: 'http://sherlockops:8080/webhook/alertmanager'
        send_resolved: true
        http_config:
          http_headers:
            - name: X-Environment
              values: [ "prod" ]
            - name: X-Channel-Slack
              values: [ "#prod-alerts" ]
            - name: X-Channel-Telegram
              values: [ "437775679" ]

  - name: 'dev-alerts'
    webhook_configs:
      - url: 'http://sherlockops:8080/webhook/alertmanager'
        http_config:
          http_headers:
            - name: X-Environment
              values: [ "dev" ]
            - name: X-Channel-Slack
              values: [ "#dev-alerts" ]
```

### Multi-Environment Setup

Each environment gets its own Prometheus, Loki, K8s, and MCP connections:

```yaml
# config.yaml
tools:
  prometheus:
    enabled: true
    url: "http://prometheus-default:9090"

environments:
  prod:
    tools:
      victoriametrics:
        enabled: true
        url: "https://vmetrics.prod.example.com"
      loki:
        enabled: true
        url: "https://loki.prod.example.com"
      kubernetes:
        enabled: true
        kubeconfig: "/kubeconfigs/prod"
    mcp:
      clients:
        - name: "k8s-prod"
          url: "https://k8s-mcp.prod.example.com/mcp"
    llm:
      system_prompt: "You are a DevOps agent for PROD..."

  dev:
    tools:
      victoriametrics:
        enabled: true
        url: "https://vmetrics.dev.example.com"
```

### Runbooks (Knowledge Base)

Create Markdown files with YAML frontmatter in the runbooks directory:

```markdown
---
title: "High CPU Usage"
alerts:
  - "HighCPU*"
  - "CPUThrottling"
labels:
  severity: critical
priority: 10
---

## Investigation Steps
1. Check top/htop on the affected node
2. Look at recent deployments
3. Check for memory leaks causing GC pressure

## Remediation
- Scale horizontally if possible
- Restart the affected pod
```

Enable in config:
```yaml
runbooks:
  enabled: true
  dir: "/data/runbooks"
```

## Endpoints

| Endpoint | Description |
|----------|-------------|
| `POST /webhook/{source}` | Receive alerts |
| `GET /ui` | Web dashboard |
| `GET /health` | Basic health check |
| `GET /health/live` | Liveness probe |
| `GET /health/ready` | Readiness probe (checks dependencies) |
| `GET /metrics` | Prometheus metrics |

## Development

```bash
cd src

# Build
go build ./...

# Test
go test ./...

# Run locally
go run ./cmd/sherlockops -config ../config.yaml
```

## Architecture

```
src/
├── cmd/sherlockops/main.go       # Entry point, DI, graceful shutdown
├── internal/
│   ├── domain/                      # Alert model, interfaces
│   ├── config/                      # YAML config + env overrides
│   ├── receiver/                    # 7 webhook receivers + HTTP router
│   ├── pipeline/                    # Two-phase pipeline + worker pool
│   ├── cache/                       # SQLite cache with TTL
│   ├── analyzer/                    # LLM agent + env routing
│   │   ├── llm/                     # Anthropic + OpenAI providers
│   │   └── tools/                   # Tool registry, MCP client, built-in tools
│   ├── messenger/                   # Slack, Telegram, Teams
│   ├── runbook/                     # Markdown knowledge base
│   ├── webui/                       # Embedded web dashboard
│   ├── middleware/                   # Recovery, request ID
│   ├── health/                      # Liveness + readiness checks
│   └── metrics/                     # Prometheus metrics
```

## License

MIT
