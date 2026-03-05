package analyzer

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/rs/zerolog"
)

type Analyzer struct {
	client        *anthropic.Client
	model         string
	systemPrompt  string
	maxChunkChars int
	betaContext   bool
	logger        zerolog.Logger
}

func New(apiKey, model, promptFile string, maxChunkChars, contextWindow int, logger zerolog.Logger) (*Analyzer, error) {
	prompt, err := os.ReadFile(promptFile)
	if err != nil {
		return nil, fmt.Errorf("read prompt file %s: %w", promptFile, err)
	}

	beta := contextWindow > 200_000
	if beta {
		logger.Info().Int("context_window", contextWindow).Msg("using 1M beta context window")
	}

	client := anthropic.NewClient(
		option.WithAPIKey(apiKey),
	)
	return &Analyzer{
		client:        &client,
		model:         model,
		systemPrompt:  strings.TrimSpace(string(prompt)),
		maxChunkChars: maxChunkChars,
		betaContext:   beta,
		logger:        logger.With().Str("component", "analyzer").Logger(),
	}, nil
}

func (a *Analyzer) Analyze(ctx context.Context, logs string) (string, error) {
	chunks := a.chunkLogs(logs)
	a.logger.Info().Int("chunks", len(chunks)).Int("total_chars", len(logs)).Msg("starting analysis")

	if len(chunks) == 1 {
		return a.analyzeChunk(ctx, chunks[0])
	}

	var chunkResults []string
	for i, chunk := range chunks {
		a.logger.Info().Int("chunk", i+1).Int("of", len(chunks)).Int("chars", len(chunk)).Msg("analyzing chunk")

		result, err := a.analyzeChunk(ctx, chunk)
		if err != nil {
			return "", fmt.Errorf("analyze chunk %d/%d: %w", i+1, len(chunks), err)
		}

		if result != "NO_FINDINGS" {
			chunkResults = append(chunkResults, result)
		}
	}

	if len(chunkResults) == 0 {
		return "NO_FINDINGS", nil
	}

	return a.aggregateResults(ctx, chunkResults)
}

func (a *Analyzer) analyzeChunk(ctx context.Context, logChunk string) (string, error) {
	result, err := a.sendMessage(ctx, logChunk)
	if err != nil {
		return "", fmt.Errorf("anthropic API call: %w", err)
	}
	return result, nil
}

func (a *Analyzer) aggregateResults(ctx context.Context, chunkResults []string) (string, error) {
	combined := strings.Join(chunkResults, "\n\n---\n\n")
	aggregationPrompt := fmt.Sprintf(
		"Below are analysis results from multiple log chunks. "+
			"Consolidate them into a single, deduplicated report. "+
			"Merge duplicate findings, keep the highest severity, and produce a unified list.\n\n%s",
		combined,
	)

	a.logger.Info().Int("partial_results", len(chunkResults)).Msg("aggregating chunk results")

	result, err := a.sendMessage(ctx, aggregationPrompt)
	if err != nil {
		return "", fmt.Errorf("anthropic aggregation call: %w", err)
	}
	return result, nil
}

func (a *Analyzer) sendMessage(ctx context.Context, userContent string) (string, error) {
	if a.betaContext {
		return a.sendBetaMessage(ctx, userContent)
	}

	message, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: 8192,
		System: []anthropic.TextBlockParam{
			{Text: a.systemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(
				anthropic.NewTextBlock(userContent),
			),
		},
	})
	if err != nil {
		return "", err
	}
	return extractText(message), nil
}

func (a *Analyzer) sendBetaMessage(ctx context.Context, userContent string) (string, error) {
	message, err := a.client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: 8192,
		System: []anthropic.BetaTextBlockParam{
			{Text: a.systemPrompt},
		},
		Messages: []anthropic.BetaMessageParam{
			anthropic.NewBetaUserMessage(
				anthropic.NewBetaTextBlock(userContent),
			),
		},
		Betas: []anthropic.AnthropicBeta{
			"context-1m-2025-08-07",
		},
	})
	if err != nil {
		return "", err
	}
	return extractBetaText(message), nil
}

func (a *Analyzer) chunkLogs(logs string) []string {
	if len(logs) <= a.maxChunkChars {
		return []string{logs}
	}

	var chunks []string
	lines := strings.Split(logs, "\n")
	var current strings.Builder

	for _, line := range lines {
		if current.Len()+len(line)+1 > a.maxChunkChars && current.Len() > 0 {
			chunks = append(chunks, current.String())
			current.Reset()
		}
		current.WriteString(line)
		current.WriteByte('\n')
	}

	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}

	return chunks
}

func extractText(msg *anthropic.Message) string {
	var parts []string
	for _, block := range msg.Content {
		switch v := block.AsAny().(type) {
		case anthropic.TextBlock:
			parts = append(parts, v.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func extractBetaText(msg *anthropic.BetaMessage) string {
	var parts []string
	for _, block := range msg.Content {
		switch v := block.AsAny().(type) {
		case anthropic.BetaTextBlock:
			parts = append(parts, v.Text)
		}
	}
	return strings.Join(parts, "\n")
}
