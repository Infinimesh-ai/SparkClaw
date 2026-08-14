package workspacefiles

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
)

type MatchMode string

const (
	MatchExact MatchMode = "exact"
	MatchFuzzy MatchMode = "fuzzy"
)

type SearchRequest struct {
	Mode       MatchMode
	Term       string
	MaxResults int
	MaxEntries int
	MaxDepth   int
	Timeout    time.Duration
	Validate   func(relPath string) error
}

type Match struct {
	RelPath string `json:"rel_path"`
	Name    string `json:"name"`
	Score   int    `json:"score"`
	Reason  string `json:"reason"`
}

type SearchResult struct {
	Matches   []Match
	Total     int
	Complete  bool
	Truncated bool
}

// Search walks one workspace and ranks filename-only matches. It never opens
// file content and never returns an absolute path.
func Search(ctx context.Context, root string, request SearchRequest) (SearchResult, error) {
	term := strings.TrimSpace(request.Term)
	if term == "" {
		return SearchResult{}, errors.New("filename search term cannot be empty")
	}
	if request.Mode != MatchExact && request.Mode != MatchFuzzy {
		return SearchResult{}, errors.New("filename search mode is invalid")
	}
	root = strings.TrimSpace(root)
	info, err := os.Stat(root)
	if err != nil {
		return SearchResult{}, err
	}
	if !info.IsDir() {
		return SearchResult{}, errors.New("filename search root is not a directory")
	}
	if request.MaxResults <= 0 {
		request.MaxResults = 20
	}
	if request.MaxEntries <= 0 {
		request.MaxEntries = 20000
	}
	if request.MaxDepth <= 0 {
		request.MaxDepth = 32
	}
	if request.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, request.Timeout)
		defer cancel()
	}

	result := SearchResult{Matches: []Match{}, Complete: true}
	entries := 0
	err = filepath.WalkDir(root, func(candidate string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			result.Complete = false
			return nil
		}
		if err := ctx.Err(); err != nil {
			result.Complete = false
			return err
		}
		entries++
		if entries > request.MaxEntries {
			result.Complete = false
			return fs.SkipAll
		}
		rel, err := filepath.Rel(root, candidate)
		if err != nil {
			result.Complete = false
			return nil
		}
		depth := strings.Count(filepath.ToSlash(rel), "/") + 1
		if entry.IsDir() {
			if candidate != root && (skipDir(entry.Name()) || depth > request.MaxDepth) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil
		}

		score, reason := matchFilename(entry.Name(), term, request.Mode)
		if score <= 0 {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == "." || filepath.IsAbs(filepath.FromSlash(rel)) || rel == ".." || strings.HasPrefix(rel, "../") {
			result.Complete = false
			return nil
		}
		if request.Validate != nil {
			if err := request.Validate(rel); err != nil {
				result.Complete = false
				return nil
			}
		}
		result.Matches = append(result.Matches, Match{RelPath: rel, Name: entry.Name(), Score: score, Reason: reason})
		return nil
	})
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return result, nil
	}
	if err != nil {
		return SearchResult{}, err
	}
	slices.SortFunc(result.Matches, func(a, b Match) int {
		if a.Score != b.Score {
			return b.Score - a.Score
		}
		return strings.Compare(a.RelPath, b.RelPath)
	})
	result.Total = len(result.Matches)
	if len(result.Matches) > request.MaxResults {
		result.Matches = result.Matches[:request.MaxResults]
		result.Truncated = true
	}
	return result, nil
}

func matchFilename(name, term string, mode MatchMode) (int, string) {
	if mode == MatchExact {
		if name == term {
			return 1000, "filename_exact"
		}
		return 0, ""
	}
	lowerName := strings.ToLower(strings.TrimSpace(name))
	lowerTerm := strings.ToLower(strings.TrimSpace(term))
	if lowerName == lowerTerm {
		return 1000, "filename_exact"
	}
	nameCompact := compact(lowerName)
	termCompact := compact(lowerTerm)
	if nameCompact == "" || termCompact == "" {
		return 0, ""
	}
	if strings.Contains(nameCompact, termCompact) {
		return 800 + len(termCompact), "filename_substring"
	}
	stem := compact(strings.TrimSuffix(lowerName, filepath.Ext(lowerName)))
	if stem != "" && strings.Contains(termCompact, stem) {
		return 650 + len(stem), "filename_stem"
	}
	score := 0
	for _, token := range tokens(lowerTerm) {
		if strings.Contains(nameCompact, token) {
			score += 100 + len(token)
		}
	}
	if score > 0 {
		return score, "filename_tokens"
	}
	return 0, ""
}

func skipDir(name string) bool {
	switch name {
	case ".git", ".sparkclaw", "node_modules", "dist", "build", ".next", "vendor", ".venv":
		return true
	default:
		return false
	}
}

func compact(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, value)
}

func tokens(value string) []string {
	parts := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	stop := map[string]bool{"send": true, "publish": true, "share": true, "the": true, "a": true, "an": true, "file": true, "document": true, "please": true}
	out := []string{}
	for _, part := range parts {
		if token := compact(part); token != "" && !stop[token] {
			out = append(out, token)
		}
	}
	return out
}
