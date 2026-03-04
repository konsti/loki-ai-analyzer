package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type LokiConfig struct {
	URL        string `mapstructure:"url"`
	Query      string `mapstructure:"query"`
	QueryLimit int    `mapstructure:"query_limit"`
}

type AnthropicConfig struct {
	APIKey        string `mapstructure:"api_key"`
	Model         string `mapstructure:"model"`
	ContextWindow int    `mapstructure:"context_window"`
}

func (a AnthropicConfig) MaxChunkChars() int {
	const charsPerToken = 4
	usableTokens := int(float64(a.ContextWindow) * 0.80)
	return usableTokens * charsPerToken
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
}

func Load() (*Config, error) {
	v := viper.New()

	v.SetEnvPrefix("LOKI_ANALYZER")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	v.SetDefault("loki.url", "http://loki-gateway.monitoring.svc.cluster.local:80")
	v.SetDefault("loki.query", `{namespace=~".+"}`)
	v.SetDefault("loki.query_limit", 5000)
	v.SetDefault("anthropic.model", "claude-opus-4-20250514")
	v.SetDefault("anthropic.context_window", 1_000_000)
	v.SetDefault("analysis.period", "12h")
	v.SetDefault("log_level", "info")
	v.SetDefault("prompt_file", "/etc/analyzer/prompts/system.md")

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
