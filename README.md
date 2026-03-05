# Loki AI Log Analyzer

A Kubernetes CronJob that periodically fetches logs from [Grafana Loki](https://grafana.com/oss/loki/), sends them to [Anthropic Claude](https://www.anthropic.com/) for SRE-focused analysis, and reports findings to a Slack channel.

## How It Works

```
K8s CronJob (2x/day) → Fetch logs from Loki → Analyze with Claude → Report to Slack
```

1. On each scheduled run, the app queries Loki for all logs from the configured time period (default: last 12 hours).
2. Logs are chunked if they exceed 80% of the configured context window (default: 1M tokens / ~3.2M characters per chunk).
3. Each chunk is sent to Anthropic Claude with an SRE-focused system prompt that looks for error patterns, anomalies, performance degradation, security concerns, and improvement opportunities.
4. If multiple chunks are analyzed, results are aggregated into a single deduplicated report.
5. If findings are detected, a formatted report is posted to Slack via incoming webhook. If nothing notable is found, the job exits silently.

## Configuration

All configuration is via environment variables with the `LOKI_ANALYZER_` prefix. When deployed via the Helm chart, these are set through `values.yaml`.

| Variable | Default | Description |
|---|---|---|
| `LOKI_ANALYZER_LOKI_URL` | `http://loki-gateway.system.svc.cluster.local:80` | Loki HTTP API base URL |
| `LOKI_ANALYZER_LOKI_SERVICES` | *(empty -- all)* | Comma-separated list of services to watch |
| `LOKI_ANALYZER_LOKI_SERVICE_LABEL` | `service_name` | Loki label used to match services |
| `LOKI_ANALYZER_LOKI_NAMESPACES` | *(empty -- all)* | Comma-separated list of namespaces to watch |
| `LOKI_ANALYZER_LOKI_QUERY` | *(empty -- built from filters)* | Raw LogQL override (takes precedence over filters if set) |
| `LOKI_ANALYZER_LOKI_QUERY_LIMIT` | `5000` | Max log entries per Loki request |
| `LOKI_ANALYZER_ANALYSIS_PERIOD` | `6h` | Time window to fetch logs for |
| `LOKI_ANALYZER_ANTHROPIC_API_KEY` | *(required)* | Anthropic API key |
| `LOKI_ANALYZER_ANTHROPIC_MODEL` | `claude-opus-4-6` | Anthropic model to use |
| `LOKI_ANALYZER_ANTHROPIC_CONTEXT_WINDOW` | `200000` | Context window in tokens. Set to `1000000` to enable 1M beta (requires tier 4) |
| `LOKI_ANALYZER_PROMPT_FILE` | `/etc/analyzer/prompts/system.md` | Path to the system prompt markdown file |
| `LOKI_ANALYZER_CACHE_FILE` | `/tmp/loki-analyzer-logs.txt` | Path to cache fetched logs (survives retries, cleaned up on success) |
| `LOKI_ANALYZER_SLACK_WEBHOOK_URL` | *(required)* | Slack incoming webhook URL |
| `LOKI_ANALYZER_LOG_LEVEL` | `info` | Log level (`debug`, `info`, `warn`, `error`) |

## Deployment

### Helm

```bash
helm install loki-ai-analyzer ./chart \
  --set secrets.anthropicAPIKey="sk-ant-..." \
  --set secrets.slackWebhookURL="https://hooks.slack.com/services/..."
```

Override any default in `chart/values.yaml`:

```bash
helm install loki-ai-analyzer ./chart \
  --set schedule="0 6,18 * * *" \
  --set config.lokiServices="api-gateway,payment-service" \
  --set config.lokiNamespaces="production" \
  --set config.analysisPeriod="6h" \
  --set secrets.anthropicAPIKey="sk-ant-..." \
  --set secrets.slackWebhookURL="https://hooks.slack.com/services/..."
```

Use an existing secret (e.g. created by ExternalSecrets) instead of having the chart manage one:

```bash
helm install loki-ai-analyzer ./chart \
  --set secrets.existingSecret="my-external-secret" \
  --set secrets.anthropicAPIKeyKey="my-anthropic-key" \
  --set secrets.slackWebhookURLKey="my-slack-url"
```

### Docker

```bash
docker build -t loki-ai-analyzer .

docker run --rm \
  -e LOKI_ANALYZER_LOKI_URL="http://loki:3100" \
  -e LOKI_ANALYZER_ANTHROPIC_API_KEY="sk-ant-..." \
  -e LOKI_ANALYZER_SLACK_WEBHOOK_URL="https://hooks.slack.com/services/..." \
  loki-ai-analyzer
```

### Local Development

Port-forward Loki and run the analyzer locally:

```bash
kubectl port-forward -n system svc/loki-gateway 3100:80
```

```bash
export $(grep -v '^#' .env | xargs) && go run ./cmd/analyzer
```

Example `.env`:

```bash
LOKI_ANALYZER_LOKI_URL=http://localhost:3100
LOKI_ANALYZER_ANTHROPIC_API_KEY=sk-ant-...
LOKI_ANALYZER_SLACK_WEBHOOK_URL=https://hooks.slack.com/services/...
LOKI_ANALYZER_ANALYSIS_PERIOD=15m
LOKI_ANALYZER_LOG_LEVEL=debug
LOKI_ANALYZER_PROMPT_FILE=prompts/system.md
```

## Project Structure

```
cmd/analyzer/main.go          Entrypoint and orchestration
internal/
  config/config.go            Viper-based configuration loading
  loki/client.go              Loki HTTP API client with pagination
  analyzer/analyzer.go        Anthropic SDK integration with chunking
  notifier/notifier.go        Slack webhook notifier with Block Kit
prompts/
  system.md                   Editable SRE analysis system prompt
Dockerfile                    Multi-stage build
chart/                        Helm chart
  Chart.yaml
  values.yaml
  templates/
    cronjob.yaml
    secret.yaml
    configmap.yaml
    serviceaccount.yaml
    _helpers.tpl
```

## Dependencies

- [spf13/viper](https://github.com/spf13/viper) -- configuration management
- [rs/zerolog](https://github.com/rs/zerolog) -- structured JSON logging
- [slack-go/slack](https://github.com/slack-go/slack) -- Slack API client
- [anthropics/anthropic-sdk-go](https://github.com/anthropics/anthropic-sdk-go) -- official Anthropic Go SDK

## License

MIT
