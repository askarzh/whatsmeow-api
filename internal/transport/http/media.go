package http

import (
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/go-chi/chi/v5"
)

// SendMediaHandler handles POST /v1/media (multipart/form-data).
//
// Form fields:
//
//	chat_jid (text, required)
//	kind     (text, required: "image" | "document")
//	caption  (text, optional, ≤ 4096 bytes)
//	filename (text, required if kind=document)
//	file     (file, required, body capped at maxBodyBytes)
func SendMediaHandler(svc service.Service, maxBodyBytes int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

		if err := r.ParseMultipartForm(maxBodyBytes); err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "multipart parse: "+err.Error())
			return
		}

		chatJID := r.FormValue("chat_jid")
		kind := r.FormValue("kind")
		caption := r.FormValue("caption")
		filename := r.FormValue("filename")

		filePart, fileHeader, err := r.FormFile("file")
		if err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "file is required")
			return
		}
		defer filePart.Close()

		body, err := io.ReadAll(filePart)
		if err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "read file: "+err.Error())
			return
		}

		mimeType := pickMIME(fileHeader, filename)

		req := service.SendMediaRequest{
			ChatJID:  chatJID,
			Kind:     kind,
			Caption:  caption,
			Filename: filename,
			MIME:     mimeType,
			Body:     body,
		}
		msg, err := svc.SendMedia(r.Context(), req)
		if err != nil {
			switch {
			case errors.Is(err, service.ErrInvalidRequest):
				WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
			case errors.Is(err, waclient.ErrNotConnected):
				WriteProblem(w, http.StatusConflict, "wa.not_connected", err.Error())
			default:
				WriteProblem(w, http.StatusInternalServerError, "wa.send_failed", err.Error())
			}
			return
		}

		writeJSON(w, http.StatusCreated, map[string]any{
			"id":       msg.ID,
			"chat_jid": msg.ChatJID,
			"ts":       msg.Timestamp.UTC().Format(time.RFC3339),
		})
	})
}

func pickMIME(fh *multipart.FileHeader, filename string) string {
	if fh != nil && fh.Header != nil {
		if ct := fh.Header.Get("Content-Type"); ct != "" && ct != "application/octet-stream" {
			return ct
		}
	}
	if ext := filepath.Ext(filename); ext != "" {
		if t := mime.TypeByExtension(ext); t != "" {
			return t
		}
	}
	return "application/octet-stream"
}

// GetMediaHandler handles GET /v1/media/{message_id}: streams stored bytes.
func GetMediaHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		messageID := chi.URLParam(r, "message_id")
		ref, err := svc.GetMediaRef(r.Context(), messageID)
		switch {
		case err == nil:
			// fall through
		case errors.Is(err, store.ErrNotFound):
			WriteProblem(w, http.StatusNotFound, "media.not_found", "no media for that message id")
			return
		case errors.Is(err, service.ErrInvalidRequest):
			WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
			return
		default:
			WriteProblem(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}

		f, err := os.Open(ref.Path)
		if err != nil {
			WriteProblem(w, http.StatusInternalServerError, "media.read_failed", err.Error())
			return
		}
		defer f.Close()

		w.Header().Set("Content-Type", ref.MIME)
		w.Header().Set("Content-Length", strconv.FormatInt(ref.Size, 10))
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, f)
	})
}
