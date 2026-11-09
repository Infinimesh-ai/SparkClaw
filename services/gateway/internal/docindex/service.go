package docindex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

const (
	// DefaultMaxWorkspaceBytes caps the indexes kept for one workspace.
	DefaultMaxWorkspaceBytes = 512 << 20
	prebuildQueueSize        = 32
	cachedIndexes            = 4
	lastUsedRefresh          = time.Hour
	MaxSourceBytes           = 256 << 20
	MaxExtractedBytes        = 64 << 20
	MaxSourceUnits           = 250000
	MaxSourcePages           = 100000
)

// Target names one workspace document. RelPath is relative to
// WorkspaceRoot, which also scopes the index directory.
type Target struct {
	// Zero retains the v1 contract for existing direct callers.
	StructureVersion  int
	WorkspaceRoot     string
	RelPath           string
	Format            string
	SourceSHA256      string
	EnrichmentVersion string
}

// Prebuild shares the on-demand structure build for every document size.
// The historical Small* fields are ignored for source compatibility.
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
		if _, err := s.ensure(s.ctx, request.Target, &request, nil); err != nil && s.ctx.Err() == nil {
			slog.Info("document index prebuild failed; analysis will retry", "path", request.RelPath, "error", err)
		}
	}
}

// Ensure returns a usable index for target, waiting for a build already in
// progress or building it now. waiting is called once, before blocking, only
// when the index is not ready; callers use it to show progress.
func (s *Service) Ensure(ctx context.Context, target Target, waiting func()) (*Index, error) {
	return s.ensure(ctx, target, nil, waiting)
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
	path, err := targetPath(target)
	if err != nil {
		return err
	}
	hashCtx, cancel := context.WithTimeout(s.ctx, 5*time.Minute)
	defer cancel()
	sum, _, err := fileSHA256Context(hashCtx, path, MaxSourceBytes)
	if err != nil {
		return err
	}
	if target.SourceSHA256 != "" && sum != target.SourceSHA256 {
		return errors.New("document source changed during OCR")
	}
	dir := target.indexDir(sum)
	for page, text := range pages {
		if page <= 0 || page > MaxSourcePages || len(text) > MaxExtractedBytes {
			return errors.New("OCR page exceeds structure admission")
		}
		if err := writeOCRPage(dir, page, text, sum, target.EnrichmentVersion); err != nil {
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
	if target.StructureVersion < 0 || target.StructureVersion > document.StructureSchemaVersion {
		return nil, errors.New("unsupported document structure version")
	}
	path, err := targetPath(target)
	if err != nil {
		return nil, err
	}
	sum, size, err := fileSHA256Context(ctx, path, MaxSourceBytes)
	if err != nil {
		return nil, err
	}
	if target.SourceSHA256 != "" && target.SourceSHA256 != sum {
		return nil, errors.New("document source version changed")
	}
	dir := target.indexDir(sum)
	notified := false
	for {
		s.mu.Lock()
		if !force {
			if cached := s.cachedLocked(dir); cached != nil && cached.Manifest.Format == target.Format && cached.Manifest.EnrichmentVersion == target.EnrichmentVersion {
				cached = s.touchCachedLocked(dir, cached, target.RelPath)
				s.mu.Unlock()
				return bindIndex(cached, target, AcquisitionMemoryCache), nil
			}
		}
		call, inFlight := s.builds[dir]
		started := false
		if !inFlight {
			if !force {
				if index, ok := s.loadReadyLocked(dir, target); ok {
					s.mu.Unlock()
					return bindIndex(index, target, AcquisitionDiskCache), nil
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
			if call.err == nil && (call.index.Manifest.Format != target.Format || call.index.Manifest.EnrichmentVersion != target.EnrichmentVersion) {
				force = true
				continue
			}
			acquisition := AcquisitionSharedBuild
			if started {
				acquisition = AcquisitionBuilt
				if force {
					acquisition = AcquisitionRebuilt
				}
			}
			return bindIndex(call.index, target, acquisition), call.err
		}
	}
}

// loadReadyLocked loads a persisted usable index built by this builder
// version and records the requesting path as one of its sources.
func (s *Service) loadReadyLocked(dir string, target Target) (*Index, bool) {
	manifest, ok, err := readManifest(dir)
	if err != nil || !ok || !manifest.State.Usable() || manifest.BuilderVersion != target.builderVersion() || manifest.SchemaVersion != SchemaVersion || manifest.Format != target.Format || manifest.EnrichmentVersion != target.EnrichmentVersion {
		return nil, false
	}
	relPath := target.RelPath
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

func (s *Service) buildIndex(dir string, target Target, sum string, size int64, prebuild *Prebuild, reuseSource bool) (result *Index, resultErr error) {
	buildCtx, cancel := context.WithTimeout(s.ctx, 5*time.Minute)
	defer cancel()
	buildCtx = WithStructureVersion(buildCtx, target.StructureVersion)
	previous, _, _ := readManifest(dir)
	now := s.now().UTC()
	manifest := Manifest{
		SchemaVersion: SchemaVersion, BuilderVersion: target.builderVersion(), SourceSHA256: sum, SourceBytes: size,
		EnrichmentVersion: target.EnrichmentVersion, Format: target.Format, SourcePaths: []string{target.RelPath}, State: StateBuilding,
		Attempts: previous.Attempts + 1, UpdatedAt: now, LastUsedAt: now,
	}
	// Every size shares the same published build and retry lifecycle.
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := writeManifest(dir, manifest); err != nil {
		return nil, err
	}
	fail := func(buildErr error) (*Index, error) {
		// Failed acquisition is explicit; an on-demand call may retry it.
		if os.MkdirAll(dir, 0o700) == nil {
			manifest.State, manifest.Error, manifest.UpdatedAt = StateFailed, buildErr.Error(), s.now().UTC()
			_ = writeManifest(dir, manifest)
		}
		return nil, buildErr
	}
	source, reused := extractedSource{}, false
	if reuseSource && previous.SchemaVersion == SchemaVersion && previous.BuilderVersion == target.builderVersion() && previous.Format == target.Format {
		if stored, ok, err := readSource(dir); err == nil && ok {
			source, reused = stored, true
		}
	}
	sourcePath, err := targetPath(target)
	if err != nil {
		return fail(err)
	}
	if !reused {
		snapshot, err := snapshotSource(buildCtx, sourcePath, dir, sum, size)
		if err != nil {
			return fail(err)
		}
		defer func() {
			if cleanupErr := snapshot.Close(); cleanupErr != nil {
				result = nil
				resultErr = errors.Join(resultErr, fmt.Errorf("clean document parser snapshot: %w", cleanupErr))
				manifest.State = StateFailed
				manifest.Error = resultErr.Error()
				_ = writeManifest(dir, manifest)
			}
		}()
		extractedBytes := 0
		info, err := s.extractor.Extract(buildCtx, snapshot.Path, target.Format, func(unit Unit) error {
			raw, _ := json.Marshal(unit)
			extractedBytes += len(raw)
			if len(source.Units) >= MaxSourceUnits || extractedBytes > MaxExtractedBytes {
				return errors.New("document extraction exceeds structure admission")
			}
			if err := buildCtx.Err(); err != nil {
				return err
			}
			source.Units = append(source.Units, unit)
			return nil
		})
		if err != nil {
			return fail(fmt.Errorf("extract document text: %w", err))
		}
		if info.Pages > MaxSourcePages {
			return fail(errors.New("document pages exceed structure admission"))
		}
		source.Info = info
		if err := snapshot.Verify(buildCtx); err != nil {
			return fail(err)
		}
		if err := snapshot.Close(); err != nil {
			return fail(fmt.Errorf("clean document parser snapshot: %w", err))
		}
	} else {
		verified, verifiedSize, verifyErr := fileSHA256Context(buildCtx, sourcePath, MaxSourceBytes)
		if verifyErr != nil {
			return fail(verifyErr)
		}
		if verified != sum || verifiedSize != size {
			return fail(errors.New("document source changed during structure extraction"))
		}
	}
	recognized, ocrBytes, err := readOCRPages(dir, sum, target.EnrichmentVersion)
	if err != nil {
		return fail(err)
	}
	units, info, ocrPages := mergeRecognizedPages(source, recognized)
	canonicalBytes := 0
	for _, unit := range units {
		raw, _ := json.Marshal(unit)
		canonicalBytes += len(raw)
		if canonicalBytes > MaxExtractedBytes || len(units) > MaxSourceUnits {
			return fail(errors.New("enriched document exceeds structure admission"))
		}
	}
	var nodes []document.StructureNode
	var structure document.StructureManifest
	if target.StructureVersion == document.StructureSchemaVersion {
		units, nodes, structure, err = buildStructureTree(sum, target.Format, target.EnrichmentVersion, units, info)
		if err != nil {
			return fail(err)
		}
	} else {
		units, nodes, structure = buildStructure(sum, target.Format, target.EnrichmentVersion, units, info)
	}
	builder := &assembler{}
	for _, unit := range units {
		builder.add(unit)
	}
	chunks, outline := builder.chunks, []OutlineNode{}
	if target.StructureVersion == document.StructureSchemaVersion {
		outline = projectTreeOutline(nodes, chunks)
	} else {
		chunks, outline = builder.finish(info)
	}
	if len(nodes) == 0 {
		return fail(errors.New("document has no extractable text"))
	}
	keywords := buildKeywordIndex(chunks)
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
	structureBytes, cursorSecret, err := writeStructure(dir, structure, nodes)
	if err != nil {
		return fail(err)
	}
	indexBytes += sourceBytes + ocrBytes + structureBytes
	manifest.StructureGeneration = structure.StructureGeneration
	manifest.OCRPages = ocrPages
	manifest.State = StateReady
	if structure.Coverage.Missing > 0 {
		manifest.State = StatePartial
	}
	manifest.Pages, manifest.Chunks, manifest.TextBytes = info.Pages, len(chunks), builder.textBytes
	manifest.UnindexedPages, manifest.IndexBytes, manifest.UpdatedAt = info.UnindexedPages, indexBytes, s.now().UTC()
	if err := writeManifest(dir, manifest); err != nil {
		return fail(err)
	}
	index := &Index{cursorSecret: cursorSecret, Manifest: manifest, Structure: structure, Nodes: nodes, Outline: outline, Chunks: chunks, keywords: keywords}
	index.initTreeNavigation()
	return index, nil
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
		units = slices.DeleteFunc(units, func(unit Unit) bool { return unit.Page == page && unit.Availability != "available" && unit.Text == "" })
		units = append(units, Unit{Kind: "page", Provenance: "ocr", Text: text, Page: page, Block: fmt.Sprintf("document.page[%d]", page)})
		ocrPages = append(ocrPages, page)
	}
	if len(ocrPages) > 0 {
		sort.SliceStable(units, func(i, j int) bool { return units[i].Page < units[j].Page })
	}
	return units, info, ocrPages
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

func targetPath(target Target) (string, error) {
	if !filepath.IsAbs(target.WorkspaceRoot) || filepath.IsAbs(target.RelPath) {
		return "", errors.New("document target must be workspace-relative")
	}
	root, err := filepath.EvalSymlinks(target.WorkspaceRoot)
	if err != nil {
		return "", err
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, target.RelPath))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("document target escapes workspace")
	}
	return path, nil
}

func bindIndex(index *Index, target Target, acquisition string) *Index {
	if index == nil {
		return nil
	}
	copy := *index
	copy.Acquisition = acquisition
	root, _ := filepath.EvalSymlinks(target.WorkspaceRoot)
	copy.AccessScope = structureHash([]string{root, filepath.Clean(target.RelPath)})
	return &copy
}

func (s *Service) touchCachedLocked(dir string, index *Index, relPath string) *Index {
	if slices.Contains(index.Manifest.SourcePaths, relPath) && s.now().UTC().Sub(index.Manifest.LastUsedAt) < lastUsedRefresh {
		return index
	}
	copy := *index
	copy.Manifest = index.Manifest
	copy.Manifest.SourcePaths = append([]string{}, index.Manifest.SourcePaths...)
	if !slices.Contains(copy.Manifest.SourcePaths, relPath) {
		copy.Manifest.SourcePaths = append(copy.Manifest.SourcePaths, relPath)
	}
	copy.Manifest.LastUsedAt = s.now().UTC()
	if writeManifest(dir, copy.Manifest) == nil {
		s.rememberLocked(dir, &copy)
		return &copy
	}
	return index
}

func (target Target) builderVersion() string {
	if target.StructureVersion == document.StructureSchemaVersion {
		return "docindex_structure_tree_bm25_v1"
	}
	return BuilderVersion
}
func (target Target) indexDir(sum string) string {
	if target.StructureVersion == document.StructureSchemaVersion {
		sum += "-tree-v1"
	}
	return Dir(target.WorkspaceRoot, sum)
}
