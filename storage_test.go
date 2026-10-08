package storage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func makePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{R: 20, G: 80, B: 120, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	return encoded.Bytes()
}

func TestSaveImagePreservesOriginalAndCreatesPreview(t *testing.T) {
	root := t.TempDir()
	store, err := NewStorage(root, 1)
	if err != nil {
		t.Fatalf("create storage: %v", err)
	}
	original := makePNG(t, 4, 4)

	saved, err := store.SaveImage(bytes.NewReader(original), "../../untrusted.png")
	if err != nil {
		t.Fatalf("save PNG: %v", err)
	}
	if saved.MIMEType != "image/png" || saved.Size != int64(len(original)) {
		t.Fatalf("unexpected image metadata: %+v", saved)
	}
	digest := sha256.Sum256(original)
	if saved.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("hash does not match original bytes: %s", saved.SHA256)
	}

	privateOriginal := filepath.Join(root, filepath.FromSlash(saved.OriginalReference))
	storedOriginal, err := os.ReadFile(privateOriginal)
	if err != nil {
		t.Fatalf("read private original: %v", err)
	}
	if !bytes.Equal(storedOriginal, original) {
		t.Fatal("private evidence original was modified")
	}
	info, err := os.Stat(privateOriginal)
	if err != nil {
		t.Fatalf("stat private original: %v", err)
	}
	if info.Mode().Perm()&0077 != 0 {
		t.Fatalf("private original permissions are too broad: %o", info.Mode().Perm())
	}
	previewPath := filepath.Join(root, "public", "reports", filepath.Base(saved.Path))
	preview, err := os.ReadFile(previewPath)
	if err != nil {
		t.Fatalf("read public preview: %v", err)
	}
	if _, _, err := image.Decode(bytes.NewReader(preview)); err != nil {
		t.Fatalf("display preview is not a valid image: %v", err)
	}
	if bytes.Equal(preview, original) {
		t.Fatal("preview should be re-encoded separately from the original")
	}
}

func TestSaveImageAcceptsJPEG(t *testing.T) {
	store, err := NewStorage(t.TempDir(), 1)
	if err != nil {
		t.Fatalf("create storage: %v", err)
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 4, 4)), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode JPEG: %v", err)
	}
	saved, err := store.SaveImage(bytes.NewReader(encoded.Bytes()), "scene.jpg")
	if err != nil {
		t.Fatalf("save JPEG: %v", err)
	}
	if saved.MIMEType != "image/jpeg" || filepath.Ext(saved.Path) != ".jpg" {
		t.Fatalf("unexpected JPEG metadata: %+v", saved)
	}
}

func TestSaveImageRejectsInvalidAndOversizedFiles(t *testing.T) {
	store, err := NewStorage(t.TempDir(), 1)
	if err != nil {
		t.Fatalf("create storage: %v", err)
	}

	for _, test := range []struct {
		name string
		data []byte
		want error
	}{
		{name: "empty", data: nil, want: ErrEmptyFile},
		{name: "unsupported", data: []byte("not an image"), want: ErrInvalidFileType},
		{name: "oversized", data: bytes.Repeat([]byte("x"), 1024*1024+1), want: ErrFileTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := store.SaveImage(bytes.NewReader(test.data), "image.png")
			if !errors.Is(err, test.want) {
				t.Fatalf("expected %v, got %v", test.want, err)
			}
		})
	}

	t.Run("truncated PNG", func(t *testing.T) {
		data := makePNG(t, 4, 4)
		_, err := store.SaveImage(bytes.NewReader(data[:len(data)-8]), "image.png")
		if err == nil {
			t.Fatal("expected truncated image to be rejected")
		}
	})
}
