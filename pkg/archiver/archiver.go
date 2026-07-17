// Package archiver converts a set of downloaded toon chapters into a
// distributable archive that users can save on their computer or phone.
//
// Supported output formats:
//
//   - FormatPDF   – one PDF per chapter, all bundled into a ZIP archive.
//   - FormatCBZ   – one CBZ (ZIP of images) per chapter, all bundled into a ZIP archive.
//   - FormatImages – raw image files in per-chapter sub-folders, bundled into a ZIP archive.
//
// In every case the outermost container is a single ZIP file written to a
// temporary file on disk — never fully buffered in memory — so the caller
// can serve it with http.ServeContent and the process stays within its RAM budget.
package archiver

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	_ "image/png" // register PNG decoder
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Zapharaos/offtoon-backend/internal/toon"
	"github.com/Zapharaos/offtoon-backend/pkg/workerpool"
	"github.com/signintech/gopdf"
	"go.uber.org/zap"
	_ "golang.org/x/image/webp" // register WebP decoder
)

// Format is the per-chapter output format chosen by the caller.
type Format string

const (
	// FormatPDF creates one PDF per chapter (images embedded as pages).
	FormatPDF Format = "pdf"
	// FormatCBZ creates one CBZ archive per chapter (raw images inside a ZIP).
	FormatCBZ Format = "cbz"
	// FormatImages places raw image files in per-chapter sub-folders.
	FormatImages Format = "images"
)

// ParseFormat normalises a raw string into a Format constant.
// Unrecognised values fall back to FormatPDF.
func ParseFormat(raw string) Format {
	switch Format(strings.ToLower(strings.TrimSpace(raw))) {
	case FormatCBZ:
		return FormatCBZ
	case FormatImages:
		return FormatImages
	default:
		return FormatPDF
	}
}

// Progress sub-phases reported by ProgressFunc. A download is two page-level
// passes: images are fetched (PhaseDownloading), then assembled into the chosen
// format (PhaseBuilding — the CPU-heavy step, especially for PDF). Reporting
// them separately lets the UI show real progress during both, rather than
// freezing while the archive is built.
const (
	PhaseDownloading = "downloading"
	PhaseBuilding    = "building"
)

// ProgressFunc is called frequently to report page-level progress for a phase
// (PhaseDownloading or PhaseBuilding). done is the cumulative number of pages
// processed in that phase; total is the number of pages known so far. Because
// pages are resolved lazily when pipelining, total starts at 0 and grows as
// each chapter's page list resolves. Frequent per-page calls keep the progress
// UI (and the WebSocket) lively throughout both phases.
type ProgressFunc func(phase string, done, total int)

// PageResolverFunc lazily resolves the page list for a single chapter. When
// passed to Build, each chapter worker calls it before downloading images —
// this is what lets metadata resolution pipeline with image downloads across
// chapters instead of resolving every chapter's pages up front. It returns the
// chapter's pages, or an error which marks that one chapter as failed without
// aborting the whole build.
type PageResolverFunc func(ctx context.Context, chapter toon.Chapter) ([]toon.Page, error)

// ChapterStatus is the outcome of building one chapter's archive entry.
type ChapterStatus string

const (
	// ChapterStatusSuccess means every image was fetched and the archive entry was built.
	ChapterStatusSuccess ChapterStatus = "success"
	// ChapterStatusIncomplete means some images failed but the chapter was built with the rest.
	ChapterStatusIncomplete ChapterStatus = "incomplete"
	// ChapterStatusFailed means the chapter produced no archive entry at all.
	ChapterStatusFailed ChapterStatus = "failed"
)

// ImageStatus is the outcome of fetching one page image.
type ImageStatus string

const (
	ImageStatusSuccess ImageStatus = "success"
	ImageStatusFailed  ImageStatus = "failed"
)

// ImageReport carries the fetch outcome for a single page image.
type ImageReport struct {
	Page   int         `json:"page"`
	URL    string      `json:"url"`
	Status ImageStatus `json:"status"`
	Reason string      `json:"reason,omitempty"` // non-empty only on failure
}

