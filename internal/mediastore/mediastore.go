// Package mediastore owns on-disk media files for the daemon. Files are
// content-addressed by SHA-256: <root>/<sha[0:2]>/<sha>.<ext>. Identical
// bytes uploaded by two different messages share one disk file.
package mediastore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

type Store struct {
	root string
}

// New constructs a Store rooted at root. The directory tree is created lazily
// on first Write.
func New(root string) *Store {
	return &Store{root: root}
}

// Write hashes body, derives the on-disk path from sha + ExtFromMIME(mime),
// ensures the parent dir exists, and atomically writes the file via a .tmp
// sibling + rename. If the destination file already exists at the right size,
// no rewrite (idempotent on re-upload of identical bytes).
//
// Returns (sha256-hex, full-path, error).
func (s *Store) Write(ctx context.Context, body []byte, mime string) (string, string, error) {
	_ = ctx
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	ext := ExtFromMIME(mime)
	path := s.Path(sha, ext)

	if st, err := os.Stat(path); err == nil && st.Size() == int64(len(body)) {
		return sha, path, nil
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", "", fmt.Errorf("mediastore mkdir: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o640); err != nil {
		return "", "", fmt.Errorf("mediastore write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", "", fmt.Errorf("mediastore rename: %w", err)
	}
	return sha, path, nil
}

// Path returns the on-disk path for a given sha + extension. No IO.
func (s *Store) Path(sha, ext string) string {
	prefix := ""
	if len(sha) >= 2 {
		prefix = sha[:2]
	}
	return filepath.Join(s.root, prefix, sha+ext)
}

// ExtFromMIME maps a MIME type to a file extension (with leading dot).
// Unknown / empty types return ".bin".
func ExtFromMIME(mime string) string {
	switch mime {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/heic":
		return ".heic"
	case "video/mp4":
		return ".mp4"
	case "video/quicktime":
		return ".mov"
	case "video/webm":
		return ".webm"
	case "audio/ogg", "audio/ogg; codecs=opus":
		return ".ogg"
	case "audio/mpeg":
		return ".mp3"
	case "audio/aac":
		return ".aac"
	case "audio/wav":
		return ".wav"
	case "application/pdf":
		return ".pdf"
	case "application/zip":
		return ".zip"
	case "application/x-tar":
		return ".tar"
	case "text/plain":
		return ".txt"
	default:
		return ".bin"
	}
}
