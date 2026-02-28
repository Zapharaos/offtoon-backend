// Package archiver converts a set of downloaded toon chapters into a
// distributable archive that users can save on their computer or phone.
//
// Supported output formats:
//
//   - FormatPDF   – one PDF per chapter, all bundled into a ZIP archive.
//   - FormatCBZ   – one CBZ (ZIP of images) per chapter, all bundled into a ZIP archive.
//   - FormatImages – raw image files in per-chapter sub-folders, bundled into a ZIP archive.
//
// In every case the outermost container is a single ZIP file so the caller
// always receives one []byte it can stream straight to the HTTP response.
package archiver

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	_ "image/png" // register PNG decoder
	"io"
	"net/http"
	"path"
	"strings"
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

// ProgressFunc is called after each page image is successfully downloaded.
// pagesDone is the cumulative number of images downloaded so far across all
// chapters; pagesTotal is the grand total across all chapters.
type ProgressFunc func(pagesDone, pagesTotal int)

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
// The returned []byte is the raw ZIP file ready to be written to an
// http.ResponseWriter.
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
func Build(ctx context.Context, chapters []toon.Chapter, slug string, format Format, httpClient *http.Client, onProgress ProgressFunc, onZipping func(), onChapterDone OnChapterDoneFunc) ([]byte, int, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}

	totalPages := 0
	for _, ch := range chapters {
		totalPages += len(ch.Pages)
	}

	// pagesDone is accessed only from the result handler (single goroutine),
	// so no mutex is needed.
	pagesDone := 0

	// Build jobs — one per chapter, tagged with their original index.
	jobs := make([]chapterJob, len(chapters))
	for i, ch := range chapters {
		jobs[i] = chapterJob{index: i, chapter: ch}
	}

	// workerFunc: fetch all images for a chapter then build the archive format.
	// This is the hot path: WebP decode + NRGBA normalise + JPEG encode runs
	// here in parallel across chapters.
	// On error the chapter is skipped (empty entries) so the rest of the
	// archive still completes — a single bad chapter never aborts the whole job.
	// A ChapterReport is always attached to the result so the resultHandler can
	// stream it to the caller immediately when this worker finishes.
	workerFunc := func(ctx context.Context, job chapterJob) (chapterResult, error) {
		ch := job.chapter
		chapterName := safeChapterName(ch)
		dirPrefix := path.Join(slug, chapterName)

		rawImages, reasons, err := fetchImages(ctx, ch.Pages, ch.URL, httpClient, nil)
		if err != nil {
			// fetchImages itself returning an error is rare (pool-level failure).
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

		// Build per-image reports and split into valid/skipped.
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

		switch format {
		case FormatPDF:
			pdfBytes, err := buildPDF(images)
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
			cbzBytes, err := buildCBZ(ch.Pages, images)
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
			}
		}

		// Build the chapter report. Only include image details when there were failures.
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

	// Collect results in a pre-allocated slice indexed by chapter position so
	// that we can write them to the ZIP in the original chapter order even
	// though workers complete out of order.
	ordered := make([]chapterResult, len(chapters))
	skippedChapters := 0

	// resultHandler runs in a single goroutine (the pool's collector), so
	// writing into ordered[] and incrementing counters is race-free.
	resultHandler := func(res chapterResult) error {
		ordered[res.index] = res

		if len(res.entries) == 0 {
			skippedChapters++
		} else if onProgress != nil {
			for range chapters[res.index].Pages {
				pagesDone++
				onProgress(pagesDone, totalPages)
			}
		}

		// Stream the chapter report to the caller immediately — before the
		// outer ZIP is written — so the frontend gets progressive updates.
		if onChapterDone != nil {
			onChapterDone(res.report)
		}

		return nil
	}

	cfg := workerpool.NewConfigChapterBuild(len(chapters))
	pool := workerpool.NewPool(ctx, cfg, workerFunc, nil)
	pool.SetResultHandler(resultHandler)

	if err := pool.Process(jobs); err != nil {
		return nil, 0, fmt.Errorf("archiver: %w", err)
	}

	if skippedChapters > 0 {
		zap.L().Warn("archiver: some chapters were skipped",
			zap.Int("skipped", skippedChapters),
			zap.Int("total", len(chapters)),
		)
	}

	// All chapters that could be built are ready. Fail only when every single
	// chapter was skipped — there is nothing useful to put in the archive.
	succeededChapters := len(chapters) - skippedChapters
	if succeededChapters == 0 {
		return nil, 0, fmt.Errorf("archiver: all %d chapters failed, nothing to archive", len(chapters))
	}

	// Write chapters into the outer ZIP in order, silently skipping any that
	// produced no entries (already warned above).
	if onZipping != nil {
		onZipping()
	}
	zap.L().Info("archiver: all chapters built, writing outer ZIP",
		zap.Int("succeeded", succeededChapters),
		zap.Int("skipped", skippedChapters),
		zap.Int("total", len(chapters)),
		zap.String("format", string(format)),
	)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	for _, res := range ordered {
		if len(res.entries) == 0 {
			continue // chapter was skipped — already warned
		}
		for _, e := range res.entries {
			if err := writeZipEntry(zw, e.name, e.data); err != nil {
				return nil, 0, err
			}
		}
	}

	if err := zw.Close(); err != nil {
		return nil, 0, fmt.Errorf("archiver: finalise ZIP: %w", err)
	}
	return buf.Bytes(), succeededChapters, nil
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
// onPageDone is called after every successful image download (may be nil).
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
		fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		data, err := fetchImage(fetchCtx, client, job.page, job.chapterURL)
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
		if res.data != nil && onPageDone != nil {
			onPageDone()
		}
		return nil
	})

	if err := pool.Process(jobs); err != nil {
		return nil, nil, err
	}
	return images, reasons, nil
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
func buildPDF(images [][]byte) ([]byte, error) {
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
	}

	var buf bytes.Buffer
	if _, err := pdf.WriteTo(&buf); err != nil {
		return nil, fmt.Errorf("write PDF: %w", err)
	}
	return buf.Bytes(), nil
}

// buildCBZ assembles raw image bytes into a CBZ file (a ZIP of images).
func buildCBZ(pages []toon.Page, images [][]byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i, img := range images {
		ext := imageExt(pages, i)
		name := fmt.Sprintf("%03d%s", i+1, ext)
		if err := writeZipEntry(zw, name, img); err != nil {
			return nil, err
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
	ext := path.Ext(path.Base(u))
	if ext == "" {
		return ".webp"
	}
	return ext
}