// ChapterReport is the build outcome for a single chapter, streamed to the
// caller as soon as the chapter worker finishes (before the outer ZIP is written).
type ChapterReport struct {
	ChapterID string        `json:"chapter_id"`
	Chapter   string        `json:"chapter"` // display name, e.g. "Chapter 001"
	Status    ChapterStatus `json:"status"`
	Reason    string        `json:"reason,omitempty"` // non-empty on failed/incomplete
	Images    []ImageReport `json:"images,omitempty"` // omitted on full success
}

// OnChapterDoneFunc is called from the worker-pool result handler (single
// goroutine) each time a chapter finishes building, whether it succeeded,
// was incomplete, or failed entirely.  Callers use it to push progressive
// status packets to connected WebSocket clients.
type OnChapterDoneFunc func(report ChapterReport)

// chapterJob is the input unit for the chapter-level worker pool.
type chapterJob struct {
	index   int          // original position in the chapters slice — used to restore order
	chapter toon.Chapter // chapter metadata + page list
}

// chapterResult is what the chapter worker returns: the ready-to-write ZIP
// entries for one chapter and the outcome report to stream to the caller.
type chapterResult struct {
	index   int           // mirrors chapterJob.index so the collector can sort
	entries []zipEntry    // one entry per PDF/CBZ/image file produced
	report  ChapterReport // always populated; streamed to the caller immediately
}

// zipEntry is a single file to be written into the outer ZIP archive.
type zipEntry struct {
	name string
	data []byte
}

