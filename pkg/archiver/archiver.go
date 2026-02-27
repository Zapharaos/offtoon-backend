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

// Build downloads all page images for every chapter and assembles them into a
// single ZIP archive whose internal layout depends on format:
//
//	FormatPDF:    <toonSlug>/Chapter 001.pdf
//	FormatCBZ:    <toonSlug>/Chapter 001.cbz
//	FormatImages: <toonSlug>/Chapter 001/<page>.webp
//
// The returned []byte is the raw ZIP file ready to be written to an
// http.ResponseWriter.
// onProgress is optional (may be nil); it is called after every downloaded
// page image so callers can report live progress to connected clients.
func Build(chapters []toon.Chapter, slug string, format Format, httpClient *http.Client, onProgress ProgressFunc) ([]byte, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}

	// Count the total number of pages across all chapters up front so the
	// progress callback can report a meaningful percentage.
	totalPages := 0
	for _, ch := range chapters {
		totalPages += len(ch.Pages)
	}
	pagesDone := 0

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	for _, ch := range chapters {
		// Download all page images for this chapter.
		// Use the chapter page URL as Referer so the CDN accepts the requests.
		images, err := fetchImages(ch.Pages, ch.URL, httpClient, func() {
			pagesDone++
			if onProgress != nil {
				onProgress(pagesDone, totalPages)
			}
		})
		if err != nil {
			return nil, fmt.Errorf("archiver: chapter %s: %w", ch.ID, err)
		}

		chapterName := safeChapterName(ch)
		dirPrefix := path.Join(slug, chapterName)

		switch format {
		case FormatPDF:
			pdfBytes, err := buildPDF(images)
			if err != nil {
				return nil, fmt.Errorf("archiver: chapter %s: build PDF: %w", ch.ID, err)
			}
			if err := writeZipEntry(zw, dirPrefix+".pdf", pdfBytes); err != nil {
				return nil, err
			}

		case FormatCBZ:
			cbzBytes, err := buildCBZ(ch.Pages, images)
			if err != nil {
				return nil, fmt.Errorf("archiver: chapter %s: build CBZ: %w", ch.ID, err)
			}
			if err := writeZipEntry(zw, dirPrefix+".cbz", cbzBytes); err != nil {
				return nil, err
			}

		case FormatImages:
			for i, img := range images {
				ext := imageExt(ch.Pages, i)
				name := fmt.Sprintf("%s/%03d%s", dirPrefix, i+1, ext)
				if err := writeZipEntry(zw, name, img); err != nil {
					return nil, err
				}
			}
		}
	}

	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("archiver: finalise ZIP: %w", err)
	}
	return buf.Bytes(), nil
}

// -----------------------------------------------------------------------
// Internal helpers
// -----------------------------------------------------------------------

// fetchImages downloads the image at each page's URL and returns the raw bytes
// in page-number order.
// chapterURL is sent as the Referer header so CDNs that enforce hotlink
// protection (e.g. gg.asuracomic.net) accept the requests.
// A per-image timeout of 30 s prevents a single slow image from hanging the
// entire archive build.
// onPageDone is called after every successful image download (may be nil).
func fetchImages(pages []toon.Page, chapterURL string, client *http.Client, onPageDone func()) ([][]byte, error) {
	images := make([][]byte, len(pages))
	for i, p := range pages {
		zap.L().Debug("archiver: fetching image",
			zap.Int("page", p.Number),
			zap.String("url", p.ImageURL),
		)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		data, err := fetchImage(ctx, client, p, chapterURL)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("fetch page %d: %w", p.Number, err)
		}
		zap.L().Debug("archiver: image fetched", zap.Int("page", p.Number), zap.Int("bytes", len(data)))
		images[i] = data
		if onPageDone != nil {
			onPageDone()
		}
	}
	return images, nil
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
