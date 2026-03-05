package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type LokiConfig struct {
	URL          string `mapstructure:"url"`
	Query        string `mapstructure:"query"`
	QueryLimit   int    `mapstructure:"query_limit"`
	Namespaces   string `mapstructure:"namespaces"`
	Services     string `mapstructure:"services"`
	ServiceLabel string `mapstructure:"service_label"`
}

func (l LokiConfig) BuildQuery() string {
	if l.Query != "" {
		return l.Query
	}

	var matchers []string

	if l.Namespaces != "" {
		parts := splitAndTrim(l.Namespaces)
		if len(parts) == 1 {
			matchers = append(matchers, fmt.Sprintf(`namespace="%s"`, parts[0]))
		} else {
			matchers = append(matchers, fmt.Sprintf(`namespace=~"%s"`, strings.Join(parts, "|")))
		}
	} else {
		matchers = append(matchers, `namespace=~".+"`)
	}

	if l.Services != "" {
		label := l.ServiceLabel
		parts := splitAndTrim(l.Services)
		if len(parts) == 1 {
			matchers = append(matchers, fmt.Sprintf(`%s="%s"`, label, parts[0]))
		} else {
			matchers = append(matchers, fmt.Sprintf(`%s=~"%s"`, label, strings.Join(parts, "|")))
		}
	}

	return fmt.Sprintf(`{%s}`, strings.Join(matchers, ", "))
}

func splitAndTrim(s string) []string {
	raw := strings.Split(s, ",")
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if t := strings.TrimSpace(v); t != "" {
			out = append(out, t)
		}
	}
	return out
}

type AnthropicConfig struct {
	APIKey        string `mapstructure:"api_key"`
	Model         string `mapstructure:"model"`
	ContextWindow int    `mapstructure:"context_window"`
}

func (a AnthropicConfig) MaxChunkChars() int {
	// Log data tokenizes at ~2.3 chars/token due to timestamps, JSON, labels.
	// Reserve 10K tokens for system prompt + output (max_tokens=8192).
	const charsPerToken = 2.3
	usableTokens := a.ContextWindow - 10_000
	return int(float64(usableTokens) * charsPerToken)
}

type SlackConfig struct {
	WebhookURL string `mapstructure:"webhook_url"`
}

type AnalysisConfig struct {
	Period time.Duration `mapstructure:"period"`
}

type Config struct {
	Loki       LokiConfig      `mapstructure:"loki"`
	Anthropic  AnthropicConfig `mapstructure:"anthropic"`
	Slack      SlackConfig     `mapstructure:"slack"`
	Analysis   AnalysisConfig  `mapstructure:"analysis"`
	LogLevel   string          `mapstructure:"log_level"`
	PromptFile string          `mapstructure:"prompt_file"`
	CacheFile  string          `mapstructure:"cache_file"`
}

func Load() (*Config, error) {
	v := viper.New()

	v.SetEnvPrefix("LOKI_ANALYZER")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	v.SetDefault("loki.url", "http://loki-gateway.system.svc.cluster.local:80")
	v.SetDefault("loki.query", "")
	v.SetDefault("loki.query_limit", 5000)
	v.SetDefault("loki.namespaces", "")
	v.SetDefault("loki.services", "")
	v.SetDefault("loki.service_label", "service_name")
	v.SetDefault("anthropic.api_key", "")
	v.SetDefault("anthropic.model", "claude-opus-4-6")
	v.SetDefault("anthropic.context_window", 200_000)
	v.SetDefault("analysis.period", "6h")
	v.SetDefault("slack.webhook_url", "")
	v.SetDefault("log_level", "info")
	v.SetDefault("prompt_file", "/etc/analyzer/prompts/system.md")
	v.SetDefault("cache_file", "/tmp/loki-analyzer-logs.txt")

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	if cfg.Anthropic.APIKey == "" {
		return nil, fmt.Errorf("LOKI_ANALYZER_ANTHROPIC_API_KEY is required")
	}
	if cfg.Slack.WebhookURL == "" {
		return nil, fmt.Errorf("LOKI_ANALYZER_SLACK_WEBHOOK_URL is required")
	}

	if cfg.Analysis.Period == 0 {
		raw := v.GetString("analysis.period")
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("parse analysis.period %q: %w", raw, err)
		}
		cfg.Analysis.Period = d
	}

	return &cfg, nil
}
