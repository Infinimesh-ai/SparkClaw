package docindex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultMaxWorkspaceBytes caps the indexes kept for one workspace.
	DefaultMaxWorkspaceBytes = 512 << 20
	prebuildQueueSize        = 32
	cachedIndexes            = 4
	lastUsedRefresh          = time.Hour
)

// ErrNotRequired reports that a prebuild found a document small enough for
// the complete-read path, so no index was kept.
var ErrNotRequired = errors.New("document fits the complete-read path")

// Target names one workspace document. RelPath is relative to
// WorkspaceRoot, which also scopes the index directory.
type Target struct {
	WorkspaceRoot string
	RelPath       string
	Format        string
}

// Prebuild is a background build request. A document whose source is at most
// SmallSourceBytes and whose text is at most SmallTextBytes keeps no index.
type Prebuild struct {
	Target
	SmallSourceBytes int64
	SmallTextBytes   int
}

// Service builds indexes once per source hash, lets concurrent requests wait
// for the same build, and runs background prebuilds on one worker.
type Service struct {
	extractor         Extractor
	maxWorkspaceBytes int64
	now               func() time.Time

	ctx    context.Context
	cancel context.CancelFunc
	queue  chan Prebuild
	wg     sync.WaitGroup

	mu     sync.Mutex
	builds map[string]*buildCall
	cache  []cachedIndex
	closed bool
}

type cachedIndex struct {
	dir   string
	index *Index
}

type buildCall struct {
	done  chan struct{}
	index *Index
	err   error
}

func NewService(extractor Extractor) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	service := &Service{
		extractor: extractor, maxWorkspaceBytes: DefaultMaxWorkspaceBytes, now: time.Now,
		ctx: ctx, cancel: cancel, queue: make(chan Prebuild, prebuildQueueSize), builds: map[string]*buildCall{},
	}
	service.wg.Add(1)
	go service.runPrebuilds()
	return service
}

// Close cancels running builds and stops the prebuild worker.
func (s *Service) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	close(s.queue)
	s.wg.Wait()
}

// Enqueue schedules a background prebuild without blocking. It reports false
// when the queue is full or closed; the analysis phase then builds on demand.
func (s *Service) Enqueue(request Prebuild) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	select {
	case s.queue <- request:
		return true
	default:
		return false
	}
}

func (s *Service) runPrebuilds() {
	defer s.wg.Done()
	for request := range s.queue {
		if s.ctx.Err() != nil {
			continue
		}
		if _, err := s.ensure(s.ctx, request.Target, &request, nil); err != nil && !errors.Is(err, ErrNotRequired) && s.ctx.Err() == nil {
			slog.Info("document index prebuild failed; analysis will retry", "path", request.RelPath, "error", err)
		}
	}
}

// Ensure returns a usable index for target, waiting for a build already in
// progress or building it now. waiting is called once, before blocking, only
// when the index is not ready; callers use it to show progress.
func (s *Service) Ensure(ctx context.Context, target Target, waiting func()) (*Index, error) {
	index, err := s.ensure(ctx, target, nil, waiting)
	if errors.Is(err, ErrNotRequired) {
		// A concurrent prebuild judged the document small; this caller
		// needs the index anyway.
		return s.ensure(ctx, target, nil, nil)
	}
	return index, err
}

// RecognizedPages adds analysis-phase OCR text for pages of target to its
// index cache and rebuilds the index with them. Cached pages survive an
// interrupted request, and the next rebuild includes them.
func (s *Service) RecognizedPages(ctx context.Context, target Target, pages map[int]string) (*Index, error) {
	if err := s.CacheRecognizedPages(target, pages); err != nil {
		return nil, err
	}
	return s.build(ctx, target, nil, nil, true)
}

