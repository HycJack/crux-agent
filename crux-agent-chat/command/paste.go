package command

import (
	"crux-agent-chat/tools"
	"crux-agent-chat/ui"
	"github.com/hycjack/crux-ai/core"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// HandlePaste handles the /paste command which stages images for the next turn.
func HandlePaste(input string) (result HandlerResult) {
	args := strings.Fields(input)
	if len(args) < 2 {
		ui.PrintError("Usage: /paste <image-path> [more-image-paths...]")
		return HandlerResult{Handled: true}
	}

	var pending []core.ContentBlock
	added, skipped := StageImages(args[1:], &pending)
	ui.PrintInfo("📎 Staged %d image(s)%s (will attach to the next turn)", added, SkippedMsg(skipped))

	return HandlerResult{
		Handled:      true,
		StagedBlocks: pending,
	}
}

func StageImages(paths []string, pending *[]core.ContentBlock) (added int, skipped []string) {
	for _, p := range paths {
		p = strings.Trim(p, "\"'")
		if strings.HasPrefix(p, "~") {
			if home, err := os.UserHomeDir(); err == nil {
				suffix := strings.TrimPrefix(p, "~")
				suffix = strings.TrimLeft(suffix, "/\\")
				p = filepath.Join(home, suffix)
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

func SkippedMsg(skipped []string) string {
	if len(skipped) == 0 {
		return ""
	}
	return fmt.Sprintf(" (skipped %d invalid: %s)", len(skipped), strings.Join(skipped, ", "))
}
