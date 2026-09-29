package storage

import (
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

var (
	ErrFileTooLarge    = errors.New("file exceeds maximum allowed upload size")
	ErrInvalidFileType = errors.New("invalid file type: only JPG, PNG, and WebP images are permitted")
	ErrEmptyFile       = errors.New("file is empty")
)

type Storage struct {
	baseDir     string
	maxSizeBytes int64
}

func NewStorage(baseDir string, maxUploadSizeMB int64) (*Storage, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create upload directory %s: %w", baseDir, err)
	}

	return &Storage{
		baseDir:     baseDir,
		maxSizeBytes: maxUploadSizeMB * 1024 * 1024,
	}, nil
}

// SaveImage validates the file header, MIME type, and image structure, then saves it to disk
func (s *Storage) SaveImage(reader io.Reader, originalFilename string) (string, error) {
	// Read first 512 bytes for MIME type sniffing
	buf := make([]byte, 512)
	n, err := io.ReadFull(reader, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", fmt.Errorf("failed to read file header: %w", err)
	}
	if n == 0 {
		return "", ErrEmptyFile
	}

	mimeType := http.DetectContentType(buf[:n])
	var ext string
	switch {
	case strings.HasPrefix(mimeType, "image/jpeg"):
		ext = ".jpg"
	case strings.HasPrefix(mimeType, "image/png"):
		ext = ".png"
	case strings.HasPrefix(mimeType, "image/webp"):
		ext = ".webp"
	default:
		// Also inspect file extension as fallback if sniffing was ambiguous
		origExt := strings.ToLower(filepath.Ext(originalFilename))
		if origExt == ".jpg" || origExt == ".jpeg" {
			ext = ".jpg"
		} else if origExt == ".png" {
			ext = ".png"
		} else if origExt == ".webp" {
			ext = ".webp"
		} else {
			return "", ErrInvalidFileType
		}
	}

	// Prepare safe output filename
	filename := fmt.Sprintf("report_%s%s", uuid.New().String(), ext)
	dstPath := filepath.Join(s.baseDir, filename)

	dst, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return "", fmt.Errorf("failed to create destination file: %w", err)
	}
	defer dst.Close()

	// Write the initial sniffed bytes
	if _, err := dst.Write(buf[:n]); err != nil {
		os.Remove(dstPath)
		return "", err
	}

	// Copy the remainder with a LimitedReader to enforce maxSizeBytes
	limitedReader := io.LimitReader(reader, s.maxSizeBytes-int64(n)+1)
	written, err := io.Copy(dst, limitedReader)
	if err != nil {
		os.Remove(dstPath)
		return "", fmt.Errorf("failed to write upload content: %w", err)
	}

	totalWritten := int64(n) + written
	if totalWritten > s.maxSizeBytes {
		os.Remove(dstPath)
		return "", ErrFileTooLarge
	}

	// Reopen and verify valid image decoding
	if ext == ".jpg" || ext == ".png" {
		checkFile, err := os.Open(dstPath)
		if err == nil {
			_, _, decodeErr := image.DecodeConfig(checkFile)
			checkFile.Close()
			if decodeErr != nil {
				os.Remove(dstPath)
				return "", fmt.Errorf("corrupted image data: %w", decodeErr)
			}
		}
	}

	// Return public URL relative to web root
	return "/uploads/" + filename, nil
}

// DeleteFile removes an uploaded file by its public path
func (s *Storage) DeleteFile(publicPath string) error {
	filename := filepath.Base(publicPath)
	fullPath := filepath.Join(s.baseDir, filename)
	return os.Remove(fullPath)
}
