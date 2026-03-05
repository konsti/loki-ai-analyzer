package main

import (
	"context"
	"os"
	"os/signal"
	"strings"
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

	lokiQuery := cfg.Loki.BuildQuery()

	logger.Info().
		Str("loki_url", cfg.Loki.URL).
		Str("loki_query", lokiQuery).
		Str("filter_services", cfg.Loki.Services).
		Str("filter_namespaces", cfg.Loki.Namespaces).
		Str("model", cfg.Anthropic.Model).
		Dur("analysis_period", cfg.Analysis.Period).
		Str("cache_file", cfg.CacheFile).
		Msg("starting loki log analyzer")

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, cfg, lokiQuery, logger); err != nil {
		logger.Fatal().Err(err).Msg("analyzer run failed")
	}

	logger.Info().Msg("analysis complete")
}

func run(ctx context.Context, cfg *config.Config, lokiQuery string, logger zerolog.Logger) error {
	cacheFile := cfg.CacheFile

	if err := ensureCachedLogs(ctx, cfg, lokiQuery, cacheFile, logger); err != nil {
		return err
	}

	logs, err := os.ReadFile(cacheFile)
	if err != nil {
		return err
	}

	formattedLogs := strings.TrimSuffix(string(logs), loki.CacheCompleteMarker)
	if len(formattedLogs) == 0 {
		logger.Info().Msg("no logs returned from loki, nothing to analyze")
		os.Remove(cacheFile)
		return nil
	}

	ai, err := analyzer.New(
		cfg.Anthropic.APIKey,
		cfg.Anthropic.Model,
		cfg.PromptFile,
		cfg.Anthropic.MaxChunkChars(),
		cfg.Anthropic.ContextWindow,
		logger,
	)
	if err != nil {
		return err
	}

	result, err := ai.Analyze(ctx, formattedLogs)
	if err != nil {
		return err
	}

	os.Remove(cacheFile)

	if result == "NO_FINDINGS" {
		logger.Info().Msg("analysis found no notable issues")
		return nil
	}

	logger.Info().Msg("findings detected, sending to slack")

	n := notifier.New(cfg.Slack.WebhookURL, logger)
	return n.Send(result)
}

// ensureCachedLogs checks for a valid (complete) cache file. If missing or
// incomplete it fetches from Loki, streaming each page directly to disk.
func ensureCachedLogs(ctx context.Context, cfg *config.Config, lokiQuery, cacheFile string, logger zerolog.Logger) error {
	if isCompleteCacheFile(cacheFile) {
		info, _ := os.Stat(cacheFile)
		logger.Info().Int64("cached_bytes", info.Size()).Msg("using cached logs from previous run")
		return nil
	}

	os.Remove(cacheFile)

	f, err := os.Create(cacheFile)
	if err != nil {
		return err
	}
	defer f.Close()

	end := time.Now()
	start := end.Add(-cfg.Analysis.Period)

	lokiClient := loki.NewClient(cfg.Loki.URL, logger)
	totalEntries, err := lokiClient.FetchAndWrite(ctx, f, lokiQuery, start, end, cfg.Loki.QueryLimit)
	if err != nil {
		f.Close()
		os.Remove(cacheFile)
		return err
	}

	logger.Info().Int("log_entries", totalEntries).Msg("fetched logs from loki")

	if _, err := f.WriteString(loki.CacheCompleteMarker); err != nil {
		f.Close()
		os.Remove(cacheFile)
		return err
	}

	return nil
}

func isCompleteCacheFile(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return len(data) > 0 && strings.HasSuffix(string(data), loki.CacheCompleteMarker)
}
