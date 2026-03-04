package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"

	"github.com/unique/loki-ai-analyzer/internal/analyzer"
	"github.com/unique/loki-ai-analyzer/internal/config"
	"github.com/unique/loki-ai-analyzer/internal/loki"
	"github.com/unique/loki-ai-analyzer/internal/notifier"
)

func main() {
	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()

	cfg, err := config.Load()
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to load configuration")
	}

	level, err := zerolog.ParseLevel(cfg.LogLevel)
	if err != nil {
		level = zerolog.InfoLevel
	}
	logger = logger.Level(level)

	logger.Info().
		Str("loki_url", cfg.Loki.URL).
		Str("model", cfg.Anthropic.Model).
		Dur("analysis_period", cfg.Analysis.Period).
		Msg("starting loki log analyzer")

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, cfg, logger); err != nil {
		logger.Fatal().Err(err).Msg("analyzer run failed")
	}

	logger.Info().Msg("analysis complete")
}

func run(ctx context.Context, cfg *config.Config, logger zerolog.Logger) error {
	end := time.Now()
	start := end.Add(-cfg.Analysis.Period)

	lokiClient := loki.NewClient(cfg.Loki.URL, logger)
	entries, err := lokiClient.QueryRange(ctx, cfg.Loki.Query, start, end, cfg.Loki.QueryLimit)
	if err != nil {
		return err
	}

	if len(entries) == 0 {
		logger.Info().Msg("no logs returned from loki, nothing to analyze")
		return nil
	}

	logger.Info().Int("log_entries", len(entries)).Msg("fetched logs from loki")

	formattedLogs := loki.FormatLogs(entries)

	ai, err := analyzer.New(
		cfg.Anthropic.APIKey,
		cfg.Anthropic.Model,
		cfg.PromptFile,
		cfg.Anthropic.MaxChunkChars(),
		logger,
	)
	if err != nil {
		return err
	}

	result, err := ai.Analyze(ctx, formattedLogs)
	if err != nil {
		return err
	}

	if result == "NO_FINDINGS" {
		logger.Info().Msg("analysis found no notable issues")
		return nil
	}

	logger.Info().Msg("findings detected, sending to slack")

	n := notifier.New(cfg.Slack.WebhookURL, logger)
	return n.Send(result)
}
