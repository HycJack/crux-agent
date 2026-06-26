// Package l2 aggregates atomic L1 memories into topic-scoped "scene" files.
// A scene is a Markdown file with a META block + body, where META tracks
// created/updated/summary/heat. This mirrors the TS scene-format.ts.
package l2

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	metaStart = "-----META-START-----"
	metaEnd   = "-----META-END-----"
)

// Scene is the parsed form of a scene file.
type Scene struct {
	Filename string
	Meta     Meta
	Content  string
}

// Meta is the structured header stored between the META delimiters.
type Meta struct {
	Created string `json:"created"`  // RFC3339
	Updated string `json:"updated"`  // RFC3339
	Summary string `json:"summary"`  // one-line description
	Heat    int    `json:"heat"`     // 0-10 access frequency
}

// Store is a directory of scene files.
type Store struct {
	BaseDir string
}

// NewStore creates a scene store at baseDir/l2/scenes/.
func NewStore(baseDir string) (*Store, error) {
	p := filepath.Join(baseDir, "l2", "scenes")
	if err := os.MkdirAll(p, 0o755); err != nil {
		return nil, fmt.Errorf("l2: create dir: %w", err)
	}
	return &Store{BaseDir: p}, nil
}

// Read loads a scene by filename (without .md).
func (s *Store) Read(name string) (*Scene, error) {
	path := filepath.Join(s.BaseDir, name+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(string(raw), name+".md"), nil
}

// Write saves a scene. The filename (without .md) is taken from s.Name.
func (s *Store) Write(scene *Scene) error {
	if scene.Filename == "" {
		return fmt.Errorf("l2: empty filename")
	}
	if scene.Meta.Updated == "" {
		scene.Meta.Updated = time.Now().UTC().Format(time.RFC3339)
	}
	if scene.Meta.Created == "" {
		scene.Meta.Created = scene.Meta.Updated
	}
	path := filepath.Join(s.BaseDir, scene.Filename)
	return os.WriteFile(path, []byte(Format(scene)), 0o644)
}

// List returns all scene names (filenames without .md).
func (s *Store) List() ([]string, error) {
	entries, err := os.ReadDir(s.BaseDir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			out = append(out, strings.TrimSuffix(e.Name(), ".md"))
		}
	}
	return out, nil
}

// Parse parses a raw scene file. If META block is missing, the entire file is
// treated as content with empty meta.
func Parse(raw, filename string) *Scene {
	startIdx := strings.Index(raw, metaStart)
	endIdx := strings.Index(raw, metaEnd)
	if startIdx == -1 || endIdx == -1 || endIdx < startIdx {
		return &Scene{
			Filename: filename,
			Meta:     Meta{},
			Content:  strings.TrimSpace(raw),
		}
	}
	metaBlock := strings.TrimSpace(raw[startIdx+len(metaStart) : endIdx])
	content := strings.TrimSpace(raw[endIdx+len(metaEnd):])
	return &Scene{
		Filename: filename,
		Meta: Meta{
			Created: extractField(metaBlock, "created"),
			Updated: extractField(metaBlock, "updated"),
			Summary: extractField(metaBlock, "summary"),
			Heat:    parseInt(extractField(metaBlock, "heat")),
		},
		Content: content,
	}
}

// Format renders a scene back to its file form.
func Format(s *Scene) string {
	return fmt.Sprintf("%s\n\n%s\n%s\n%s\n%s\n\n%s\n\n%s",
		metaStart,
		"created: "+s.Meta.Created,
		"updated: "+s.Meta.Updated,
		"summary: "+s.Meta.Summary,
		"heat: "+strconv.Itoa(s.Meta.Heat),
		metaEnd,
		s.Content,
	)
}

func extractField(block, key string) string {
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, key+":") {
			return strings.TrimSpace(line[len(key)+1:])
		}
	}
	return ""
}

func parseInt(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// Heat updates a scene's heat counter atomically.
func (s *Store) Touch(name string) error {
	scene, err := s.Read(name)
	if err != nil {
		return err
	}
	scene.Meta.Updated = time.Now().UTC().Format(time.RFC3339)
	scene.Meta.Heat++
	return s.Write(scene)
}

// ReadScenes reads every scene file and returns parsed scenes.
func (s *Store) ReadScenes() ([]*Scene, error) {
	names, err := s.List()
	if err != nil {
		return nil, err
	}
	var out []*Scene
	for _, n := range names {
		sc, err := s.Read(n)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, nil
}

// BufferedRead returns the file content as a streaming-friendly reader.
func (s *Store) BufferedRead(name string) (*bufio.Reader, error) {
	f, err := os.Open(filepath.Join(s.BaseDir, name+".md"))
	if err != nil {
		return nil, err
	}
	return bufio.NewReader(f), nil
}