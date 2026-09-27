package hub

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestManualTimeIsOwnerOnly(t *testing.T) {
	s := newFixture(t)
	seedTeam(t, s)
	id, err := s.store.AddManual(companyA, 101, 1, "2026-01-05", 600, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range [][2]string{
		{"POST", "/api/employer/1/members/101/manual"},
		{"DELETE", fmt.Sprintf("/api/employer/1/manual/%d", id)},
	} {
		r := call(t, s, c[0], "viewer", c[1], `{"date":"2026-01-05","minutes":10}`)
		if r.code != 403 || r.body["message"] != "only the company owner can change manual time" {
			t.Fatalf("%s %s gave %d %v", c[0], c[1], r.code, r.body["message"])
		}
	}
	if r := call(t, s, "POST", "owner", "/api/employer/1/members/101/manual", `{"date":"2026-01-05","minutes":10}`); r.code != 201 {
		t.Fatalf("owner add gave %d", r.code)
	}
	if r := call(t, s, "DELETE", "owner", fmt.Sprintf("/api/employer/1/manual/%d", id), ""); r.code != 200 {
		t.Fatalf("owner delete gave %d", r.code)
	}
}

func TestWSClosesAfterMaxLifetime(t *testing.T) {
	s := newFixture(t)
	old := wsMaxLifetime
	wsMaxLifetime = 300 * time.Millisecond
	t.Cleanup(func() { wsMaxLifetime = old })
	srv := httptest.NewServer(s.routes())
	defer srv.Close()
	ticket := call(t, s, "POST", "owner", "/api/employer/1/ws-ticket", "").body["data"].(map[string]any)["ticket"].(string)
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?company_id=1&ticket=" + url.QueryEscape(ticket)
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	started := time.Now()
	for {
		_, _, err := conn.ReadMessage()
		if err == nil {
			continue
		}
		var ce *websocket.CloseError
		if !errors.As(err, &ce) || ce.Code != websocket.CloseNormalClosure {
			t.Fatalf("expected normal close, got %v", err)
		}
		if time.Since(started) < 200*time.Millisecond {
			t.Fatalf("closed too early: %v", time.Since(started))
		}
		return
	}
}

func TestMonthlyIncludesRevokedMembersConsistently(t *testing.T) {
	s := newFixture(t)
	seedTeam(t, s)
	dave := addMember(t, s, companyA, 104, "Dave")
	work(t, s, dave, "2026-01-05", 9, 0, 19)
	if ok, err := s.store.RevokeDevice(companyA, dave.ID); !ok || err != nil {
		t.Fatalf("revoke %v %v", ok, err)
	}
	blank, _, err := s.store.CreateDevice(companyA, 101, 1, "", "spare")
	if err != nil {
		t.Fatal(err)
	}
	s.store.Heartbeat(blank.ID, "active", 0, "", "", "", nil)
	eve := addMember(t, s, companyA, 105, "Eve")
	if ok, _ := s.store.RevokeDevice(companyA, eve.ID); !ok {
		t.Fatal("could not revoke Eve")
	}

	monthly := call(t, s, "GET", "owner", "/api/employer/1/monthly?month=2026-01", "").body["data"].(map[string]any)
	if monthly["total_members"] != float64(4) {
		t.Fatalf("total_members %v", monthly["total_members"])
	}
	top := monthly["metrics"].(map[string]any)["worked"].(map[string]any)["top"].([]any)
	if len(top) != 4 {
		t.Fatalf("top %v", top)
	}
	for _, row := range top {
		if row.(map[string]any)["employee_name"] == "" {
			t.Fatalf("blank name in top %v", top)
		}
	}
	if first := top[0].(map[string]any); first["employee_name"] != "Carol" || top[1].(map[string]any)["employee_name"] != "Dave" {
		t.Fatalf("ranking %v", top)
	}
	r := call(t, s, "GET", "owner", "/api/employer/1/monthly/members?month=2026-01", "")
	if got := strings.Join(rowNames(r), ","); got != "Alice,Bob,Carol,Dave" {
		t.Fatalf("members table %q", got)
	}
	if r.body["meta"].(map[string]any)["total"] != float64(4) {
		t.Fatalf("meta %v", r.body["meta"])
	}
}

func TestAbsentRuleSharedByOverviewAndMonthly(t *testing.T) {
	s := newFixture(t)
	devs := seedTeam(t, s)
	end := day("2026-01-05", 20, 0)
	if err := s.store.UpsertBreak(devs[103], day("2026-01-05", 8, 0), &end); err != nil {
		t.Fatal(err)
	}
	ov := call(t, s, "GET", "owner", "/api/employer/1/overview?date=2026-01-05", "")
	for _, row := range ov.body["data"].([]any) {
		m := row.(map[string]any)
		if m["employee_name"] == "Carol" && (m["absent"] != false || m["worked_seconds"] != float64(0)) {
			t.Fatalf("overview carol %v", m)
		}
	}
	if ov.body["totals"].(map[string]any)["absent_members"] != float64(0) {
		t.Fatalf("overview totals %v", ov.body["totals"])
	}
	r := call(t, s, "GET", "owner", "/api/employer/1/monthly/members?month=2026-01&search=carol", "")
	carol := r.body["data"].([]any)[0].(map[string]any)
	if carol["days_present"] != float64(1) || carol["absent_days"] != float64(25) {
		t.Fatalf("monthly carol %v", carol)
	}
}

func verifierServer(t *testing.T, status int) *Server {
	s := newFixture(t)
	hajir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(`{"message":"db down: SQLSTATE secret"}`))
	}))
	t.Cleanup(hajir.Close)
	s.verifier = NewVerifier(hajir.URL)
	return s
}

