package toonruntime

import (
	"encoding/json"

	"github.com/Zapharaos/offtoon-backend/internal/toon"
	"github.com/Zapharaos/offtoon-backend/pkg/archiver"
	"github.com/Zapharaos/offtoon-backend/pkg/wsruntime"
)

type PacketType string

const (
	PacketTypeInit          PacketType = "init"
	PacketTypeFatal         PacketType = "fatal"
	PacketTypeProgress      PacketType = "progress"
	PacketTypeArchiving     PacketType = "archiving"
	PacketTypeZipping       PacketType = "zipping"
	PacketTypeChapterReport PacketType = "chapter_report"
	PacketTypeCompleted     PacketType = "completed"
)

// packetSpec is a struct that contains all the possible packets
// WARNING : used for swagger doc and generation
type packetSpec struct {
	Packet              packet              `json:"packet"`
	PacketInit          PacketInit          `json:"packetInit"`
	PacketFatal         PacketFatal         `json:"packetFatal"`
	PacketProgress      PacketProgress      `json:"packetProgress"`
	PacketArchiving     PacketArchiving     `json:"packetArchiving"`
	PacketZipping       PacketZipping       `json:"packetZipping"`
	PacketChapterReport PacketChapterReport `json:"packetChapterReport"`
	PacketCompleted     PacketCompleted     `json:"packetCompleted"`
}

// Packet interface
type Packet interface {
	ToJSON() ([]byte, error)
}

// Packet is a generic packet
type packet struct {
	Type PacketType `json:"type"`
	Hash string     `json:"hash"`
}

// ToJSON returns the JSON representation of the packet
func (p *packet) ToJSON() ([]byte, error) {
	return json.Marshal(p)
}

// --------------------------------------------
// --------------------------------------------
// --------------------------------------------

// PacketInit is a packet to initialize the set
type PacketInit struct {
	packet
}

func NewPacketInit() *PacketInit {
	return &PacketInit{
		packet: packet{
			Type: PacketTypeInit,
		},
	}
}

func (p *PacketInit) ToJSON() ([]byte, error) {
	return json.Marshal(p)
}

// PacketFatal is a packet to send a fatal internal error
type PacketFatal struct {
	packet
	Step    toon.FetchErrorStep `json:"step"`
	Message string              `json:"message"`
}

// NewPacketFatal creates a new PacketFatal
func NewPacketFatal(step toon.FetchErrorStep, message string) *PacketFatal {
	return &PacketFatal{
		packet: packet{
			Type: PacketTypeFatal,
		},
		Step:    step,
		Message: message,
	}
}

// ToJSON returns the JSON representation of the packet
func (p *PacketFatal) ToJSON() ([]byte, error) {
	return json.Marshal(p)
}

// --------------------------------------------
// --------------------------------------------
// --------------------------------------------

// PacketProgress is a packet sent to report download progress to connected clients.
//
// A download runs two pipelined page-level phases: "downloading" (fetching page
// images) then "building" (assembling them into the chosen format — the slow,
// CPU-bound step, especially for PDF). Phase says which one; Total is the number
// of pages known so far (it grows as chapters resolve); Done is the number of
// pages processed in that phase; Items is empty (per-chapter outcomes are
// streamed via PacketChapterReport instead).
type PacketProgress struct {
	packet
	Phase string `json:"phase"` // "downloading" or "building"
	Total int    `json:"total"` // Total pages known so far
	Done  int    `json:"done"`  // Pages processed so far in this phase
	Items []any  `json:"items"` // Reserved; currently always empty
}

// Progress phase labels sent in PacketProgress.Phase.
const (
	// ProgressPhaseDownloading tracks page image downloads.
	ProgressPhaseDownloading = "downloading"
	// ProgressPhaseBuilding tracks archive assembly (CBZ/PDF/image build).
	ProgressPhaseBuilding = "building"
)

// NewPacketProgress creates a new PacketProgress from a wsruntime.Progress snapshot.
// p.Phase is forwarded as-is; use ProgressPhaseDownloading / ProgressPhaseBuilding.
func NewPacketProgress(p wsruntime.Progress) *PacketProgress {
	return &PacketProgress{
		packet: packet{Type: PacketTypeProgress},
		Phase:  p.Phase,
		Total:  p.Total,
		Done:   p.Done,
		Items:  p.Items,
	}
}

