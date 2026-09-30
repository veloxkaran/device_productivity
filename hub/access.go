package hub

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Scope struct {
	IsOwner   bool
	MemberIDs map[int64]bool
	// What this viewer may change. The owner always may; anyone else only
	// with the matching Laravel permission.
	CanAdd    bool
	CanEdit   bool
	CanDelete bool
}

type activityAction string

const (
	actAdd    activityAction = "add"
	actEdit   activityAction = "edit"
	actDelete activityAction = "delete"
)

func (sc Scope) May(a activityAction) bool {
	if sc.IsOwner {
		return true
	}
	switch a {
	case actAdd:
		return sc.CanAdd
	case actEdit:
		return sc.CanEdit
	case actDelete:
		return sc.CanDelete
	}
	return false
}

func (sc Scope) Allows(userID int64) bool {
	return sc.IsOwner || sc.MemberIDs == nil || sc.MemberIDs[userID]
}

func (sc Scope) Scoped() bool { return !sc.IsOwner && sc.MemberIDs != nil }

func (sc Scope) Receives(userID int64) bool {
	if sc.IsOwner {
		return true
	}
	return userID > 0 && sc.Allows(userID)
}

type scopeKey struct{}

func withScope(r *http.Request, sc Scope) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), scopeKey{}, sc))
}

func scopeOf(r *http.Request) Scope {
	sc, ok := r.Context().Value(scopeKey{}).(Scope)
	if !ok {
		return Scope{MemberIDs: map[int64]bool{}}
	}
	return sc
}

func (s *Server) owner(action string, h func(http.ResponseWriter, *http.Request, *Employer, int64)) http.Handler {
	return s.employer(func(w http.ResponseWriter, r *http.Request, e *Employer, companyID int64) {
		if !scopeOf(r).IsOwner {
			writeErr(w, http.StatusForbidden, "only the company owner can "+action)
			return
		}
		h(w, r, e, companyID)
	})
}

// can gates a write on the matching activity permission (add / edit / delete)
// instead of ownership. Member-level scope is still enforced in the handler.
func (s *Server) can(a activityAction, action string, h func(http.ResponseWriter, *http.Request, *Employer, int64)) http.Handler {
	return s.employer(func(w http.ResponseWriter, r *http.Request, e *Employer, companyID int64) {
		if !scopeOf(r).May(a) {
			writeErr(w, http.StatusForbidden, "you do not have permission to "+action)
			return
		}
		h(w, r, e, companyID)
	})
}

func denyMember(w http.ResponseWriter, r *http.Request, userID int64) bool {
	if scopeOf(r).Allows(userID) {
		return false
	}
	writeErr(w, http.StatusForbidden, "you do not have access to this member's activity")
	return true
}

func scopeMembers(sc Scope, list []member) []member {
	out := make([]member, 0, len(list))
	for _, m := range list {
		if sc.Allows(m.UserID) {
			out = append(out, m)
		}
	}
	return out
}

type paging struct {
	Page    int
	PerPage int
	Search  string
	Filter  string
	Sort    string
	Desc    bool
}

func pagingRequested(q url.Values) bool {
	for _, k := range []string{"page", "per_page", "search", "filter", "sort", "dir"} {
		if q.Has(k) {
			return true
		}
	}
	return false
}

func parsePaging(q url.Values, sorts, filters []string, defSort string) (paging, error) {
	p := paging{Page: 1, PerPage: 25, Sort: defSort, Filter: "all"}
	if v := q.Get("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return p, fmt.Errorf("page must be a positive integer")
		}
		p.Page = n
	}
	if v := q.Get("per_page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return p, fmt.Errorf("per_page must be a positive integer")
		}
		p.PerPage = min(n, 100)
	}
	p.Search = strings.TrimSpace(q.Get("search"))
	if len([]rune(p.Search)) > 100 {
		return p, fmt.Errorf("search must be at most 100 characters")
	}
	if v := q.Get("sort"); v != "" {
		if !contains(sorts, v) {
			return p, fmt.Errorf("sort must be one of %s", strings.Join(sorts, ", "))
		}
		p.Sort = v
	}
	if v := q.Get("filter"); v != "" {
		if !contains(filters, v) {
			return p, fmt.Errorf("filter must be one of %s", strings.Join(filters, ", "))
		}
		p.Filter = v
	}
	switch q.Get("dir") {
	case "", "asc":
	case "desc":
		p.Desc = true
	default:
		return p, fmt.Errorf("dir must be asc or desc")
	}
	return p, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func matchesSearch(name, search string) bool {
	return search == "" || strings.Contains(strings.ToLower(name), strings.ToLower(search))
}

func pageSlice[T any](list []T, p paging) ([]T, map[string]any) {
	total := len(list)
	last := (total + p.PerPage - 1) / p.PerPage
	if last < 1 {
		last = 1
	}
	from := min((p.Page-1)*p.PerPage, total)
	to := min(from+p.PerPage, total)
	return list[from:to], map[string]any{"page": p.Page, "per_page": p.PerPage, "total": total, "last_page": last}
}

func cmpTimePtr(a, b *time.Time) (int, bool) {
	switch {
	case a == nil && b == nil:
		return 0, true
	case a == nil || b == nil:
		return 0, false
	case a.Before(*b):
		return -1, true
	case a.After(*b):
		return 1, true
	}
	return 0, true
}

const maxTicketScopes = 10000

type ticketScope struct {
	scope   Scope
	expires time.Time
}

type ticketScopes struct {
	mu sync.Mutex
	m  map[string]ticketScope
}

func newTicketScopes() *ticketScopes { return &ticketScopes{m: map[string]ticketScope{}} }

func (t *ticketScopes) put(ticket string, sc Scope, expires, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.m) >= maxTicketScopes {
		for k, v := range t.m {
			if now.After(v.expires) {
				delete(t.m, k)
			}
		}
	}
	if len(t.m) >= maxTicketScopes {
		return false
	}
	t.m[ticket] = ticketScope{scope: sc, expires: expires}
	return true
}

func (t *ticketScopes) take(ticket string, now time.Time) (Scope, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	v, ok := t.m[ticket]
	if !ok {
		return Scope{}, false
	}
	delete(t.m, ticket)
	if now.After(v.expires) {
		return Scope{}, false
	}
	return v.scope, true
}