func TestEmployerGateDistinguishesOutageFromRejection(t *testing.T) {
	for _, c := range []struct {
		status int
		code   int
		msg    string
	}{
		{500, 503, "activity service temporarily unavailable"},
		{502, 503, "activity service temporarily unavailable"},
		{401, 401, "invalid or expired session"},
		{403, 401, "invalid or expired session"},
	} {
		r := call(t, verifierServer(t, c.status), "GET", "tok", "/api/employer/1/overview", "")
		if r.code != c.code || r.body["message"] != c.msg {
			t.Fatalf("hajir %d gave %d %v", c.status, r.code, r.body["message"])
		}
	}
	s := newFixture(t)
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	s.verifier = NewVerifier(dead.URL)
	r := call(t, s, "GET", "tok", "/api/employer/1/overview", "")
	if r.code != 503 || r.body["message"] != "activity service temporarily unavailable" {
		t.Fatalf("unreachable gave %d %v", r.code, r.body["message"])
	}
}

func TestPurgeSkipsFailingRow(t *testing.T) {
	s := newFixture(t)
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	a := addMember(t, s, companyA, 101, "Alice")
	b := addMember(t, s, companyB, 201, "Zed")
	first, firstPath := addShot(t, s, a, now.AddDate(0, 0, -200))
	stuck, stuckPath := addShot(t, s, a, now.AddDate(0, 0, -199))
	third, thirdPath := addShot(t, s, a, now.AddDate(0, 0, -198))
	other, otherPath := addShot(t, s, b, now.AddDate(0, 0, -200))
	if _, err := s.store.db.Exec(fmt.Sprintf(`CREATE TRIGGER block_delete BEFORE DELETE ON screenshots WHEN old.id = %d BEGIN SELECT RAISE(ABORT, 'locked'); END`, stuck)); err != nil {
		t.Fatal(err)
	}
	n, err := s.purgeScreenshots(now)
	if err != nil || n != 3 {
		t.Fatalf("purged %d err %v", n, err)
	}
	for id, p := range map[int64]string{first: firstPath, third: thirdPath, other: otherPath} {
		if _, err := s.store.ScreenshotByID(id); err == nil || exists(p) {
			t.Fatalf("screenshot %d not purged", id)
		}
	}
	if _, err := s.store.ScreenshotByID(stuck); err != nil || !exists(stuckPath) {
		t.Fatal("failing screenshot lost")
	}
}

