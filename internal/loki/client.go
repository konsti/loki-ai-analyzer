package loki

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
	logger     zerolog.Logger
}

func NewClient(baseURL string, logger zerolog.Logger) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: 2 * time.Minute},
		logger:     logger.With().Str("component", "loki").Logger(),
	}
}

type LogEntry struct {
	Timestamp time.Time
	Line      string
	Labels    map[string]string
}

type queryRangeResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string         `json:"resultType"`
		Result     []streamResult `json:"result"`
	} `json:"data"`
}

type streamResult struct {
	Stream map[string]string `json:"stream"`
	Values [][]string        `json:"values"` // [nanosecond_timestamp, log_line]
}

func (c *Client) QueryRange(ctx context.Context, query string, start, end time.Time, limit int) ([]LogEntry, error) {
	var allEntries []LogEntry
	windowStart := start

	for windowStart.Before(end) {
		entries, err := c.queryWindow(ctx, query, windowStart, end, limit)
		if err != nil {
			return nil, err
		}

		if len(entries) == 0 {
			break
		}

		allEntries = append(allEntries, entries...)

		if len(entries) < limit {
			break
		}

		// Move window past the last entry to paginate
		windowStart = entries[len(entries)-1].Timestamp.Add(1 * time.Nanosecond)
		c.logger.Debug().
			Time("next_window_start", windowStart).
			Int("fetched", len(entries)).
			Msg("paginating to next window")
	}

	sort.Slice(allEntries, func(i, j int) bool {
		return allEntries[i].Timestamp.Before(allEntries[j].Timestamp)
	})

	c.logger.Info().
		Int("total_entries", len(allEntries)).
		Time("start", start).
		Time("end", end).
		Msg("log fetch complete")

	return allEntries, nil
}

func (c *Client) queryWindow(ctx context.Context, query string, start, end time.Time, limit int) ([]LogEntry, error) {
	params := url.Values{}
	params.Set("query", query)
	params.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	params.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	params.Set("limit", strconv.Itoa(limit))
	params.Set("direction", "forward")

	reqURL := fmt.Sprintf("%s/loki/api/v1/query_range?%s", c.baseURL, params.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	c.logger.Debug().
		Str("url", reqURL).
		Msg("querying loki")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("loki request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("loki returned %d: %s", resp.StatusCode, string(body))
	}

	var qr queryRangeResponse
	if err := json.Unmarshal(body, &qr); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if qr.Status != "success" {
		return nil, fmt.Errorf("loki query status: %s", qr.Status)
	}

	var entries []LogEntry
	for _, stream := range qr.Data.Result {
		for _, val := range stream.Values {
			if len(val) < 2 {
				continue
			}
			nsec, err := strconv.ParseInt(val[0], 10, 64)
			if err != nil {
				c.logger.Warn().Str("ts", val[0]).Msg("skipping unparseable timestamp")
				continue
			}
			entries = append(entries, LogEntry{
				Timestamp: time.Unix(0, nsec),
				Line:      val[1],
				Labels:    stream.Stream,
			})
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})

	return entries, nil
}

func FormatLogs(entries []LogEntry) string {
	var sb strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&sb, "[%s] %s | %s\n",
			e.Timestamp.Format(time.RFC3339),
			formatLabels(e.Labels),
			e.Line,
		)
	}
	return sb.String()
}

func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	parts := make([]string, 0, len(labels))
	for k, v := range labels {
		parts = append(parts, k+"="+v)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}
