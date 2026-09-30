package hub

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const wsTicketTTL = 60 * time.Second

func (s *Server) wsTicketSig(companyID, userID, exp int64, nonce string) string {
	m := hmac.New(sha256.New, s.cfg.Secret)
	fmt.Fprintf(m, "ws:%d:%d:%d:%s", companyID, userID, exp, nonce)
	return hex.EncodeToString(m.Sum(nil))
}

func (s *Server) issueWSTicket(companyID, userID int64, now time.Time) string {
	exp := now.Add(wsTicketTTL).Unix()
	b := make([]byte, 8)
	rand.Read(b)
	nonce := hex.EncodeToString(b)
	return fmt.Sprintf("%d.%d.%d.%s.%s", companyID, userID, exp, nonce, s.wsTicketSig(companyID, userID, exp, nonce))
}

var errBadTicket = errors.New("invalid or expired ticket")

func (s *Server) verifyWSTicket(ticket string, companyID int64, now time.Time) (int64, error) {
	parts := strings.Split(ticket, ".")
	if len(parts) != 5 || parts[3] == "" {
		return 0, errBadTicket
	}
	cid, err1 := strconv.ParseInt(parts[0], 10, 64)
	uid, err2 := strconv.ParseInt(parts[1], 10, 64)
	exp, err3 := strconv.ParseInt(parts[2], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || cid != companyID || cid <= 0 || uid <= 0 {
		return 0, errBadTicket
	}
	if now.Unix() > exp || !hmac.Equal([]byte(s.wsTicketSig(cid, uid, exp, parts[3])), []byte(parts[4])) {
		return 0, errBadTicket
	}
	return uid, nil
}

func (s *Server) handleWSTicket(w http.ResponseWriter, r *http.Request, e *Employer, companyID int64) {
	now := time.Now()
	ticket := s.issueWSTicket(companyID, e.UserID, now)
	if !s.tickets.put(ticket, scopeOf(r), now.Add(wsTicketTTL), now) {
		writeErr(w, http.StatusServiceUnavailable, "too many pending tickets, try again shortly")
		return
	}
	writeOK(w, "ticket issued", map[string]any{"ticket": ticket, "expires_in": int(wsTicketTTL.Seconds())})
}

func (s *Server) handleClock(w http.ResponseWriter, r *http.Request, e *Employer, companyID int64) {
	now := time.Now().In(s.cfg.Location)
	sc := scopeOf(r)
	writeOK(w, "clock", map[string]any{"timezone": s.cfg.Location.String(), "today": now.Format("2006-01-02"), "now": now.Format(time.RFC3339),
		"access": map[string]any{"is_owner": sc.IsOwner, "scoped": sc.Scoped(),
			"can_add": sc.May(actAdd), "can_edit": sc.May(actEdit), "can_delete": sc.May(actDelete),
			// kept for older dashboards: true when any change is allowed
			"can_manage": sc.May(actAdd) || sc.May(actEdit) || sc.May(actDelete)}})
}