// ToJSON returns the JSON representation of the packet.
func (p *PacketProgress) ToJSON() ([]byte, error) {
	return json.Marshal(p)
}

// Droppable marks progress as safe to discard under back-pressure: it is a
// counter, so the next packet supersedes this one entirely. A long download
// emits these far faster than a browser consumes them, and dropping the excess
// is what leaves room for the chapter reports and the completion packet.
//
// It implements wsruntime.Droppable.
func (p *PacketProgress) Droppable() bool { return true }

// --------------------------------------------
// --------------------------------------------
// --------------------------------------------

// PacketArchiving signals that all chapter metadata has been downloaded and
// the server is now assembling the final archive (image fetch + PDF/CBZ build).
// The frontend can use this to display a dedicated "archiving…" state instead
// of the generic progress spinner, so users understand why there is no more
// chapter-level progress.
type PacketArchiving struct {
	packet
	Chapters int    `json:"chapters"` // Total number of chapters being archived
	Format   string `json:"format"`   // Archive format: "pdf", "cbz", or "images"
}

// NewPacketArchiving creates a new PacketArchiving.
func NewPacketArchiving(chapters int, format string) *PacketArchiving {
	return &PacketArchiving{
		packet:   packet{Type: PacketTypeArchiving},
		Chapters: chapters,
		Format:   format,
	}
}

// ToJSON returns the JSON representation of the packet.
func (p *PacketArchiving) ToJSON() ([]byte, error) {
	return json.Marshal(p)
}

// --------------------------------------------
// --------------------------------------------
// --------------------------------------------

// PacketZipping signals that all chapters have been built and the server is now
// writing them into the final outer ZIP archive. This phase is purely CPU/IO-bound
// (no more network activity) and can take several minutes for large downloads
// (e.g. ~2 min for a ~2 GB 151-chapter archive). The frontend should display a
// dedicated "zipping…" state so users understand why progress has stalled.
type PacketZipping struct {
	packet
	Chapters int    `json:"chapters"` // Total number of chapters being zipped
	Format   string `json:"format"`   // Archive format: "pdf", "cbz", or "images"
}

// NewPacketZipping creates a new PacketZipping.
func NewPacketZipping(chapters int, format string) *PacketZipping {
	return &PacketZipping{
		packet:   packet{Type: PacketTypeZipping},
		Chapters: chapters,
		Format:   format,
	}
}

// ToJSON returns the JSON representation of the packet.
func (p *PacketZipping) ToJSON() ([]byte, error) {
	return json.Marshal(p)
}

// --------------------------------------------
// --------------------------------------------
// --------------------------------------------

// PacketChapterReport streams the build outcome for one chapter as soon as
// its worker finishes — well before the outer ZIP is written. This gives the
// frontend a progressive, per-chapter status list throughout the archiving phase.
//
// On ChapterStatusSuccess the Images field is omitted (all pages succeeded).
// On ChapterStatusIncomplete or ChapterStatusFailed, Images lists every page
// with its individual status and failure reason so the user knows exactly
// which pages are missing and why.
type PacketChapterReport struct {
	packet
	archiver.ChapterReport // embedded — all fields promoted to JSON top-level
}

// NewPacketChapterReport creates a new PacketChapterReport from an archiver.ChapterReport.
func NewPacketChapterReport(report archiver.ChapterReport) *PacketChapterReport {
	return &PacketChapterReport{
		packet:        packet{Type: PacketTypeChapterReport},
		ChapterReport: report,
	}
}

// ToJSON returns the JSON representation of the packet.
func (p *PacketChapterReport) ToJSON() ([]byte, error) {
	return json.Marshal(p)
}

// --------------------------------------------
// --------------------------------------------
// --------------------------------------------

// PacketCompleted signals that a download job has finished successfully.
type PacketCompleted struct {
	packet
	Total      int    `json:"total"`       // Total chapters that were downloaded
	ArchiveURL string `json:"archive_url"` // URL to GET the assembled archive file
}

// NewPacketCompleted creates a new PacketCompleted.
func NewPacketCompleted(total int, archiveURL string) *PacketCompleted {
	return &PacketCompleted{
		packet:     packet{Type: PacketTypeCompleted},
		Total:      total,
		ArchiveURL: archiveURL,
	}
}

// ToJSON returns the JSON representation of the packet.
func (p *PacketCompleted) ToJSON() ([]byte, error) {
	return json.Marshal(p)
}
