// Package http exposes the service layer as a JSON HTTP API using chi.
package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

func WriteProblem(w http.ResponseWriter, status int, code, detail string) {
	p := Problem{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Code:   code,
		Detail: detail,
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(p); err != nil {
		slog.Default().Error("write problem", "err", err)
	}
}
