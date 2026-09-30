package hub

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

// etagJSON wraps the employer JSON read routes. It buffers each successful GET
// response, adds a strong validator (ETag) and a per-user, date-aware
// Cache-Control, and replies 304 Not Modified when the client already holds the
// current bytes. Every policy is `private`, so Cloudflare -- a shared cache in
// front of this hub -- never stores one employer's data and serves it to
// another.
//
// It is deliberately narrow: anything that is not a GET/HEAD under
// /api/employer/ (mutations, device sync, the WebSocket upgrade, and the
// screenshot/download file routes) passes straight through unbuffered.
func (s *Server) etagJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) ||
			r.Header.Get("Upgrade") != "" ||
			!strings.HasPrefix(r.URL.Path, "/api/employer/") {
			next.ServeHTTP(w, r)
			return
		}

		cw := &captureWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(cw, r)
		body := cw.buf.Bytes()

		// Only add validators to a clean 200 JSON body; flush anything else as-is.
		if cw.status != http.StatusOK {
			w.WriteHeader(cw.status)
			w.Write(body)
			return
		}

		sum := sha256.Sum256(body)
		etag := `"` + hex.EncodeToString(sum[:16]) + `"`

		policy := s.cacheControlFor(r)
		// Responses that carry short-lived signed screenshot URLs must never
		// outlive the signature (screenshotURLTTL), so they always revalidate.
		if bytes.Contains(body, []byte("/files/screenshots/")) {
			policy = "private, no-cache"
		}

		h := w.Header()
		h.Set("ETag", etag)
		h.Set("Vary", "Authorization")
		h.Set("Cache-Control", policy)

		if inm := r.Header.Get("If-None-Match"); inm != "" && etagMatches(inm, etag) {
			h.Del("Content-Length")
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			w.Write(body)
		}
	})
}

// cacheControlFor derives a private, date-aware policy from the request's
// date / date_from / month query. A past day or month is immutable, so the
// browser may keep it for a day; the current period and undated requests are
// marked no-cache and revalidated every time -- cheaply, via the ETag.
func (s *Server) cacheControlFor(r *http.Request) string {
	q := r.URL.Query()
	now := time.Now().In(s.cfg.Location)

	if m := q.Get("month"); m != "" {
		if t, err := time.ParseInLocation("2006-01", m, s.cfg.Location); err == nil {
			cur := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, s.cfg.Location)
			if t.Before(cur) {
				return "private, max-age=86400"
			}
		}
		return "private, no-cache"
	}

	date := q.Get("date")
	if date == "" {
		date = q.Get("date_from")
	}
	if date != "" {
		if t, err := time.ParseInLocation("2006-01-02", date, s.cfg.Location); err == nil {
			today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.cfg.Location)
			if t.Before(today) {
				return "private, max-age=86400"
			}
		}
	}
	return "private, no-cache"
}

// captureWriter buffers a handler's response so etagJSON can hash and
// conditionally replace it. Header() still targets the real writer, so the
// Content-Type and CORS headers set by inner handlers are preserved.
type captureWriter struct {
	http.ResponseWriter
	buf    bytes.Buffer
	status int
}

func (c *captureWriter) WriteHeader(code int)        { c.status = code }
func (c *captureWriter) Write(b []byte) (int, error) { return c.buf.Write(b) }

// etagMatches reports whether an If-None-Match header (which may be a
// comma-separated list, and may use the weak "W/" prefix) contains this ETag.
func etagMatches(ifNoneMatch, etag string) bool {
	for _, part := range strings.Split(ifNoneMatch, ",") {
		p := strings.TrimSpace(part)
		p = strings.TrimPrefix(p, "W/")
		if p == "*" || p == etag {
			return true
		}
	}
	return false
}
