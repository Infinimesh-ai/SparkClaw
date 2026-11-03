package docindex

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	manifestFile = "manifest.json"
	outlineFile  = "outline.json"
	chunksFile   = "chunks.jsonl"
	keywordsFile = "keywords.json"
	// sourceFile keeps the extracted units, so merging recognized pages
	// rebuilds chunks and keywords without parsing the document again.
	sourceFile = "source.json"
	ocrDirName = "ocr"
)

// extractedSource is one extraction of a document, in document order.
type extractedSource struct {
	Info  SourceInfo `json:"info"`
	Units []Unit     `json:"units"`
}

func writeSource(dir string, source extractedSource) (int64, error) {
	raw, err := json.Marshal(source)
	if err != nil {
		return 0, err
	}
	return int64(len(raw)), writeFileAtomic(filepath.Join(dir, sourceFile), raw)
}

func readSource(dir string) (extractedSource, bool, error) {
	raw, err := os.ReadFile(filepath.Join(dir, sourceFile))
	if errors.Is(err, fs.ErrNotExist) {
		return extractedSource{}, false, nil
	}
	if err != nil {
		return extractedSource{}, false, err
	}
	var source extractedSource
	if err := json.Unmarshal(raw, &source); err != nil {
		return extractedSource{}, false, fmt.Errorf("decode extracted source: %w", err)
	}
	return source, true, nil
}

type ocrPage struct {
	Page int    `json:"page"`
	Text string `json:"text"`
}

func writeOCRPage(dir string, page int, text string) error {
	ocrDir := filepath.Join(dir, ocrDirName)
	if err := os.MkdirAll(ocrDir, 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(ocrPage{Page: page, Text: text})
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(ocrDir, fmt.Sprintf("%d.json", page)), raw)
}

// readOCRPages returns the recognized text of every cached page and the
// bytes the cache occupies.
func readOCRPages(dir string) (map[int]string, int64, error) {
	entries, err := os.ReadDir(filepath.Join(dir, ocrDirName))
	if errors.Is(err, fs.ErrNotExist) {
		return map[int]string{}, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	pages := map[int]string{}
	total := int64(0)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, ocrDirName, entry.Name()))
		if err != nil {
			return nil, 0, err
		}
		var page ocrPage
		if json.Unmarshal(raw, &page) == nil && page.Page > 0 {
			pages[page.Page] = page.Text
			total += int64(len(raw))
		}
	}
	return pages, total, nil
}

// RelativeRoot is the index directory inside a workspace root. It sits in
// the .sparkclaw/state control directory that file tools refuse.
var RelativeRoot = filepath.Join(".sparkclaw", "state", "doc-index")

// Dir is the index directory of one source hash in a workspace.
func Dir(workspaceRoot, sourceSHA256 string) string {
	return filepath.Join(workspaceRoot, RelativeRoot, sourceSHA256)
}

func readManifest(dir string) (Manifest, bool, error) {
	raw, err := os.ReadFile(filepath.Join(dir, manifestFile))
	if errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, false, nil
	}
	if err != nil {
		return Manifest{}, false, err
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return Manifest{}, false, fmt.Errorf("decode index manifest: %w", err)
	}
	return manifest, true, nil
}

func writeManifest(dir string, manifest Manifest) error {
	raw, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, manifestFile), raw)
}

// writeIndexFiles persists everything but the manifest and returns the
// bytes written; the manifest is written last to publish the index.
func writeIndexFiles(dir string, outline []OutlineNode, chunks []Chunk, keywords keywordIndex) (int64, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, err
	}
	total := int64(0)
	rawOutline, err := json.Marshal(outline)
	if err != nil {
		return 0, err
	}
	if err := writeFileAtomic(filepath.Join(dir, outlineFile), rawOutline); err != nil {
		return 0, err
	}
	total += int64(len(rawOutline))
	var lines strings.Builder
	encoder := json.NewEncoder(&lines)
	for _, chunk := range chunks {
		if err := encoder.Encode(chunk); err != nil {
			return 0, err
		}
	}
	if err := writeFileAtomic(filepath.Join(dir, chunksFile), []byte(lines.String())); err != nil {
		return 0, err
	}
	total += int64(lines.Len())
	rawKeywords, err := json.Marshal(keywords)
	if err != nil {
		return 0, err
	}
	if err := writeFileAtomic(filepath.Join(dir, keywordsFile), rawKeywords); err != nil {
		return 0, err
	}
	return total + int64(len(rawKeywords)), nil
}

func loadIndex(dir string, manifest Manifest) (*Index, error) {
	index := &Index{Manifest: manifest}
	rawOutline, err := os.ReadFile(filepath.Join(dir, outlineFile))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(rawOutline, &index.Outline); err != nil {
		return nil, fmt.Errorf("decode index outline: %w", err)
	}
	file, err := os.Open(filepath.Join(dir, chunksFile))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	for scanner.Scan() {
		var chunk Chunk
		if err := json.Unmarshal(scanner.Bytes(), &chunk); err != nil {
			return nil, fmt.Errorf("decode index chunk: %w", err)
		}
		index.Chunks = append(index.Chunks, chunk)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	rawKeywords, err := os.ReadFile(filepath.Join(dir, keywordsFile))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(rawKeywords, &index.keywords); err != nil {
		return nil, fmt.Errorf("decode keyword index: %w", err)
	}
	if len(index.Chunks) != manifest.Chunks || len(index.keywords.Lengths) != len(index.Chunks) {
		return nil, errors.New("document index files do not match their manifest")
	}
	return index, nil
}

func writeFileAtomic(path string, raw []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	if _, err := temp.Write(raw); err != nil {
		_ = temp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}

// sweep removes indexes whose recorded sources no longer exist with their
// recorded size, then the least recently used ones until the workspace's
// indexes fit maxBytes.
func sweep(workspaceRoot string, maxBytes int64) error {
	base := filepath.Join(workspaceRoot, RelativeRoot)
	entries, err := os.ReadDir(base)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	type kept struct {
		dir      string
		manifest Manifest
	}
	live := []kept{}
	total := int64(0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(base, entry.Name())
		manifest, ok, err := readManifest(dir)
		if err != nil || !ok {
			continue
		}
		if manifest.State == StateBuilding {
			continue
		}
		if !anySourcePresent(workspaceRoot, manifest) {
			_ = os.RemoveAll(dir)
			continue
		}
		live = append(live, kept{dir: dir, manifest: manifest})
		total += manifest.IndexBytes
	}
	sort.Slice(live, func(i, j int) bool { return live[i].manifest.LastUsedAt.Before(live[j].manifest.LastUsedAt) })
	for _, item := range live {
		if total <= maxBytes {
			break
		}
		if err := os.RemoveAll(item.dir); err == nil {
			total -= item.manifest.IndexBytes
		}
	}
	return nil
}

func anySourcePresent(workspaceRoot string, manifest Manifest) bool {
	for _, rel := range manifest.SourcePaths {
		info, err := os.Stat(filepath.Join(workspaceRoot, rel))
		if err == nil && info.Mode().IsRegular() && info.Size() == manifest.SourceBytes {
			return true
		}
	}
	return false
}
