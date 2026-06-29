package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"crux-agent-runtime/agent"
)

const webFetchSchema = `{
	"type": "object",
	"properties": {
		"url": { "type": "string", "description": "The URL to fetch. Must be a valid HTTP(S) URL." },
		"max_length": { "type": "integer", "description": "Maximum number of characters to return (default 10000)." }
	},
	"required": ["url"]
}`

const (
	webFetchTimeout   = 30 * time.Second
	webFetchUserAgent = "CruxAgent/1.0"
	webFetchDefaultMax = 10000
)

// WebFetch returns the web_fetch tool.
func WebFetch() agent.AgentTool {
	return agent.AgentTool{
		Name:        "web_fetch",
		Description: "Fetch a URL and return its content as plain text. Strips HTML tags and truncates long pages. Use this when you need to read documentation, articles, or any web page.",
		Parameters:  mustSchema(webFetchSchema),
		Execute:     executeWebFetch,
	}
}

type webFetchArgs struct {
	URL      string `json:"url"`
	MaxLength int   `json:"max_length"`
}

func executeWebFetch(ctx context.Context, toolCallID string, params json.RawMessage, onUpdate func(json.RawMessage)) (agent.AgentToolResult, error) {
	var args webFetchArgs
	if err := json.Unmarshal(params, &args); err != nil {
		return errResult("invalid arguments: " + err.Error()), nil
	}
	if args.URL == "" {
		return errResult("url is required"), nil
	}

	maxLen := args.MaxLength
	if maxLen <= 0 {
		maxLen = webFetchDefaultMax
	}

	ctx, cancel := context.WithTimeout(ctx, webFetchTimeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, args.URL, nil)
	if err != nil {
		return errResult(fmt.Sprintf("web_fetch: %v", err)), nil
	}
	httpReq.Header.Set("User-Agent", webFetchUserAgent)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return errResult(fmt.Sprintf("web_fetch: HTTP request failed: %v", err)), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return errResult(fmt.Sprintf("web_fetch: HTTP %d %s", resp.StatusCode, resp.Status)), nil
	}

	// Read up to 3x maxLen because HTML is verbose and stripping tags
	// shrinks the output substantially.
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxLen*3)))
	if err != nil {
		return errResult(fmt.Sprintf("web_fetch: read error: %v", err)), nil
	}

	text := stripHTML(string(body))
	text = truncateWebFetch(text, maxLen)

	details := map[string]any{
		"url":       args.URL,
		"bytes":     len(body),
		"chars":     len(text),
		"truncated": len(text) > maxLen,
	}
	detailJSON, _ := json.Marshal(details)

	return agent.AgentToolResult{
		Content: textBlock(text),
		Details: detailJSON,
	}, nil
}

// truncateWebFetch caps text at maxLen with a visible marker.
func truncateWebFetch(text string, maxLen int) string {
	if maxLen <= 0 || len(text) <= maxLen {
		return text
	}
	return text[:maxLen] + "\n[...truncated]"
}

var htmlTagRe = regexp.MustCompile(`<[^>]*>`)

// stripHTML removes script/style blocks, drops remaining HTML tags, and
// collapses whitespace.
func stripHTML(html string) string {
	scriptRe := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	html = scriptRe.ReplaceAllString(html, "")
	styleRe := regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	html = styleRe.ReplaceAllString(html, "")

	text := htmlTagRe.ReplaceAllString(html, " ")

	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", "\"")
	text = strings.ReplaceAll(text, "&#39;", "'")
	text = strings.ReplaceAll(text, "&nbsp;", " ")

	spaceRe := regexp.MustCompile(`[ \t]+`)
	text = spaceRe.ReplaceAllString(text, " ")
	nlRe := regexp.MustCompile(`\n{3,}`)
	text = nlRe.ReplaceAllString(text, "\n\n")

	return strings.TrimSpace(text)
}
