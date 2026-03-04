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
	logger        zerolog.Logger
}

func New(apiKey, model, promptFile string, maxChunkChars int, logger zerolog.Logger) (*Analyzer, error) {
	prompt, err := os.ReadFile(promptFile)
	if err != nil {
		return nil, fmt.Errorf("read prompt file %s: %w", promptFile, err)
	}

	client := anthropic.NewClient(
		option.WithAPIKey(apiKey),
	)
	return &Analyzer{
		client:        &client,
		model:         model,
		systemPrompt:  strings.TrimSpace(string(prompt)),
		maxChunkChars: maxChunkChars,
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
	message, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: 4096,
		System: []anthropic.TextBlockParam{
			{Text: a.systemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(
				anthropic.NewTextBlock(logChunk),
			),
		},
	})
	if err != nil {
		return "", fmt.Errorf("anthropic API call: %w", err)
	}

	return extractText(message), nil
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

	message, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: 4096,
		System: []anthropic.TextBlockParam{
			{Text: a.systemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(
				anthropic.NewTextBlock(aggregationPrompt),
			),
		},
	})
	if err != nil {
		return "", fmt.Errorf("anthropic aggregation call: %w", err)
	}

	return extractText(message), nil
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
