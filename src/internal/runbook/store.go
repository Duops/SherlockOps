package runbook

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

// Runbook represents a single runbook document loaded from a Markdown file.
type Runbook struct {
	Name       string            // filename, e.g. "high-cpu.md"
	Title      string            // from YAML frontmatter
	AlertNames []string          // alertname glob patterns this applies to
	Labels     map[string]string // label matchers (all must match)
	Content    string            // markdown body after frontmatter
	Priority   int               // higher = preferred when multiple match
}

// frontmatter is the YAML structure parsed from the top of a runbook file.
type frontmatter struct {
	Title  string            `yaml:"title"`
	Alerts []string          `yaml:"alerts"`
	Labels map[string]string `yaml:"labels"`
	Priority int             `yaml:"priority"`
}

// Store loads and matches runbooks from a directory of Markdown files.
type Store struct {
	runbooks []Runbook
	dir      string
	logger   *slog.Logger
	mu       sync.RWMutex
}

// NewStore creates a new runbook store for the given directory.
// It does not load runbooks automatically; call Load() after creation.
func NewStore(dir string, logger *slog.Logger) (*Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("runbook directory must not be empty")
	}
	// Sanitize path to prevent directory traversal.
	dir = filepath.Clean(dir)
	if logger == nil {
		logger = slog.Default()
	}
	return &Store{
		dir:    dir,
		logger: logger,
	}, nil
}

// Load scans the directory for *.md files and parses their frontmatter.
// Previously loaded runbooks are replaced.
func (s *Store) Load() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("read runbook directory %q: %w", s.dir, err)
	}

	var loaded []Runbook
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		rb, err := s.parseFile(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			s.logger.Warn("skipping malformed runbook", "file", entry.Name(), "error", err)
			continue
		}
		rb.Name = entry.Name()
		loaded = append(loaded, rb)
	}

	s.mu.Lock()
	s.runbooks = loaded
	s.mu.Unlock()

	s.logger.Info("loaded runbooks", "count", len(loaded), "dir", s.dir)
	return nil
}

// Reload is an alias for Load — it re-reads all runbooks from disk.
func (s *Store) Reload() error {
	return s.Load()
}

// Match returns all runbooks that match the given alert, sorted by priority descending.
// If no runbooks match, an empty slice is returned.
func (s *Store) Match(alert *domain.Alert) []Runbook {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var matched []Runbook
	for _, rb := range s.runbooks {
		if s.matches(rb, alert) {
			matched = append(matched, rb)
		}
	}

	sort.Slice(matched, func(i, j int) bool {
		return matched[i].Priority > matched[j].Priority
	})

	return matched
}

// matches checks whether a single runbook applies to the given alert.
func (s *Store) matches(rb Runbook, alert *domain.Alert) bool {
	nameMatched := len(rb.AlertNames) == 0
	for _, pattern := range rb.AlertNames {
		if matchGlob(pattern, alert.Name) {
			nameMatched = true
			break
		}
	}
	if !nameMatched {
		return false
	}

	for k, v := range rb.Labels {
		if alert.Labels[k] != v {
			return false
		}
	}

	return true
}

// parseFile reads a markdown file and extracts the YAML frontmatter and body.
func (s *Store) parseFile(path string) (Runbook, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Runbook{}, fmt.Errorf("read file: %w", err)
	}

	content := string(data)
	fm, body, err := parseFrontmatter(content)
	if err != nil {
		return Runbook{}, err
	}

	return Runbook{
		Title:      fm.Title,
		AlertNames: fm.Alerts,
		Labels:     fm.Labels,
		Content:    body,
		Priority:   fm.Priority,
	}, nil
}

// parseFrontmatter splits a markdown document at the --- delimiters and parses
// the YAML between them.
func parseFrontmatter(content string) (frontmatter, string, error) {
	const delimiter = "---"

	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, delimiter) {
		return frontmatter{}, content, fmt.Errorf("no frontmatter found")
	}

	// Find the closing delimiter.
	rest := trimmed[len(delimiter):]
	idx := strings.Index(rest, "\n"+delimiter)
	if idx < 0 {
		return frontmatter{}, content, fmt.Errorf("unclosed frontmatter")
	}

	yamlBlock := rest[:idx]
	body := strings.TrimSpace(rest[idx+len("\n"+delimiter):])

	var fm frontmatter
	if err := yaml.Unmarshal([]byte(yamlBlock), &fm); err != nil {
		return frontmatter{}, content, fmt.Errorf("parse frontmatter YAML: %w", err)
	}

	return fm, body, nil
}

// matchGlob performs simple glob matching where * matches any sequence of characters.
// It uses filepath.Match semantics.
func matchGlob(pattern, name string) bool {
	matched, err := filepath.Match(pattern, name)
	if err != nil {
		return false
	}
	return matched
}

// MatchAlert implements domain.RunbookMatcher. It returns whether any runbooks
// matched the alert and the pre-formatted prompt context block.
func (s *Store) MatchAlert(alert *domain.Alert) (bool, string) {
	matched := s.Match(alert)
	if len(matched) == 0 {
		return false, ""
	}
	return true, FormatForPrompt(matched)
}

// FormatForPrompt formats a list of runbooks into a string suitable for injection
// into an LLM prompt.
func FormatForPrompt(runbooks []Runbook) string {
	if len(runbooks) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("<runbooks>\n")
	for i, rb := range runbooks {
		if i > 0 {
			b.WriteString("\n---\n\n")
		}
		if rb.Title != "" {
			b.WriteString("## ")
			b.WriteString(rb.Title)
			b.WriteString("\n\n")
		}
		b.WriteString(rb.Content)
		b.WriteString("\n")
	}
	b.WriteString("</runbooks>")
	return b.String()
}
