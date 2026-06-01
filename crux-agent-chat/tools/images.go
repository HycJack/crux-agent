package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"crux-ai/core"
	"crux-agent-runtime/agent"
)

// maxImageBytes is the largest image file the agent will read into memory.
// The encoded payload (base64) sent over the wire will be ~33% larger.
const maxImageBytes = 8 * 1024 * 1024 // 8 MiB

// supportedImageExts is the set of file extensions we recognize as images.
var supportedImageExts = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
}

// IsImagePath reports whether path refers to a supported image file.
func IsImagePath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	_, ok := supportedImageExts[ext]
	return ok
}

// ReadImageFile reads an image from disk and returns the (mime, base64) pair
// ready to be embedded in a core.ImageContent block.
func ReadImageFile(path string) (mime, b64 string, err error) {
	ext := strings.ToLower(filepath.Ext(path))
	mime, ok := supportedImageExts[ext]
	if !ok {
		return "", "", fmt.Errorf("unsupported image format %q (supported: jpg, jpeg, png, gif, webp)", ext)
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", "", err
	}
	if info.Size() > maxImageBytes {
		return "", "", fmt.Errorf("image too large: %d bytes (max %d)", info.Size(), maxImageBytes)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	return mime, base64.StdEncoding.EncodeToString(data), nil
}

// ReadImageTool lets the LLM ask to see an image from the local filesystem.
// The returned base64 payload is wrapped in a core.ImageContent block so the
// next model turn can use it as visual context.
var ReadImageTool = ToolDef{
	Name:        "read_image",
	Description: "Read a local image file (jpg, jpeg, png, gif, webp) and return its base64-encoded contents as a multimodal image attachment. Use this when the user references a file path or when you need to look at an image.",
	Parameters: json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Absolute or relative path to the image file."
			}
		},
		"required": ["path"]
	}`),
	Execute: executeReadImage,
}

func executeReadImage(_ context.Context, _ string, params json.RawMessage, _ func(json.RawMessage)) (agent.AgentToolResult, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return toolError("invalid parameters: " + err.Error()), nil
	}
	if args.Path == "" {
		return toolError("path is required"), nil
	}

	mime, b64, err := ReadImageFile(args.Path)
	if err != nil {
		return toolError(fmt.Sprintf("failed to read image %s: %v", args.Path, err)), nil
	}

	// We return the base64 string as text so the LLM gets a confirmation
	// message, but the more important payload is in Details: the next
	// caller that knows how to inspect AgentToolResult can lift it back
	// into an ImageContent block. This keeps the wire format simple
	// while still preserving the bytes for inspection.
	return agent.AgentToolResult{
		Content: []core.ContentBlock{core.TextContent{
			Type: "text",
			Text: fmt.Sprintf("Loaded image %s (%s, %d bytes, base64 length %d). Use it as multimodal context for the next reasoning step.",
				args.Path, mime, len(b64)*3/4, len(b64)),
		}},
		Details: json.RawMessage(fmt.Sprintf(
			`{"path":%q,"mime":%q,"data":%q}`,
			args.Path, mime, b64,
		)),
	}, nil
}
