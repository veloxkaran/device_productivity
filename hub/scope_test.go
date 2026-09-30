package hub

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeVerifier map[string]*Employer

func (f fakeVerifier) Verify(token string) (*Employer, error) {
	if e, ok := f[token]; ok {
		return e, nil
	}
	return nil, fmt.Errorf("%w: token rejected (401)", errSessionRejected)
}

const (
	companyA = int64(1)
	companyB = int64(2)
)

func newFixture(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	os.MkdirAll(filepath.Join(dir, "screenshots"), 0755)
	var viewerCompany EmployerCompany
	json.Unmarshal([]byte(`{"id":1,"name":"A","is_owner":false,"can_view_activity":true,"activity_member_ids":[101,102]}`), &viewerCompany)
	var managerCompany EmployerCompany
	json.Unmarshal([]byte(`{"id":1,"name":"A","is_owner":false,"can_view_activity":true,"activity_member_ids":[101,102],"can_add_activity":true,"can_edit_activity":true,"can_delete_activity":true}`), &managerCompany)
	return &Server{
		cfg:    Config{Secret: []byte("s"), Location: time.UTC, DataDir: dir, OnlineThreshold: 90 * time.Second, PublicURL: "http://hub"},
		store:  store,
		broker: NewBroker(), tickets: newTicketScopes(),
		verifier: fakeVerifier{
			"owner":  {UserID: 1, Name: "Owner", Companies: map[int64]EmployerCompany{companyA: {ID: companyA, IsOwner: true, CanViewActivity: true}}},
			"ownerB": {UserID: 2, Name: "OwnerB", Companies: map[int64]EmployerCompany{companyB: {ID: companyB, IsOwner: true, CanViewActivity: true}}},
			"viewer": {UserID: 3, Name: "Viewer", Companies: map[int64]EmployerCompany{companyA: viewerCompany}},
			// Same scope as viewer, plus add/edit/delete grants from Laravel.
			"manager": {UserID: 5, Name: "Manager", Companies: map[int64]EmployerCompany{companyA: managerCompany}},
			"nobody": {UserID: 4, Name: "Nobody", Companies: map[int64]EmployerCompany{companyA: {ID: companyA}}},
		},
	}
}

func day(s string, hh, mm int) time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return t.Add(time.Duration(hh)*time.Hour + time.Duration(mm)*time.Minute)
}

