package files

import (
	"bytes"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"mime"
	"net/http"
	"strings"

	_ "golang.org/x/image/webp"
)

const MetadataPrefixLimit = 256 * 1024

// Metadata describes physical content independently of any virtual name.
type Metadata struct {
	MIME             string `json:"mime_type"`
	Type             string `json:"type"`
	Subtype          string `json:"subtype"`
	Format           string `json:"format,omitempty"`
	Width            *int   `json:"width,omitempty"`
	Height           *int   `json:"height,omitempty"`
	Status           string `json:"status"`
	Basis            string `json:"basis"`
	ExtractorVersion string `json:"extractor_version,omitempty"`
}

// MetadataExtractor runs only on a bounded prefix. New format strategies can
// register without changing the upload/checksum pipeline.
type MetadataExtractor interface {
	Name() string
	Supports(string) bool
	Extract([]byte, *Metadata) error
}

type MetadataRegistry struct{ extractors []MetadataExtractor }

func NewMetadataRegistry() *MetadataRegistry { return &MetadataRegistry{} }

func (r *MetadataRegistry) Register(extractor MetadataExtractor) error {
	if extractor == nil || extractor.Name() == "" {
		return errors.New("files: invalid metadata extractor")
	}
	for _, current := range r.extractors {
		if current.Name() == extractor.Name() {
			return errors.New("files: duplicate metadata extractor")
		}
	}
	r.extractors = append(r.extractors, extractor)
	return nil
}

// Detect never selects an extractor from a filename or declared MIME.
func (r *MetadataRegistry) Detect(prefix []byte) Metadata {
	if len(prefix) > MetadataPrefixLimit {
		prefix = prefix[:MetadataPrefixLimit]
	}
	mimeType, _, err := mime.ParseMediaType(http.DetectContentType(prefix))
	if err != nil {
		mimeType = "application/octet-stream"
	}
	parts := strings.SplitN(mimeType, "/", 2)
	meta := Metadata{MIME: mimeType, Type: parts[0], Subtype: parts[1], Status: "unsupported", Basis: "bounded_header"}
	if mimeType == "application/octet-stream" {
		meta.Status = "unknown"
	}
	for _, extractor := range r.extractors {
		if extractor.Supports(mimeType) {
			if err := extractor.Extract(prefix, &meta); err != nil {
				meta.Status = "unavailable"
			}
			break
		}
	}
	return meta
}

// ImageMetadata reads raster image configuration only, not decoded pixels.
type ImageMetadata struct{}

func (ImageMetadata) Name() string { return "raster-config-v1" }
func (ImageMetadata) Supports(mimeType string) bool {
	return mimeType == "image/png" || mimeType == "image/jpeg" || mimeType == "image/gif" || mimeType == "image/webp"
}
func (ImageMetadata) Extract(prefix []byte, meta *Metadata) error {
	config, format, err := image.DecodeConfig(bytes.NewReader(prefix))
	if err != nil {
		return err
	}
	meta.Format, meta.Width, meta.Height = format, &config.Width, &config.Height
	meta.Status, meta.ExtractorVersion = "extracted", "raster-config-v1"
	return nil
}
