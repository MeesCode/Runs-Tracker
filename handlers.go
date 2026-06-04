package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// GET /api/runs
func (a *App) handleRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	q := r.URL.Query()
	p := ListParams{
		Page:    atoiDefault(q.Get("page"), 1),
		PerPage: atoiDefault(q.Get("per_page"), 20),
		Sort:    defaultStr(q.Get("sort"), "date"),
		Order:   defaultStr(q.Get("order"), "desc"),
		Search:  strings.TrimSpace(q.Get("search")),
	}
	if p.Page < 1 {
		p.Page = 1
	}
	if p.PerPage < 1 || p.PerPage > 200 {
		p.PerPage = 20
	}

	runs, total, err := listRuns(a.db, p)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	totalPages := (total + p.PerPage - 1) / p.PerPage
	if totalPages < 1 {
		totalPages = 1
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"runs":        runs,
		"total":       total,
		"page":        p.Page,
		"per_page":    p.PerPage,
		"total_pages": totalPages,
	})
}

// Dispatches /api/runs/:id and /api/runs/:id/gpx
func (a *App) handleRunByPath(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/runs/")
	rest = strings.Trim(rest, "/")
	if rest == "" {
		a.handleRuns(w, r)
		return
	}
	parts := strings.Split(rest, "/")
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid run id")
		return
	}

	run, err := getRun(a.db, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	if len(parts) >= 2 && parts[1] == "gpx" {
		gpx := buildGPX(run)
		w.Header().Set("Content-Type", "application/gpx+xml")
		w.Header().Set("Content-Disposition", "attachment; filename=\"run-"+strconv.FormatInt(run.ID, 10)+".gpx\"")
		_, _ = w.Write([]byte(gpx))
		return
	}

	writeJSON(w, http.StatusOK, run)
}

// GET /api/stats
func (a *App) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	st, err := computeStats(a.db, nowLocal())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}

func defaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