// Build downloads all page images for every chapter and assembles them into a
// single ZIP archive whose internal layout depends on format:
//
//	FormatPDF:    <toonSlug>/Chapter 001.pdf
//	FormatCBZ:    <toonSlug>/Chapter 001.cbz
//	FormatImages: <toonSlug>/Chapter 001/<page>.webp
//
// The archive is written to a temporary file on disk and its path is returned.
// The caller is responsible for deleting the file after use.
//
// Chapters are processed concurrently using a worker pool sized to
// runtime.NumCPU(). Each worker independently fetches the chapter's page
// images (themselves fetched concurrently via a nested image pool) and then
// builds the requested archive format. Results are collected in original
// chapter order before being written into the outer ZIP so the archive layout
// is always deterministic.
//
// ctx is forwarded to every HTTP request; cancelling it aborts all in-flight
// work promptly. onProgress is optional and is called after every page image
// is successfully downloaded. onZipping is optional and is called once, right
// before the final outer ZIP is written — useful to notify clients that the
// last silent phase (which can take several minutes for large archives) has begun.
func Build(ctx context.Context, chapters []toon.Chapter, slug string, format Format, httpClient *http.Client, onProgress ProgressFunc, onZipping func(), onChapterDone OnChapterDoneFunc, resolvePages PageResolverFunc) (string, int, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}

	// Page-level progress counters, updated concurrently from every chapter's
	// worker — hence atomic. pagesTotal grows as chapters resolve their page
	// lists. pagesDownloaded advances as images are fetched; pagesBuilt advances
	// as pages are assembled into the archive format. The two emit helpers are
	// called on every page so a late-connecting WebSocket client always catches
	// the next update and never sits on a stale "connecting" view.
	var pagesTotal, pagesDownloaded, pagesBuilt int64
	emitDownloading := func() {
		if onProgress != nil {
			onProgress(PhaseDownloading, int(atomic.LoadInt64(&pagesDownloaded)), int(atomic.LoadInt64(&pagesTotal)))
		}
	}
	emitBuilding := func() {
		if onProgress != nil {
			onProgress(PhaseBuilding, int(atomic.LoadInt64(&pagesBuilt)), int(atomic.LoadInt64(&pagesTotal)))
		}
	}
	// Emit once up front (0/0) so the UI leaves its "connecting" state immediately
	// instead of waiting for the first chapter's pages to resolve.
	emitDownloading()

	jobs := make([]chapterJob, len(chapters))
	for i, ch := range chapters {
		jobs[i] = chapterJob{index: i, chapter: ch}
	}

	// Open the outer ZIP temp file immediately so we can stream chapter
	// entries into it as they finish — never accumulating all bytes in RAM.
	tmpFile, err := os.CreateTemp("", "offtoon-archive-*.zip")
	if err != nil {
		return "", 0, fmt.Errorf("archiver: create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	zw := zip.NewWriter(tmpFile)

	// cleanup closes and removes the temp file on any error path.
	cleanup := func() {
		_ = zw.Close()
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}

	// Streaming ordered writer:
	// nextIndex tracks the next chapter index we are allowed to write.
	// pending holds results that arrived out of order.
	// When a result arrives for nextIndex we write it immediately and then
	// drain any contiguous pending entries — so at most a handful of
	// chapters are ever buffered in memory at the same time.
	nextIndex := 0
	skippedChapters := 0
	succeededChapters := 0
	pending := make(map[int]chapterResult, len(chapters))

	flushPending := func() error {
		for {
			res, ok := pending[nextIndex]
			if !ok {
				break
			}
			delete(pending, nextIndex)
			nextIndex++

			if len(res.entries) == 0 {
				skippedChapters++
				continue
			}
			succeededChapters++
			for _, e := range res.entries {
				if err := writeZipEntry(zw, e.name, e.data); err != nil {
					return err
				}
			}
			// Free the chapter's image bytes immediately after writing.
			res.entries = nil
		}
		return nil
	}

	workerFunc := func(ctx context.Context, job chapterJob) (chapterResult, error) {
		ch := job.chapter
		chapterName := safeChapterName(ch)
		dirPrefix := path.Join(slug, chapterName)

		// Resolve the chapter's page URLs lazily if a resolver was provided.
		// This is the pipelining step: chapter A's images start downloading while
		// chapter B's page list is still being fetched, instead of resolving every
		// chapter's metadata up front before any image download begins.
		if resolvePages != nil {
			pages, err := resolvePages(ctx, ch)
			if err != nil {
				zap.L().Warn("archiver: chapter skipped — page resolution failed",
					zap.String("chapter_id", ch.ID),
					zap.String("chapter", chapterName),
					zap.Error(err),
				)
				report := ChapterReport{
					ChapterID: ch.ID,
					Chapter:   chapterName,
					Status:    ChapterStatusFailed,
					Reason:    fmt.Sprintf("page list resolution failed: %s", err.Error()),
				}
				return chapterResult{index: job.index, entries: nil, report: report}, nil
			}
			ch.Pages = pages
		}

		if len(ch.Pages) == 0 {
			zap.L().Warn("archiver: chapter skipped — no pages",
				zap.String("chapter_id", ch.ID),
				zap.String("chapter", chapterName),
			)
			report := ChapterReport{
				ChapterID: ch.ID,
				Chapter:   chapterName,
				Status:    ChapterStatusFailed,
				Reason:    "no pages to download",
			}
			return chapterResult{index: job.index, entries: nil, report: report}, nil
		}

		// Reveal this chapter's page count as soon as it is known, so the progress
		// total reflects work in flight rather than jumping only at completion.
		atomic.AddInt64(&pagesTotal, int64(len(ch.Pages)))
		emitDownloading()

		rawImages, reasons, err := fetchImages(ctx, ch.Pages, ch.URL, httpClient, func() {
			atomic.AddInt64(&pagesDownloaded, 1)
			emitDownloading()
		})
		if err != nil {
			report := ChapterReport{
				ChapterID: ch.ID,
				Chapter:   chapterName,
				Status:    ChapterStatusFailed,
				Reason:    err.Error(),
			}
			zap.L().Warn("archiver: chapter skipped — image fetch failed",
				zap.String("chapter_id", ch.ID),
				zap.String("chapter", chapterName),
				zap.Error(err),
			)
			return chapterResult{index: job.index, entries: nil, report: report}, nil
		}

		imageReports := make([]ImageReport, len(ch.Pages))
		images := make([][]byte, 0, len(rawImages))
		for i, img := range rawImages {
			page := ch.Pages[i]
			if img != nil {
				imageReports[i] = ImageReport{
					Page:   page.Number,
					URL:    page.ImageURL,
					Status: ImageStatusSuccess,
				}
				images = append(images, img)
			} else {
				imageReports[i] = ImageReport{
					Page:   page.Number,
					URL:    page.ImageURL,
					Status: ImageStatusFailed,
					Reason: reasons[i],
				}
			}
		}

		skippedImages := len(ch.Pages) - len(images)
		if skippedImages > 0 {
			zap.L().Warn("archiver: chapter built with missing pages",
				zap.String("chapter_id", ch.ID),
				zap.String("chapter", chapterName),
				zap.Int("skipped_pages", skippedImages),
				zap.Int("total_pages", len(ch.Pages)),
			)
		}

		if len(images) == 0 {
			zap.L().Warn("archiver: chapter skipped — no valid images",
				zap.String("chapter_id", ch.ID),
				zap.String("chapter", chapterName),
			)
			report := ChapterReport{
				ChapterID: ch.ID,
				Chapter:   chapterName,
				Status:    ChapterStatusFailed,
				Reason:    "all images failed to download",
				Images:    imageReports,
			}
			return chapterResult{index: job.index, entries: nil, report: report}, nil
		}

		var entries []zipEntry

		// onPageBuilt advances the "building" phase progress as each page is
		// assembled into the output format — the slow, CPU-bound step (esp. PDF).
		onPageBuilt := func() {
			atomic.AddInt64(&pagesBuilt, 1)
			emitBuilding()
		}

		switch format {
		case FormatPDF:
			pdfBytes, err := buildPDF(images, onPageBuilt)
			if err != nil {
				zap.L().Warn("archiver: chapter skipped — PDF build failed",
					zap.String("chapter_id", ch.ID),
					zap.String("chapter", chapterName),
					zap.Error(err),
				)
				report := ChapterReport{
					ChapterID: ch.ID,
					Chapter:   chapterName,
					Status:    ChapterStatusFailed,
					Reason:    fmt.Sprintf("PDF build failed: %s", err.Error()),
					Images:    imageReports,
				}
				return chapterResult{index: job.index, entries: nil, report: report}, nil
			}
			entries = []zipEntry{{name: dirPrefix + ".pdf", data: pdfBytes}}

		case FormatCBZ:
			cbzBytes, err := buildCBZ(ch.Pages, images, onPageBuilt)
			if err != nil {
				zap.L().Warn("archiver: chapter skipped — CBZ build failed",
					zap.String("chapter_id", ch.ID),
					zap.String("chapter", chapterName),
					zap.Error(err),
				)
				report := ChapterReport{
					ChapterID: ch.ID,
					Chapter:   chapterName,
					Status:    ChapterStatusFailed,
					Reason:    fmt.Sprintf("CBZ build failed: %s", err.Error()),
					Images:    imageReports,
				}
				return chapterResult{index: job.index, entries: nil, report: report}, nil
			}
			entries = []zipEntry{{name: dirPrefix + ".cbz", data: cbzBytes}}

		case FormatImages:
			entries = make([]zipEntry, len(images))
			for i, img := range images {
				ext := imageExt(ch.Pages, i)
				entries[i] = zipEntry{
					name: fmt.Sprintf("%s/%03d%s", dirPrefix, i+1, ext),
					data: img,
				}
				onPageBuilt()
			}
		}

		var report ChapterReport
		if skippedImages == 0 {
			report = ChapterReport{
				ChapterID: ch.ID,
				Chapter:   chapterName,
				Status:    ChapterStatusSuccess,
			}
		} else {
			report = ChapterReport{
				ChapterID: ch.ID,
				Chapter:   chapterName,
				Status:    ChapterStatusIncomplete,
				Reason:    fmt.Sprintf("%d out of %d images failed to download", skippedImages, len(ch.Pages)),
				Images:    imageReports,
			}
		}

		return chapterResult{index: job.index, entries: entries, report: report}, nil
	}

	// resultHandler runs in the pool's single collector goroutine — no mutex needed.
	resultHandler := func(res chapterResult) error {
		// Progress is page-driven (emitted from the image pools), not tracked here.
		// This handler only streams the per-chapter report and writes ordered ZIP
		// entries; it runs in the pool's single collector goroutine.

		// Notify caller immediately (for WebSocket streaming).
		if onChapterDone != nil {
			onChapterDone(res.report)
		}

		// Stream into the ZIP in order. Buffer out-of-order results in pending.
		if res.index == nextIndex {
			// Fast path: this is exactly the next chapter we need.
			nextIndex++
			if len(res.entries) == 0 {
				skippedChapters++
			} else {
				succeededChapters++
				for _, e := range res.entries {
					if err := writeZipEntry(zw, e.name, e.data); err != nil {
						return err
					}
				}
				res.entries = nil // free immediately
			}
			// Drain any contiguous pending chapters.
			return flushPending()
		}

		// Out-of-order: park it until its predecessors arrive.
		pending[res.index] = res
		return nil
	}

	cfg := workerpool.NewConfigChapterBuild(len(chapters))
	pool := workerpool.NewPool(ctx, cfg, workerFunc, nil)
	pool.SetResultHandler(resultHandler)

	if err := pool.Process(jobs); err != nil {
		cleanup()
		return "", 0, fmt.Errorf("archiver: %w", err)
	}

	if skippedChapters > 0 {
		zap.L().Warn("archiver: some chapters were skipped",
			zap.Int("skipped", skippedChapters),
			zap.Int("total", len(chapters)),
		)
	}

	if succeededChapters == 0 {
		cleanup()
		return "", 0, fmt.Errorf("archiver: all %d chapters failed, nothing to archive", len(chapters))
	}

	if onZipping != nil {
		onZipping()
	}
	zap.L().Info("archiver: all chapters built, writing outer ZIP",
		zap.Int("succeeded", succeededChapters),
		zap.Int("skipped", skippedChapters),
		zap.Int("total", len(chapters)),
		zap.String("format", string(format)),
	)

	if err := zw.Close(); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return "", 0, fmt.Errorf("archiver: finalise ZIP: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", 0, fmt.Errorf("archiver: close temp file: %w", err)
	}
	return tmpPath, succeededChapters, nil
}

// -----------------------------------------------------------------------
// Internal helpers
// -----------------------------------------------------------------------

// imageJob is the input unit fed to the image worker pool — a page together
// with its original slice index so results can be written back into the correct
// slot regardless of the order in which goroutines finish.
type imageJob struct {
	index      int
	page       toon.Page
	chapterURL string
}

// imageResult is what the image worker pool returns for each completed job.
type imageResult struct {
	index  int
	data   []byte
	reason string // non-empty when data is nil (fetch failed)
}

// fetchImages downloads the image at each page's URL concurrently using a
// worker pool and returns the raw bytes and per-image error reasons in
// page-number order.
//
// images[i] is nil when the fetch failed; reasons[i] holds the error string
// in that case (empty string means success).
// Concurrency is controlled by workerpool.NewConfigImageDownload: workers are
// capped at 8 to stay within typical CDN rate-limit thresholds.
// chapterURL is sent as the Referer header so CDNs that enforce hotlink
// protection (e.g. gg.asuracomic.net) accept the requests.
// onPageDone is called after every processed page, success or failure (may be nil).
func fetchImages(ctx context.Context, pages []toon.Page, chapterURL string, client *http.Client, onPageDone func()) ([][]byte, []string, error) {
	images := make([][]byte, len(pages))
	reasons := make([]string, len(pages))

	// Build the ordered job list.
	jobs := make([]imageJob, len(pages))
	for i, p := range pages {
		jobs[i] = imageJob{index: i, page: p, chapterURL: chapterURL}
	}

	cfg := workerpool.NewConfigImageDownload(len(pages))

	// workerFunc: fetch one image and return its index + raw bytes + error reason.
	// On error the image slot is left nil (logged as a warning) so the chapter
	// can still be built with whatever pages did succeed.
	workerFunc := func(ctx context.Context, job imageJob) (imageResult, error) {
		zap.L().Debug("archiver: fetching image",
			zap.Int("page", job.page.Number),
			zap.String("url", job.page.ImageURL),
		)

		data, err := fetchImageWithRetry(ctx, client, job.page, job.chapterURL)
		if err != nil {
			zap.L().Warn("archiver: image fetch failed, skipping page",
				zap.Int("page", job.page.Number),
				zap.String("url", job.page.ImageURL),
				zap.Error(err),
			)
			// Return a nil-data result with the reason — do not propagate the
			// error so the pool keeps running and the chapter is built from
			// the pages that succeeded.
			return imageResult{index: job.index, data: nil, reason: err.Error()}, nil
		}
		zap.L().Debug("archiver: image fetched",
			zap.Int("page", job.page.Number),
			zap.Int("bytes", len(data)),
		)
		return imageResult{index: job.index, data: data}, nil
	}

	// Use streaming mode (batchHandler = nil) so each result is available the
	// moment it arrives, letting us call onPageDone without waiting for a batch.
	pool := workerpool.NewPool(ctx, cfg, workerFunc, nil)

	// resultHandler: write each result into the pre-allocated slices.
	// nil data means the image fetch failed — the slot stays nil and the
	// reason string is stored so the chapter builder can report it.
	pool.SetResultHandler(func(res imageResult) error {
		images[res.index] = res.data
		reasons[res.index] = res.reason
		// Fire on every processed page (success or failure) so page-level progress
		// reaches its total even when some images fail.
		if onPageDone != nil {
			onPageDone()
		}
		return nil
	})

	if err := pool.Process(jobs); err != nil {
		return nil, nil, err
	}
	return images, reasons, nil
}

// imageFetchAttempts is the number of times fetchImageWithRetry tries to
// download a single image before giving up. A transient network hiccup or a
// momentary CDN 5xx should not lose the page permanently.
const imageFetchAttempts = 3

// imageRetryBaseDelay is the backoff before the first retry; it doubles on each
// subsequent attempt.
const imageRetryBaseDelay = 500 * time.Millisecond

// errPermanentFetch marks a fetch failure that must not be retried (e.g. an
// HTTP 4xx — retrying a missing/forbidden image just wastes time).
var errPermanentFetch = errors.New("permanent fetch failure")

// fetchImageWithRetry downloads a single page image, retrying on transient
// failures (network errors, timeouts, 5xx) up to imageFetchAttempts times with
// exponential backoff. A permanent failure (4xx) or a cancelled context stops
// the retry loop immediately. Each attempt gets its own 30s timeout so one slow
// hang cannot consume the whole budget.
func fetchImageWithRetry(ctx context.Context, client *http.Client, p toon.Page, referer string) ([]byte, error) {
	var lastErr error
	delay := imageRetryBaseDelay

	for attempt := 1; attempt <= imageFetchAttempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		data, err := fetchImage(attemptCtx, client, p, referer)
		cancel()
		if err == nil {
			return data, nil
		}
		lastErr = err

		// Do not retry permanent failures or a cancelled parent context.
		if errors.Is(err, errPermanentFetch) || ctx.Err() != nil {
			break
		}

		// No point sleeping after the final attempt.
		if attempt < imageFetchAttempts {
			zap.L().Debug("archiver: image fetch attempt failed, retrying",
				zap.Int("page", p.Number),
				zap.Int("attempt", attempt),
				zap.Error(err),
			)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
			delay *= 2
		}
	}
	return nil, lastErr
}