func TestMemberSettingsRequireMembership(t *testing.T) {
	s := newFixture(t)
	seedTeam(t, s)
	gone := addMember(t, s, companyA, 106, "Gone")
	s.store.RevokeDevice(companyA, gone.ID)
	addMember(t, s, companyB, 201, "Zed")
	body := `{"screenshot_interval_seconds":60}`
	for _, p := range []string{"/api/employer/1/members/999/settings", "/api/employer/1/members/201/settings"} {
		r := call(t, s, "PUT", "owner", p, body)
		if r.code != 422 || r.body["message"] != "user is not a member of this company" {
			t.Fatalf("%s gave %d %v", p, r.code, r.body["message"])
		}
	}
	if s.store.MemberScreenshotInterval(companyA, 999) != 0 {
		t.Fatal("non-member setting stored")
	}
	for _, p := range []string{"/api/employer/1/members/101/settings", "/api/employer/1/members/106/settings"} {
		if r := call(t, s, "PUT", "owner", p, body); r.code != 200 {
			t.Fatalf("%s gave %d", p, r.code)
		}
	}
}

func TestDayIsHalfOpen(t *testing.T) {
	s := newFixture(t)
	d := addMember(t, s, companyA, 101, "Alice")
	out := day("2026-01-06", 1, 0)
	if err := s.store.UpsertTimeEntry(d, day("2026-01-05", 23, 0), &out); err != nil {
		t.Fatal(err)
	}
	start, next, _ := s.dayBounds("2026-01-05")
	if !next.Equal(day("2026-01-06", 0, 0)) {
		t.Fatalf("next %v", next)
	}
	ix, err := s.collect(companyA, 0, start, next)
	if err != nil {
		t.Fatal(err)
	}
	if st := ix[101]["2026-01-05"]; st == nil || st.Worked != 3600 || st.LastOut != nil {
		t.Fatalf("day stat %+v", st)
	}
	if ix[101]["2026-01-06"] != nil {
		t.Fatalf("next day leaked %+v", ix[101]["2026-01-06"])
	}
}

func TestTimeEntriesDeviceFilter(t *testing.T) {
	s := newFixture(t)
	a := addMember(t, s, companyA, 101, "Alice")
	b, _, _ := s.store.CreateDevice(companyA, 101, 1, "Alice", "desktop")
	work(t, s, a, "2026-01-05", 9, 0, 12)
	work(t, s, b, "2026-01-05", 13, 0, 17)
	start, next, _ := s.dayBounds("2026-01-05")
	list, err := s.store.TimeEntries(companyA, 101, []int64{b.ID}, start, next)
	if err != nil || len(list) != 1 || list[0].DeviceID != b.ID {
		t.Fatalf("entries %+v %v", list, err)
	}
	if got := s.summary(b, start, next)["worked_seconds"]; got != int64(4*3600) {
		t.Fatalf("summary worked %v", got)
	}
}

func TestScreenshotLinkLifetimeAndCaching(t *testing.T) {
	s := newFixture(t)
	a := addMember(t, s, companyA, 101, "Alice")
	id, _ := addShot(t, s, a, time.Now())
	link, _ := url.Parse(s.screenshotURL(id))
	var exp int64
	fmt.Sscan(link.Query().Get("exp"), &exp)
	if left := time.Until(time.Unix(exp, 0)); left > 15*time.Minute || left < 14*time.Minute {
		t.Fatalf("link valid for %v", left)
	}
	req := httptest.NewRequest("GET", link.RequestURI(), nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
}

func TestVerifierAsksHajirForActivityFields(t *testing.T) {
	var got string
	hajir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query().Get("include")
		w.Write([]byte(`{"data":{"user":{"id":9,"name":"M"},"companies":[{"id":1,"name":"A","is_owner":false,"can_view_activity":true,"activity_member_ids":[101]}]}}`))
	}))
	defer hajir.Close()
	e, err := NewVerifier(hajir.URL).Verify("tok")
	if err != nil {
		t.Fatal(err)
	}
	if got != "activity" {
		t.Fatalf("include=%q, want activity", got)
	}
	if c := e.Companies[1]; !c.CanViewActivity || len(c.ActivityMemberIDs) != 1 {
		t.Fatalf("activity fields not parsed: %+v", c)
	}
}

func TestDeviceRegisterOutageIsNotSignInAgain(t *testing.T) {
	r := call(t, verifierServer(t, 500), "POST", "tok", "/api/device/register", `{}`)
	if r.code != 503 {
		t.Fatalf("register during outage gave %d %v", r.code, r.body["message"])
	}
	r = call(t, verifierServer(t, 401), "POST", "tok", "/api/device/register", `{}`)
	if r.code != 401 {
		t.Fatalf("register with bad token gave %d", r.code)
	}
}