// CacheRecognizedPages stores OCR text for pages of target without
// rebuilding, so a long recognition can persist each batch as it finishes.
func (s *Service) CacheRecognizedPages(target Target, pages map[int]string) error {
	sum, _, err := fileSHA256(filepath.Join(target.WorkspaceRoot, target.RelPath))
	if err != nil {
		return err
	}
	dir := Dir(target.WorkspaceRoot, sum)
	for page, text := range pages {
		if err := writeOCRPage(dir, page, text); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ensure(ctx context.Context, target Target, prebuild *Prebuild, waiting func()) (*Index, error) {
	return s.build(ctx, target, prebuild, waiting, false)
}

// build returns a usable index, rebuilding it when force is set. A forced
// rebuild waits for any build in flight first, so its own merge of cached
// pages is never answered by an older build.
func (s *Service) build(ctx context.Context, target Target, prebuild *Prebuild, waiting func(), force bool) (*Index, error) {
	path := filepath.Join(target.WorkspaceRoot, target.RelPath)
	sum, size, err := fileSHA256(path)
	if err != nil {
		return nil, err
	}
	dir := Dir(target.WorkspaceRoot, sum)
	notified := false
	for {
		s.mu.Lock()
		if !force {
			if cached := s.cachedLocked(dir); cached != nil {
				s.mu.Unlock()
				return cached, nil
			}
		}
		call, inFlight := s.builds[dir]
		started := false
		if !inFlight {
			if !force {
				if index, ok := s.loadReadyLocked(dir, target.RelPath); ok {
					s.mu.Unlock()
					return index, nil
				}
			}
			if s.closed {
				s.mu.Unlock()
				return nil, errors.New("document index service is closed")
			}
			call = &buildCall{done: make(chan struct{})}
			s.builds[dir] = call
			s.wg.Add(1)
			go s.run(call, dir, target, sum, size, prebuild, force)
			started = true
		}
		s.mu.Unlock()
		if waiting != nil && !notified {
			waiting()
			notified = true
		}
		select {
		case <-call.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if started || !force {
			return call.index, call.err
		}
	}
}

// loadReadyLocked loads a persisted usable index built by this builder
// version and records the requesting path as one of its sources.
func (s *Service) loadReadyLocked(dir, relPath string) (*Index, bool) {
	manifest, ok, err := readManifest(dir)
	if err != nil || !ok || !manifest.State.Usable() || manifest.BuilderVersion != BuilderVersion || manifest.SchemaVersion != SchemaVersion {
		return nil, false
	}
	index, err := loadIndex(dir, manifest)
	if err != nil {
		return nil, false
	}
	if now := s.now().UTC(); !slices.Contains(manifest.SourcePaths, relPath) || now.Sub(manifest.LastUsedAt) > lastUsedRefresh {
		if !slices.Contains(manifest.SourcePaths, relPath) {
			manifest.SourcePaths = append(manifest.SourcePaths, relPath)
		}
		manifest.LastUsedAt = now
		if writeManifest(dir, manifest) == nil {
			index.Manifest = manifest
		}
	}
	s.rememberLocked(dir, index)
	return index, true
}

func (s *Service) run(call *buildCall, dir string, target Target, sum string, size int64, prebuild *Prebuild, reuseSource bool) {
	defer s.wg.Done()
	index, err := s.buildIndex(dir, target, sum, size, prebuild, reuseSource)
	s.mu.Lock()
	call.index, call.err = index, err
	delete(s.builds, dir)
	if err == nil {
		s.rememberLocked(dir, index)
	}
	s.mu.Unlock()
	close(call.done)
	if err := sweep(target.WorkspaceRoot, s.maxWorkspaceBytes); err != nil {
		slog.Warn("document index sweep failed", "error", err)
	}
}

func (s *Service) buildIndex(dir string, target Target, sum string, size int64, prebuild *Prebuild, reuseSource bool) (*Index, error) {
	previous, _, _ := readManifest(dir)
	now := s.now().UTC()
	manifest := Manifest{
		SchemaVersion: SchemaVersion, BuilderVersion: BuilderVersion, SourceSHA256: sum, SourceBytes: size,
		Format: target.Format, SourcePaths: []string{target.RelPath}, State: StateBuilding,
		Attempts: previous.Attempts + 1, UpdatedAt: now, LastUsedAt: now,
	}
	// An on-demand build records its attempt before extracting. A prebuild
	// writes nothing until it knows the document needs an index.
	published := prebuild == nil
	if published {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		if err := writeManifest(dir, manifest); err != nil {
			return nil, err
		}
	}
	fail := func(buildErr error) (*Index, error) {
		// A failed prebuild is only logged; the on-demand build that retries
		// it records its own attempt.
		if published && os.MkdirAll(dir, 0o700) == nil {
			manifest.State, manifest.Error, manifest.UpdatedAt = StateFailed, buildErr.Error(), s.now().UTC()
			_ = writeManifest(dir, manifest)
		}
		return nil, buildErr
	}
	source, reused := extractedSource{}, false
	if reuseSource {
		if stored, ok, err := readSource(dir); err == nil && ok {
			source, reused = stored, true
		}
	}
	if !reused {
		info, err := s.extractor.Extract(s.ctx, filepath.Join(target.WorkspaceRoot, target.RelPath), target.Format, func(unit Unit) error {
			source.Units = append(source.Units, unit)
			return nil
		})
		if err != nil {
			return fail(fmt.Errorf("extract document text: %w", err))
		}
		source.Info = info
	}
	if prebuild != nil && size <= prebuild.SmallSourceBytes && sourceTextBytes(source.Units) <= prebuild.SmallTextBytes && len(source.Info.UnindexedPages) == 0 {
		return nil, ErrNotRequired
	}
	recognized, ocrBytes, err := readOCRPages(dir)
	if err != nil {
		return fail(err)
	}
	units, info, ocrPages := mergeRecognizedPages(source, recognized)
	assembler := &assembler{}
	for _, unit := range units {
		assembler.add(unit)
	}
	chunks, outline := assembler.finish(info)
	if len(chunks) == 0 && len(info.UnindexedPages) == 0 {
		return fail(errors.New("document has no extractable text"))
	}
	keywords := buildKeywordIndex(chunks)
	published = true
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fail(err)
	}
	sourceBytes, err := writeSource(dir, source)
	if err != nil {
		return fail(err)
	}
	indexBytes, err := writeIndexFiles(dir, outline, chunks, keywords)
	if err != nil {
		return fail(err)
	}
	indexBytes += sourceBytes + ocrBytes
	manifest.OCRPages = ocrPages
	manifest.State = StateReady
	if len(info.UnindexedPages) > 0 {
		manifest.State = StatePartial
	}
	manifest.Pages, manifest.Chunks, manifest.TextBytes = info.Pages, len(chunks), assembler.textBytes
	manifest.UnindexedPages, manifest.IndexBytes, manifest.UpdatedAt = info.UnindexedPages, indexBytes, s.now().UTC()
	if err := writeManifest(dir, manifest); err != nil {
		return fail(err)
	}
	return &Index{Manifest: manifest, Outline: outline, Chunks: chunks, keywords: keywords}, nil
}

// mergeRecognizedPages adds cached OCR text for text-less pages to the
// extracted units in page order and removes those pages from the unindexed
// list.
func mergeRecognizedPages(source extractedSource, recognized map[int]string) ([]Unit, SourceInfo, []int) {
	info := source.Info
	units := append([]Unit(nil), source.Units...)
	info.UnindexedPages = nil
	ocrPages := []int{}
	for _, page := range source.Info.UnindexedPages {
		text, ok := recognized[page]
		if !ok || strings.TrimSpace(text) == "" {
			info.UnindexedPages = append(info.UnindexedPages, page)
			continue
		}
		units = append(units, Unit{Text: text, Page: page, Block: fmt.Sprintf("document.page[%d]", page)})
		ocrPages = append(ocrPages, page)
	}
	if len(ocrPages) > 0 {
		sort.SliceStable(units, func(i, j int) bool { return units[i].Page < units[j].Page })
	}
	return units, info, ocrPages
}

func sourceTextBytes(units []Unit) int {
	total := 0
	for _, unit := range units {
		total += len(strings.TrimSpace(unit.Text))
	}
	return total
}

func (s *Service) cachedLocked(dir string) *Index {
	for _, entry := range s.cache {
		if entry.dir == dir {
			return entry.index
		}
	}
	return nil
}

// rememberLocked keeps the most recently used indexes in memory.
func (s *Service) rememberLocked(dir string, index *Index) {
	s.cache = slices.DeleteFunc(s.cache, func(entry cachedIndex) bool { return entry.dir == dir })
	s.cache = append([]cachedIndex{{dir: dir, index: index}}, s.cache...)
	if len(s.cache) > cachedIndexes {
		s.cache = s.cache[:cachedIndexes]
	}
}

func fileSHA256(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}
