package api

import (
	"errors"
	"mime"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jaysongiroux/hook_way/internal/replay/auth"
	"github.com/jaysongiroux/hook_way/internal/replay/metrics"
	"github.com/jaysongiroux/hook_way/internal/replay/store"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

// maxFilterLen matches the longest text replaywire.Validate accepts.
const maxFilterLen = 1024

var listParams = map[string]bool{
	"flow": true, "node_id": true, "destination_id": true, "status": true, "kind": true,
	"retryable": true, "since": true, "until": true, "limit": true, "cursor": true,
}

func (a *api) list(w http.ResponseWriter, r *http.Request) {
	filter, err := parseFilter(r.URL.Query())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	page, err := a.store.List(r.Context(), filter)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	resp := listResponse{Items: make([]failureSummary, 0, len(page.Items)), NextCursor: page.NextCursor}
	for _, f := range page.Items {
		resp.Items = append(resp.Items, summaryOf(f))
	}
	writeJSON(w, http.StatusOK, resp)
}

// parseFilter reads the list query strictly: unknown or repeated parameters
// are errors, so a typo never silently widens a query.
func parseFilter(q url.Values) (store.Filter, error) {
	for key, values := range q {
		if !listParams[key] {
			return store.Filter{}, newError(http.StatusBadRequest, "invalid_parameter", "unknown query parameter %q", key)
		}
		if len(values) != 1 {
			return store.Filter{}, newError(http.StatusBadRequest, "invalid_parameter", "%s must appear once", key)
		}
	}
	// Text filters are compared with stored text, so they must be text
	// PostgreSQL accepts: bounded, valid UTF-8, no control characters.
	for _, key := range []string{"flow", "node_id", "destination_id", "cursor"} {
		v := q.Get(key)
		if len(v) > maxFilterLen || !utf8.ValidString(v) || strings.IndexFunc(v, unicode.IsControl) >= 0 {
			return store.Filter{}, newError(http.StatusBadRequest, "invalid_parameter", "%s must be at most %d bytes of printable UTF-8", key, maxFilterLen)
		}
	}
	f := store.Filter{Flow: q.Get("flow"), NodeID: q.Get("node_id"), DestinationID: q.Get("destination_id"),
		Status: q.Get("status"), Kind: q.Get("kind"), Cursor: q.Get("cursor")}
	if f.Status != "" && !slices.Contains(store.Statuses, f.Status) {
		return store.Filter{}, newError(http.StatusBadRequest, "invalid_parameter", "status must be one of %s", strings.Join(store.Statuses, ", "))
	}
	if f.Kind != "" && f.Kind != replaywire.KindResume && f.Kind != replaywire.KindPublishOnly {
		return store.Filter{}, newError(http.StatusBadRequest, "invalid_parameter", "kind must be resume or publish_only")
	}
	switch v := q.Get("retryable"); v {
	case "":
	case "true", "false":
		b := v == "true"
		f.Retryable = &b
	default:
		return store.Filter{}, newError(http.StatusBadRequest, "invalid_parameter", "retryable must be true or false")
	}
	for _, p := range []struct {
		name string
		dst  *time.Time
	}{{"since", &f.Since}, {"until", &f.Until}} {
		if v := q.Get(p.name); v != "" {
			t, err := time.Parse(time.RFC3339Nano, v)
			if err != nil {
				return store.Filter{}, newError(http.StatusBadRequest, "invalid_parameter", "%s must be an RFC 3339 timestamp", p.name)
			}
			*p.dst = t
		}
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > store.MaxListLimit {
			return store.Filter{}, newError(http.StatusBadRequest, "invalid_parameter", "limit must be between 1 and %d", store.MaxListLimit)
		}
		f.Limit = n
	}
	return f, nil
}

func (a *api) get(w http.ResponseWriter, r *http.Request) {
	d, err := a.store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, detailOf(d))
}

// payload serves the exact stored payload bytes after the store has checked
// their size and digest.
func (a *api) payload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := a.store.Payload(r.Context(), id, auth.Actor(r.Context()))
	if err != nil {
		switch {
		case errors.Is(err, store.ErrCorrupt):
			a.metrics.PayloadDownload(metrics.DownloadCorrupt)
			a.logger.Error("stored payload failed its integrity check", "failure_id", id)
		case errors.Is(err, store.ErrGone):
			a.metrics.PayloadDownload(metrics.DownloadGone)
		case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrNoPayload):
			a.metrics.PayloadDownload(metrics.DownloadNotFound)
		}
		a.fail(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", contentDisposition(id, p.Filename))
	h.Set("Content-Length", strconv.Itoa(len(p.Bytes)))
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	if p.ContentType != "" {
		h.Set("X-Hookway-Payload-Content-Type", p.ContentType)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(p.Bytes)
	a.metrics.PayloadDownload(metrics.DownloadOK)
}

// contentDisposition names the download. The captured filename is vendor
// input: it is reduced to a base name and encoded by mime.FormatMediaType
// (RFC 2231 for non-ASCII). Without a usable name the record id is used.
func contentDisposition(id, filename string) string {
	name := path.Base(strings.ReplaceAll(filename, `\`, "/"))
	if filename == "" || name == "." || name == "/" || name == ".." {
		name = id + ".bin"
	}
	if v := mime.FormatMediaType("attachment", map[string]string{"filename": name}); v != "" {
		return v
	}
	return mime.FormatMediaType("attachment", map[string]string{"filename": id + ".bin"})
}

func (a *api) dismiss(w http.ResponseWriter, r *http.Request) {
	f, err := a.store.Dismiss(r.Context(), r.PathValue("id"), auth.Actor(r.Context()))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.metrics.Dismissal()
	writeJSON(w, http.StatusOK, summaryOf(f))
}
