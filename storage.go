package storage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

var (
	ErrFileTooLarge    = errors.New("file exceeds maximum allowed upload size")
	ErrInvalidFileType = errors.New("invalid file type: only JPG and PNG images are permitted")
	ErrEmptyFile       = errors.New("file is empty")
)

type Storage struct {
	baseDir      string
	maxSizeBytes int64
}

type SavedImage struct {
	Path              string
	OriginalReference string
	MIMEType          string
	Size              int64
	SHA256            string
}

func NewStorage(baseDir string, maxUploadSizeMB int64) (*Storage, error) {
	for _, dir := range []string{filepath.Join(baseDir, "originals"), filepath.Join(baseDir, "public", "reports")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create upload directory %s: %w", dir, err)
		}
	}

	return &Storage{
		baseDir:      baseDir,
		maxSizeBytes: maxUploadSizeMB * 1024 * 1024,
	}, nil
}

func (s *Storage) SaveImage(reader io.Reader, _ string) (*SavedImage, error) {
	data, err := io.ReadAll(io.LimitReader(reader, s.maxSizeBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read image: %w", err)
	}
	if len(data) == 0 {
		return nil, ErrEmptyFile
	}
	if int64(len(data)) > s.maxSizeBytes {
		return nil, ErrFileTooLarge
	}

	headerLen := len(data)
	if headerLen > 512 {
		headerLen = 512
	}
	mimeType := http.DetectContentType(data[:headerLen])
	var ext string
	switch {
	case strings.HasPrefix(mimeType, "image/jpeg"):
		ext = ".jpg"
	case strings.HasPrefix(mimeType, "image/png"):
		ext = ".png"
	default:
		return nil, ErrInvalidFileType
	}

	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 12000 || config.Height > 12000 ||
		int64(config.Width)*int64(config.Height) > 16_000_000 {
		return nil, errors.New("image is invalid or exceeds supported dimensions")
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("corrupted image data: %w", err)
	}

	id := uuid.NewString()
	originalFilename := "report_" + id + ext
	originalPath := filepath.Join(s.baseDir, "originals", originalFilename)
	if err := os.WriteFile(originalPath, data, 0600); err != nil {
		return nil, fmt.Errorf("failed to preserve original image: %w", err)
	}
	previewFilename := originalFilename
	previewPath := filepath.Join(s.baseDir, "public", "reports", previewFilename)
	preview, err := os.OpenFile(previewPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		_ = os.Remove(originalPath)
		return nil, fmt.Errorf("failed to create display image: %w", err)
	}
	if ext == ".jpg" {
		err = jpeg.Encode(preview, decoded, &jpeg.Options{Quality: 82})
	} else {
		err = (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(preview, decoded)
	}
	if err == nil {
		err = preview.Sync()
	}
	closeErr := preview.Close()
	if err != nil || closeErr != nil {
		_ = os.Remove(previewPath)
		_ = os.Remove(originalPath)
		if err != nil {
			return nil, fmt.Errorf("failed to create display image: %w", err)
		}
		return nil, fmt.Errorf("failed to close display image: %w", closeErr)
	}
	hash := sha256.Sum256(data)
	return &SavedImage{
		Path:              "/uploads/reports/" + previewFilename,
		OriginalReference: "originals/" + originalFilename,
		MIMEType:          mimeType,
		Size:              int64(len(data)),
		SHA256:            hex.EncodeToString(hash[:]),
	}, nil
}

// DeleteFile removes the public preview and its paired private original.
func (s *Storage) DeleteFile(publicPath string) error {
	filename := filepath.Base(publicPath)
	if !strings.HasPrefix(filename, "report_") || (filepath.Ext(filename) != ".jpg" && filepath.Ext(filename) != ".png") {
		return errors.New("invalid stored image reference")
	}
	previewPath := filepath.Join(s.baseDir, "public", "reports", filename)
	originalPath := filepath.Join(s.baseDir, "originals", filename)
	previewErr := os.Remove(previewPath)
	originalErr := os.Remove(originalPath)
	if previewErr != nil && !errors.Is(previewErr, os.ErrNotExist) {
		return previewErr
	}
	if originalErr != nil && !errors.Is(originalErr, os.ErrNotExist) {
		return originalErr
	}
	return nil
}
