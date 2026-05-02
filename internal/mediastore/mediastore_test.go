package mediastore_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/mediastore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtFromMIME(t *testing.T) {
	cases := []struct{ mime, want string }{
		{"image/jpeg", ".jpg"},
		{"image/png", ".png"},
		{"image/webp", ".webp"},
		{"image/gif", ".gif"},
		{"video/mp4", ".mp4"},
		{"audio/ogg", ".ogg"},
		{"audio/mpeg", ".mp3"},
		{"application/pdf", ".pdf"},
		{"application/zip", ".zip"},
		{"application/octet-stream", ".bin"},
		{"unknown/type", ".bin"},
		{"", ".bin"},
	}
	for _, tc := range cases {
		t.Run(tc.mime, func(t *testing.T) {
			assert.Equal(t, tc.want, mediastore.ExtFromMIME(tc.mime))
		})
	}
}

func TestPath(t *testing.T) {
	s := mediastore.New("/tmp/root")
	got := s.Path("abcdef0123456789", ".jpg")
	assert.Equal(t, "/tmp/root/ab/abcdef0123456789.jpg", got)
}

func TestWriteRoundTrip(t *testing.T) {
	root := t.TempDir()
	s := mediastore.New(root)
	body := []byte("hello world media bytes")
	mime := "image/jpeg"

	sha, path, err := s.Write(context.Background(), body, mime)
	require.NoError(t, err)

	expSum := sha256.Sum256(body)
	expHex := hex.EncodeToString(expSum[:])
	assert.Equal(t, expHex, sha)
	assert.Equal(t, filepath.Join(root, sha[0:2], sha+".jpg"), path)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, body, got)
}

func TestWriteIdempotent(t *testing.T) {
	root := t.TempDir()
	s := mediastore.New(root)
	body := []byte("same bytes twice")
	mime := "image/png"

	sha1, path1, err := s.Write(context.Background(), body, mime)
	require.NoError(t, err)
	st1, err := os.Stat(path1)
	require.NoError(t, err)
	mtime1 := st1.ModTime()

	sha2, path2, err := s.Write(context.Background(), body, mime)
	require.NoError(t, err)
	assert.Equal(t, sha1, sha2)
	assert.Equal(t, path1, path2)

	st2, err := os.Stat(path2)
	require.NoError(t, err)
	assert.Equal(t, mtime1, st2.ModTime(), "second write should be a no-op")
}

func TestWriteCreatesParentDir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nested", "deeper", "media")
	s := mediastore.New(root)
	_, _, err := s.Write(context.Background(), []byte("x"), "image/jpeg")
	require.NoError(t, err, "Write must MkdirAll the parent dir")
}
