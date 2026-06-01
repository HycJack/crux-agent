package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	_ "crux-ai/providers"

	"crux-agent-chat/agent"
	"crux-agent-chat/config"
	"crux-agent-chat/tools"
	"crux-agent-chat/ui"

	"crux-ai/core"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		ui.PrintError("Configuration error: %v", err)
		fmt.Println("\nCreate a .env file with your API key, for example:")
		fmt.Println("  ANTHROPIC_API_KEY=sk-ant-...")
		fmt.Println("  OPENAI_API_KEY=sk-...")
		fmt.Println("  DEEPSEEK_API_KEY=sk-...")
		os.Exit(1)
	}

	ui.PrintBanner(string(cfg.Provider), cfg.ModelID)
	ui.PrintMultimodalHint()

	// Create coding agent
	a := agent.NewCodingAgent(cfg)
	a.Subscribe(ui.Subscriber())

	// Track Ctrl+C presses: first aborts current query, second exits the REPL.
	var ctrlCCount int32
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for range sigCh {
			count := atomic.AddInt32(&ctrlCCount, 1)
			if count == 1 {
				ui.PrintInfo("\n⏸  Aborting current query... (press Ctrl+C again to quit)")
				a.Abort()
			} else {
				ui.PrintInfo("\n👋 Exiting...")
				os.Exit(0)
			}
		}
	}()
	defer signal.Stop(sigCh)

	// REPL loop
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024), 1024*1024) // initial 1KB, max 1MB token

	// pendingImages are images attached to the next user message. They are
	// consumed (cleared) when the next non-empty, non-command input is sent.
	var pendingImages []core.ContentBlock

	for {
		atomic.StoreInt32(&ctrlCCount, 0)
		fmt.Print(ui.FormatUserPrompt(len(pendingImages)))

		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				ui.PrintError("Read error: %v", err)
			}
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		// Handle commands
		if strings.HasPrefix(input, "/") {
			// /paste <path> [more paths...] — stage images for the next turn.
			if strings.HasPrefix(strings.ToLower(input), "/paste") {
				args := strings.Fields(input)
				if len(args) < 2 {
					ui.PrintError("Usage: /paste <image-path> [more-image-paths...]")
					continue
				}
				added, skipped := stageImages(args[1:], &pendingImages)
				ui.PrintInfo("📎 Staged %d image(s)%s", added, skippedMsg(skipped))
				continue
			}
			if strings.HasPrefix(strings.ToLower(input), "/clearimg") {
				pendingImages = nil
				ui.PrintInfo("Cleared staged images.")
				continue
			}
			switch strings.ToLower(input) {
			case "/quit", "/exit":
				fmt.Println("Goodbye! 👋")
				return
			case "/clear":
				a = agent.NewCodingAgent(cfg)
				a.Subscribe(ui.Subscriber())
				pendingImages = nil
				ui.PrintInfo("Conversation cleared.")
				continue
			case "/help":
				ui.PrintHelp()
				continue
			case "/tools":
				ui.PrintTools()
				continue
			default:
				ui.PrintError("Unknown command: %s (type /help for help)", input)
				continue
			}
		}

		ui.PrintSeparator()

		// Auto-detect image paths in the input. Useful when the user
		// drags a file into the terminal on most platforms (the path
		// appears in the input) or pastes a path string.
		autoImages, rest := extractImagePaths(input)
		if len(autoImages) > 0 {
			added, skipped := stageImages(autoImages, &pendingImages)
			ui.PrintInfo("🖼  Detected %d image path(s)%s", added, skippedMsg(skipped))
			input = rest
			if strings.TrimSpace(input) == "" {
				// User only dragged images; prompt again so they can add text.
				ui.PrintInfo("Add a question and press Enter (or send blank to skip):")
				continue
			}
		}

		// Build the prompt content: text + any pending images.
		var content any = input
		if len(pendingImages) > 0 {
			blocks := make([]core.ContentBlock, 0, len(pendingImages)+1)
			blocks = append(blocks, core.TextContent{Type: "text", Text: input})
			blocks = append(blocks, pendingImages...)
			content = blocks
			pendingImages = nil
		}

		// Use a fresh context per query so Abort() can cancel just this one.
		queryCtx, queryCancel := context.WithTimeout(context.Background(), 10*time.Minute)
		_, err := agent.RunOnce(queryCtx, a, content)
		queryCancel()
		if err != nil {
			ui.PrintError("Agent error: %v", err)
		}

		fmt.Println()
	}
}

// stageImages tries to load each path as an image. Successfully loaded
// images are appended to *pending. It returns how many were added and
// how many were skipped (with reasons).
func stageImages(paths []string, pending *[]core.ContentBlock) (added int, skipped []string) {
	for _, p := range paths {
		p = strings.Trim(p, "\"'")
		// Expand ~ to home directory.
		if strings.HasPrefix(p, "~") {
			if home, err := os.UserHomeDir(); err == nil {
				p = filepath.Join(home, strings.TrimPrefix(p, "~"))
			}
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", p, err))
			continue
		}
		mime, b64, err := tools.ReadImageFile(abs)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", p, err))
			continue
		}
		*pending = append(*pending, core.ImageContent{
			Type:     "image",
			Data:     b64,
			MimeType: mime,
		})
		added++
	}
	return added, skipped
}

// extractImagePaths scans the input for tokens that look like image file
// paths. The remaining text is returned as the first return value minus
// those tokens. Supports quoted paths and absolute / relative paths.
func extractImagePaths(input string) (paths []string, rest string) {
	tokens := tokenize(input)
	var kept []string
	for _, tok := range tokens {
		clean := strings.Trim(tok, "\"'")
		if tools.IsImagePath(clean) && fileExists(clean) {
			paths = append(paths, tok)
		} else {
			kept = append(kept, tok)
		}
	}
	return paths, strings.Join(kept, " ")
}

// tokenize splits on whitespace but keeps quoted substrings together.
func tokenize(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQuote != 0:
			cur.WriteByte(c)
			if c == inQuote {
				out = append(out, cur.String())
				cur.Reset()
				inQuote = 0
			}
		case c == '"' || c == '\'':
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
			cur.WriteByte(c)
			inQuote = c
		case c == ' ' || c == '\t':
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func skippedMsg(skipped []string) string {
	if len(skipped) == 0 {
		return ""
	}
	return " (skipped: " + strings.Join(skipped, "; ") + ")"
}