func addMember(t *testing.T, s *Server, company, user int64, name string) *Device {
	t.Helper()
	d, _, err := s.store.CreateDevice(company, user, 1, name, name+" laptop")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func work(t *testing.T, s *Server, d *Device, date string, inH, inM, outH int) {
	t.Helper()
	out := day(date, outH, 0)
	if err := s.store.UpsertTimeEntry(d, day(date, inH, inM), &out); err != nil {
		t.Fatal(err)
	}
}

func seedTeam(t *testing.T, s *Server) map[int64]*Device {
	devs := map[int64]*Device{
		101: addMember(t, s, companyA, 101, "Alice"),
		102: addMember(t, s, companyA, 102, "Bob"),
		103: addMember(t, s, companyA, 103, "Carol"),
	}
	work(t, s, devs[101], "2026-01-05", 9, 0, 17)
	work(t, s, devs[102], "2026-01-05", 10, 0, 12)
	work(t, s, devs[103], "2026-01-05", 8, 0, 20)
	return devs
}

type apiResp struct {
	code int
	body map[string]any
}

func call(t *testing.T, s *Server, method, token, path string, body string) apiResp {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	return apiResp{code: rec.Code, body: out}
}

func rowNames(r apiResp) []string {
	var names []string
	list, _ := r.body["data"].([]any)
	for _, row := range list {
		names = append(names, row.(map[string]any)["employee_name"].(string))
	}
	return names
}

func TestCompanyParsingDefaultsForOldBackend(t *testing.T) {
	var old, scoped, denied EmployerCompany
	json.Unmarshal([]byte(`{"id":1,"name":"A","is_owner":true}`), &old)
	json.Unmarshal([]byte(`{"id":1,"is_owner":false,"can_view_activity":true,"activity_member_ids":[5]}`), &scoped)
	json.Unmarshal([]byte(`{"id":1,"is_owner":false,"can_view_activity":false,"activity_member_ids":[]}`), &denied)
	if !old.CanViewActivity || old.ActivityMemberIDs != nil {
		t.Fatalf("old backend owner parsed as %+v", old)
	}
	e := &Employer{Companies: map[int64]EmployerCompany{1: scoped}}
	sc, ok := e.Access(1)
	if !ok || sc.IsOwner || !sc.Scoped() || !sc.Allows(5) || sc.Allows(6) {
		t.Fatalf("scoped access wrong: %+v %v", sc, ok)
	}
	e.Companies[1] = denied
	if _, ok := e.Access(1); ok {
		t.Fatal("can_view_activity=false admitted")
	}
}

func TestOverviewScopeFiltersRowsAndTotals(t *testing.T) {
	s := newFixture(t)
	seedTeam(t, s)
	all := call(t, s, "GET", "owner", "/api/employer/1/overview?date=2026-01-05", "")
	if all.code != 200 || len(rowNames(all)) != 3 {
		t.Fatalf("owner overview %d %v", all.code, all.body)
	}
	r := call(t, s, "GET", "viewer", "/api/employer/1/overview?date=2026-01-05", "")
	if r.code != 200 {
		t.Fatalf("viewer overview %d %v", r.code, r.body)
	}
	names := rowNames(r)
	if len(names) != 2 || strings.Contains(strings.Join(names, ","), "Carol") {
		t.Fatalf("viewer saw %v", names)
	}
	totals := r.body["totals"].(map[string]any)
	if totals["total_members"].(float64) != 2 || totals["avg_worked_seconds"].(float64) != 5*3600 || totals["late_members"].(float64) != 1 {
		t.Fatalf("totals not scoped: %v", totals)
	}
	if call(t, s, "GET", "nobody", "/api/employer/1/overview", "").code != 403 {
		t.Fatal("no-access user admitted")
	}
}

func TestOutOfScopeSingleMemberRoutesAre403(t *testing.T) {
	s := newFixture(t)
	seedTeam(t, s)
	for _, p := range []string{
		"/api/employer/1/members/103/day?date=2026-01-05",
		"/api/employer/1/timeline?user_id=103&date=2026-01-05",
		"/api/employer/1/screenshots?user_id=103&date=2026-01-05",
		"/api/employer/1/time-entries?user_id=103&date_from=2026-01-05",
		"/api/employer/1/members/103/settings",
	} {
		if r := call(t, s, "GET", "viewer", p, ""); r.code != 403 {
			t.Fatalf("%s gave %d", p, r.code)
		}
	}
	if r := call(t, s, "POST", "viewer", "/api/employer/1/members/103/manual", `{"date":"2026-01-05","minutes":10}`); r.code != 403 {
		t.Fatalf("manual add out of scope gave %d", r.code)
	}
	if r := call(t, s, "GET", "viewer", "/api/employer/1/members/101/day?date=2026-01-05", ""); r.code != 200 {
		t.Fatalf("in-scope member day gave %d", r.code)
	}
	r := call(t, s, "GET", "viewer", "/api/employer/1/time-entries?date_from=2026-01-05", "")
	if list := r.body["data"].([]any); len(list) != 2 {
		t.Fatalf("time entries not filtered: %d", len(list))
	}
	r = call(t, s, "GET", "viewer", "/api/employer/1/devices?date=2026-01-05", "")
	if list := r.body["data"].([]any); len(list) != 2 {
		t.Fatalf("devices not filtered: %d", len(list))
	}
}

func TestOwnerOnlyRoutesRejectScopedViewer(t *testing.T) {
	s := newFixture(t)
	seedTeam(t, s)
	for _, c := range [][2]string{
		{"PUT", "/api/employer/1/settings"},
		{"POST", "/api/employer/1/devices"},
		{"DELETE", "/api/employer/1/devices/1"},
		{"PUT", "/api/employer/1/members/101/settings"},
		{"DELETE", "/api/employer/1/screenshots/1"},
	} {
		r := call(t, s, c[0], "viewer", c[1], `{}`)
		msg, _ := r.body["message"].(string)
		// No add/edit/delete grant for this viewer, so every write is refused.
		if r.code != 403 || !strings.HasPrefix(msg, "you do not have permission to") {
			t.Fatalf("%s %s gave %d %q", c[0], c[1], r.code, msg)
		}
	}
	clock := call(t, s, "GET", "viewer", "/api/employer/1/clock", "")
	access := clock.body["data"].(map[string]any)["access"].(map[string]any)
	if access["is_owner"] != false || access["can_manage"] != false || access["scoped"] != true {
		t.Fatalf("clock access %v", access)
	}
}

func TestBrokerDeliversOnlyInScope(t *testing.T) {
	b := NewBroker()
	owner := &client{send: make(chan []byte, 8), scope: Scope{IsOwner: true}}
	viewer := &client{send: make(chan []byte, 8), scope: Scope{MemberIDs: map[int64]bool{101: true}}}
	b.join(companyA, owner)
	b.join(companyA, viewer)
	b.Publish(companyA, 101, "e", nil)
	b.Publish(companyA, 103, "e", nil)
	b.Publish(companyA, 0, "e", nil)
	if len(owner.send) != 3 || len(viewer.send) != 1 {
		t.Fatalf("owner got %d, viewer got %d", len(owner.send), len(viewer.send))
	}
}

func TestWSTicketCarriesScopeOnce(t *testing.T) {
	s := newFixture(t)
	r := call(t, s, "POST", "viewer", "/api/employer/1/ws-ticket", "")
	ticket := r.body["data"].(map[string]any)["ticket"].(string)
	sc, ok := s.tickets.take(ticket, time.Now())
	if !ok || !sc.Scoped() || !sc.Allows(101) || sc.Allows(103) {
		t.Fatalf("ticket scope %+v %v", sc, ok)
	}
	if _, ok := s.tickets.take(ticket, time.Now()); ok {
		t.Fatal("ticket reused")
	}
}

func addShot(t *testing.T, s *Server, d *Device, at time.Time) (int64, string) {
	t.Helper()
	rel := fmt.Sprintf("%d/%d-%d.jpg", d.CompanyID, d.ID, at.Unix())
	abs := filepath.Join(s.cfg.DataDir, "screenshots", rel)
	os.MkdirAll(filepath.Dir(abs), 0755)
	os.WriteFile(abs, []byte("x"), 0644)
	id, err := s.store.InsertScreenshot(d, at, rel, 1)
	if err != nil {
		t.Fatal(err)
	}
	return id, abs
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestRetentionPurge(t *testing.T) {
	s := newFixture(t)
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	a := addMember(t, s, companyA, 101, "Alice")
	b := addMember(t, s, companyB, 201, "Zed")
	cfg := s.store.GetSettings(companyA)
	cfg.ScreenshotRetentionDays = 30
	s.store.SaveSettings(companyA, cfg)
	oldA, oldAPath := addShot(t, s, a, now.AddDate(0, 0, -40))
	newA, newAPath := addShot(t, s, a, now.AddDate(0, 0, -10))
	oldB, oldBPath := addShot(t, s, b, now.AddDate(0, 0, -40))
	n, err := s.purgeScreenshots(now)
	if err != nil || n != 1 {
		t.Fatalf("purged %d err %v", n, err)
	}
	if _, err := s.store.ScreenshotByID(oldA); err == nil || exists(oldAPath) {
		t.Fatal("expired screenshot kept")
	}
	for id, p := range map[int64]string{newA: newAPath, oldB: oldBPath} {
		if _, err := s.store.ScreenshotByID(id); err != nil || !exists(p) {
			t.Fatalf("screenshot %d wrongly purged", id)
		}
	}
}

func TestRetentionSettingValidation(t *testing.T) {
	s := newFixture(t)
	if got := call(t, s, "GET", "owner", "/api/employer/1/settings", "").body["data"].(map[string]any)["screenshot_retention_days"]; got != float64(90) {
		t.Fatalf("default retention %v", got)
	}
	base := `{"work_start":"09:00","screenshot_retention_days":%d}`
	if r := call(t, s, "PUT", "owner", "/api/employer/1/settings", fmt.Sprintf(base, 5)); r.code != 422 {
		t.Fatalf("retention 5 gave %d", r.code)
	}
	if r := call(t, s, "PUT", "owner", "/api/employer/1/settings", fmt.Sprintf(base, 45)); r.code != 200 {
		t.Fatalf("retention 45 gave %d", r.code)
	}
	if r := call(t, s, "PUT", "owner", "/api/employer/1/settings", `{"work_start":"09:00"}`); r.body["data"].(map[string]any)["screenshot_retention_days"] != float64(45) {
		t.Fatalf("omitted retention reset the value: %v", r.body)
	}
}

func TestScreenshotDeleteAcrossCompanies(t *testing.T) {
	s := newFixture(t)
	b := addMember(t, s, companyB, 201, "Zed")
	id, path := addShot(t, s, b, time.Now())
	if r := call(t, s, "DELETE", "owner", fmt.Sprintf("/api/employer/1/screenshots/%d", id), ""); r.code != 404 {
		t.Fatalf("cross-company delete gave %d", r.code)
	}
	if !exists(path) {
		t.Fatal("file removed by cross-company delete")
	}
	watcher := &client{send: make(chan []byte, 8), scope: Scope{IsOwner: true}}
	s.broker.join(companyB, watcher)
	if r := call(t, s, "DELETE", "ownerB", fmt.Sprintf("/api/employer/2/screenshots/%d", id), ""); r.code != 200 {
		t.Fatalf("owner delete gave %d", r.code)
	}
	if exists(path) {
		t.Fatal("file not removed")
	}
	if len(watcher.send) != 1 || !strings.Contains(string(<-watcher.send), `"screenshot-deleted"`) {
		t.Fatal("no screenshot-deleted event")
	}
}

func TestOverviewPaging(t *testing.T) {
	s := newFixture(t)
	seedTeam(t, s)
	legacy := call(t, s, "GET", "owner", "/api/employer/1/overview?date=2026-01-05", "")
	if _, ok := legacy.body["meta"]; ok || len(rowNames(legacy)) != 3 {
		t.Fatalf("legacy response changed: %v", legacy.body)
	}
	r := call(t, s, "GET", "owner", "/api/employer/1/overview?date=2026-01-05&per_page=2&sort=worked_seconds&dir=desc", "")
	meta := r.body["meta"].(map[string]any)
	if got := strings.Join(rowNames(r), ","); got != "Carol,Alice" || meta["total"] != float64(3) || meta["last_page"] != float64(2) || meta["per_page"] != float64(2) {
		t.Fatalf("page 1 %s %v", got, meta)
	}
	r = call(t, s, "GET", "owner", "/api/employer/1/overview?date=2026-01-05&per_page=2&page=2&sort=worked_seconds&dir=desc", "")
	if got := strings.Join(rowNames(r), ","); got != "Bob" {
		t.Fatalf("page 2 %s", got)
	}
	r = call(t, s, "GET", "owner", "/api/employer/1/overview?date=2026-01-05&filter=late", "")
	if got := strings.Join(rowNames(r), ","); got != "Bob" || r.body["totals"].(map[string]any)["total_members"] != float64(3) {
		t.Fatalf("late filter %s %v", got, r.body["totals"])
	}
	r = call(t, s, "GET", "owner", "/api/employer/1/overview?date=2026-01-05&search=AR&sort=name&dir=desc", "")
	if got := strings.Join(rowNames(r), ","); got != "Carol" {
		t.Fatalf("search %s", got)
	}
	if r := call(t, s, "GET", "owner", "/api/employer/1/overview?sort=bogus", ""); r.code != 422 {
		t.Fatalf("bad sort gave %d", r.code)
	}
}

func TestMonthlyMembers(t *testing.T) {
	s := newFixture(t)
	devs := seedTeam(t, s)
	work(t, s, devs[101], "2026-01-06", 9, 0, 13)
	if _, err := s.store.AddManual(companyA, 101, 1, "2026-01-07", 1800, ""); err != nil {
		t.Fatal(err)
	}
	r := call(t, s, "GET", "viewer", "/api/employer/1/monthly/members?month=2026-01&sort=worked_seconds&dir=desc", "")
	if r.code != 200 {
		t.Fatalf("monthly members %d %v", r.code, r.body)
	}
	if got := strings.Join(rowNames(r), ","); got != "Alice,Bob" {
		t.Fatalf("rows %s", got)
	}
	alice := r.body["data"].([]any)[0].(map[string]any)
	want := map[string]float64{"days_present": 3, "late_days": 0, "absent_days": 23, "worked_seconds": 12*3600 + 1800, "manual_seconds": 1800}
	for k, v := range want {
		if alice[k] != v {
			t.Fatalf("%s = %v, want %v (%v)", k, alice[k], v, alice)
		}
	}
	bob := r.body["data"].([]any)[1].(map[string]any)
	if bob["late_days"] != float64(1) || bob["days_present"] != float64(1) || bob["absent_days"] != float64(25) {
		t.Fatalf("bob %v", bob)
	}
	if meta := r.body["meta"].(map[string]any); meta["total"] != float64(2) {
		t.Fatalf("meta %v", meta)
	}
	monthly := call(t, s, "GET", "viewer", "/api/employer/1/monthly?month=2026-01", "").body["data"].(map[string]any)
	if monthly["total_members"] != float64(2) || monthly["late_members"] != float64(1) {
		t.Fatalf("monthly not scoped %v", monthly)
	}
	top := monthly["metrics"].(map[string]any)["worked"].(map[string]any)["top"].([]any)
	if len(top) != 2 {
		t.Fatalf("monthly top includes out-of-scope members: %v", top)
	}
}

// A non-owner holding the add/edit/delete permissions may change activity for
// the people in their scope -- and only them -- but never company-wide settings.
func TestGrantedManagerWritesWithinScopeOnly(t *testing.T) {
	s := newFixture(t)
	seedTeam(t, s)
	if r := call(t, s, "POST", "manager", "/api/employer/1/members/101/manual", `{"date":"2026-01-05","minutes":10}`); r.code != 201 {
		t.Fatalf("in-scope manual add gave %d %v", r.code, r.body["message"])
	}
	if r := call(t, s, "POST", "manager", "/api/employer/1/members/999/manual", `{"date":"2026-01-05","minutes":10}`); r.code != 403 {
		t.Fatalf("out-of-scope manual add gave %d", r.code)
	}
	if r := call(t, s, "PUT", "manager", "/api/employer/1/settings", `{}`); r.code != 403 {
		t.Fatalf("scoped company settings gave %d", r.code)
	}
	clock := call(t, s, "GET", "manager", "/api/employer/1/clock", "")
	access, _ := clock.body["data"].(map[string]any)["access"].(map[string]any)
	if access["can_add"] != true || access["can_edit"] != true || access["can_delete"] != true || access["can_manage"] != true {
		t.Fatalf("manager clock access = %v", access)
	}
}