// fetchImage downloads a single page image with browser-like headers.
func fetchImage(ctx context.Context, client *http.Client, p toon.Page, referer string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.ImageURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/*,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.8")
	req.Header.Set("Referer", referer)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		// 4xx responses are permanent (missing / forbidden image) — mark them so
		// the retry loop does not waste attempts on them. 5xx and everything else
		// stays retryable.
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return nil, fmt.Errorf("%w: unexpected status %d", errPermanentFetch, resp.StatusCode)
		}
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return data, nil
}

// buildPDF assembles raw image bytes into a single PDF, one image per page.
// Each page is sized to the intrinsic image dimensions (points ≈ pixels at 72 dpi).
//
// Pipeline per image:
//  1. Decode via stdlib (WebP, JPEG, PNG via registered decoders).
//  2. Draw onto an 8-bit NRGBA canvas to normalise bit-depth.
//  3. Re-encode as JPEG (quality 90) — ~10x faster than PNG for manga pages.
//  4. Embed the JPEG into gopdf (natively supported, no extra processing).
//
// onPageBuilt, when non-nil, is called after each page is assembled — used to
// drive the "building" phase progress.
func buildPDF(images [][]byte, onPageBuilt func()) ([]byte, error) {
	zap.L().Debug("archiver: building PDF", zap.Int("images", len(images)))
	pdf := gopdf.GoPdf{}
	pdf.Start(gopdf.Config{Unit: gopdf.UnitPT, PageSize: *gopdf.PageSizeA4})

	for i, raw := range images {
		zap.L().Debug("archiver: embedding image in PDF", zap.Int("image", i+1), zap.Int("bytes", len(raw)))

		// Decode the source image — handles WebP, JPEG, PNG via init imports.
		img, _, err := image.Decode(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("image %d: decode: %w", i+1, err)
		}

		bounds := img.Bounds()
		pageSize := gopdf.Rect{W: float64(bounds.Dx()), H: float64(bounds.Dy())}

		// Normalise to 8-bit NRGBA before re-encoding.
		nrgba := image.NewNRGBA(bounds)
		draw.Draw(nrgba, bounds, img, bounds.Min, draw.Src)

		// Re-encode as JPEG — much faster than PNG for large photographic images.
		var jpgBuf bytes.Buffer
		if err := jpeg.Encode(&jpgBuf, nrgba, &jpeg.Options{Quality: 90}); err != nil {
			return nil, fmt.Errorf("image %d: jpeg encode: %w", i+1, err)
		}

		imgHolder, err := gopdf.ImageHolderByBytes(jpgBuf.Bytes())
		if err != nil {
			return nil, fmt.Errorf("image %d: create holder: %w", i+1, err)
		}

		pdf.AddPageWithOption(gopdf.PageOption{PageSize: &pageSize})
		if err := pdf.ImageByHolder(imgHolder, 0, 0, &pageSize); err != nil {
			return nil, fmt.Errorf("image %d: embed in PDF: %w", i+1, err)
		}
		if onPageBuilt != nil {
			onPageBuilt()
		}
	}

	var buf bytes.Buffer
	if _, err := pdf.WriteTo(&buf); err != nil {
		return nil, fmt.Errorf("write PDF: %w", err)
	}
	return buf.Bytes(), nil
}

// buildCBZ assembles raw image bytes into a CBZ file (a ZIP of images).
// onPageBuilt, when non-nil, is called after each page is added.
func buildCBZ(pages []toon.Page, images [][]byte, onPageBuilt func()) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i, img := range images {
		ext := imageExt(pages, i)
		name := fmt.Sprintf("%03d%s", i+1, ext)
		if err := writeZipEntry(zw, name, img); err != nil {
			return nil, err
		}
		if onPageBuilt != nil {
			onPageBuilt()
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("finalise CBZ: %w", err)
	}
	return buf.Bytes(), nil
}

// writeZipEntry adds a single file entry to zw.
func writeZipEntry(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return fmt.Errorf("archiver: create zip entry %q: %w", name, err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("archiver: write zip entry %q: %w", name, err)
	}
	return nil
}

// safeChapterName returns a filesystem-safe display name for a chapter,
// e.g. "Chapter 001" or "Chapter 074.5".
func safeChapterName(ch toon.Chapter) string {
	if ch.Number == float64(int(ch.Number)) {
		return fmt.Sprintf("Chapter %03.0f", ch.Number)
	}
	return fmt.Sprintf("Chapter %06.1f", ch.Number)
}

// imageExt returns the file extension for the i-th page by sniffing the URL,
// defaulting to ".webp".
func imageExt(pages []toon.Page, i int) string {
	if i >= len(pages) {
		return ".webp"
	}
	u := pages[i].ImageURL
	// Strip any query string / fragment before sniffing the extension —
	// CDN URLs carry a cache-buster (e.g. "a64678.webp?v=1778187277") and
	// path.Ext would otherwise return ".webp?v=1778187277", producing a
	// filename that is invalid on Windows (the "?" is illegal).
	if idx := strings.IndexAny(u, "?#"); idx != -1 {
		u = u[:idx]
	}
	ext := path.Ext(path.Base(u))
	if ext == "" {
		return ".webp"
	}
	return ext
}
