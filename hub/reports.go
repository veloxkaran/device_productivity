package hub

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type dayStat struct {
	Worked  float64
	Active  float64
	Idle    float64
	Break   float64
	Manual  float64
	FirstIn *time.Time
	LastOut *time.Time
	Open    bool
	Hourly  [24][2]float64
}

type statsIndex map[int64]map[string]*dayStat

func (ix statsIndex) get(user int64, day string) *dayStat {
	if ix[user] == nil {
		ix[user] = map[string]*dayStat{}
	}
	if ix[user][day] == nil {
		ix[user][day] = &dayStat{}
	}
	return ix[user][day]
}

func (st *dayStat) Working() float64 {
	v := st.Worked - st.Break + st.Manual
	if v < 0 {
		return 0
	}
	return v
}

func (s *Server) splitDays(from, to time.Time, fn func(day string, secs float64)) {
	cur := from
	for cur.Before(to) {
		local := cur.In(s.cfg.Location)
		dayEnd := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, s.cfg.Location).Add(24 * time.Hour)
		segEnd := minTime(dayEnd, to)
		fn(s.dayKey(cur), segEnd.Sub(cur).Seconds())
		cur = segEnd
	}
}

func (s *Server) dayKey(t time.Time) string { return t.In(s.cfg.Location).Format("2006-01-02") }

func (s *Server) collect(companyID, userID int64, from, to time.Time) (statsIndex, error) {
	ix := statsIndex{}
	now := time.Now()

	entries, err := s.store.TimeEntries(companyID, userID, nil, from, to)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		end := now
		if e.ClockOut != nil {
			end = *e.ClockOut
		}
		uid := e.UserID
		s.splitDays(maxTime(e.ClockIn, from), minTime(end, to), func(day string, v float64) { ix.get(uid, day).Worked += v })
		inDay := ix.get(e.UserID, s.dayKey(e.ClockIn))
		if !e.ClockIn.Before(from) && (inDay.FirstIn == nil || e.ClockIn.Before(*inDay.FirstIn)) {
			t := e.ClockIn
			inDay.FirstIn = &t
		}
		if e.ClockOut == nil {
			ix.get(e.UserID, s.dayKey(minTime(now, to))).Open = true
		} else if !e.ClockOut.After(to) {
			outDay := ix.get(e.UserID, s.dayKey(*e.ClockOut))
			if outDay.LastOut == nil || e.ClockOut.After(*outDay.LastOut) {
				t := *e.ClockOut
				outDay.LastOut = &t
			}
		}
	}

	breaks, err := s.store.Breaks(companyID, userID, from, to)
	if err != nil {
		return nil, err
	}
	for _, b := range breaks {
		end := now
		if b.End != nil {
			end = *b.End
		}
		uid := b.UserID
		s.splitDays(maxTime(b.Start, from), minTime(end, to), func(day string, v float64) { ix.get(uid, day).Break += v })
	}

	manual, err := s.store.Manual(companyID, userID, s.dayKey(from), s.dayKey(to))
	if err != nil {
		return nil, err
	}
	for _, m := range manual {
		ix.get(m.UserID, m.Day).Manual += float64(m.Seconds)
	}

	var prev *SampleRow
	err = s.store.EachSample(companyID, userID, from, to, func(r SampleRow) {
		if prev != nil && prev.DeviceID == r.DeviceID {
			gap := r.CapturedAt.Sub(prev.CapturedAt)
			if gap > maxSampleGap {
				gap = maxSampleGap
			}
			st := ix.get(prev.UserID, s.dayKey(prev.CapturedAt))
			h := prev.CapturedAt.In(s.cfg.Location).Hour()
			if prev.IsActive {
				st.Active += gap.Seconds()
				st.Hourly[h][0] += gap.Seconds()
			} else {
				st.Idle += gap.Seconds()
				st.Hourly[h][1] += gap.Seconds()
			}
		}
		p := r
		prev = &p
	})
	return ix, err
}

type member struct {
	UserID int64
	Name   string
	Device *Device
}

func (s *Server) members(companyID int64) ([]member, error) {
	devices, err := s.store.Devices(companyID, 0, false)
	if err != nil {
		return nil, err
	}
	seen := map[int64]int{}
	var list []member
	for _, d := range devices {
		if i, ok := seen[d.UserID]; ok {
			if d.LastSeenAt != nil && (list[i].Device.LastSeenAt == nil || d.LastSeenAt.After(*list[i].Device.LastSeenAt)) {
				list[i].Device = d
			}
			continue
		}
		seen[d.UserID] = len(list)
		list = append(list, member{UserID: d.UserID, Name: d.EmployeeName, Device: d})
	}
	return list, nil
}

func (s *Server) isWorkday(cfg Settings, day time.Time) bool {
	wd := strings.ToLower(day.Weekday().String()[:3])
	for _, off := range cfg.WeeklyOff {
		if strings.ToLower(off) == wd {
			return false
		}
	}
	return true
}

func (s *Server) lateCutoff(cfg Settings, day time.Time) time.Time {
	hh, mm := 9, 0
	fmt.Sscanf(cfg.WorkStart, "%d:%d", &hh, &mm)
	local := day.In(s.cfg.Location)
	return time.Date(local.Year(), local.Month(), local.Day(), hh, mm, 0, 0, s.cfg.Location).Add(time.Duration(cfg.GraceMinutes) * time.Minute)
}

func (s *Server) isOnline(d *Device) bool {
	return d != nil && d.RevokedAt == nil && d.LastSeenAt != nil && d.LastStatus != "offline" && time.Since(*d.LastSeenAt) < s.cfg.OnlineThreshold
}

func secs(v float64) int64 { return int64(v) }

var overviewSorts = []string{"name", "status", "shift", "clock_in", "clock_out", "worked_seconds", "last_screenshot"}
var overviewFilters = []string{"all", "active", "idle", "break", "offline", "late", "absent"}

type overviewRow struct {
	data     map[string]any
	userID   int64
	name     string
	status   string
	shift    string
	clockIn  *time.Time
	clockOut *time.Time
	worked   float64
	lastShot *time.Time
	late     bool
	absent   bool
}

func statusRank(status string) int {
	switch status {
	case "active":
		return 0
	case "idle":
		return 1
	case "break":
		return 2
	case "offline":
		return 3
	}
	return 4
}

func (o overviewRow) matches(filter string) bool {
	switch filter {
	case "active", "idle", "break":
		return o.status == filter
	case "offline":
		return statusRank(o.status) >= 3
	case "late":
		return o.late
	case "absent":
		return o.absent
	}
	return true
}

func sortOverview(rows []overviewRow, key string, desc bool) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		c := 0
		switch key {
		case "status":
			c = statusRank(a.status) - statusRank(b.status)
		case "shift":
			c = strings.Compare(a.shift, b.shift)
		case "clock_in", "clock_out", "last_screenshot":
			ta, tb := a.clockIn, b.clockIn
			if key == "clock_out" {
				ta, tb = a.clockOut, b.clockOut
			} else if key == "last_screenshot" {
				ta, tb = a.lastShot, b.lastShot
			}
			v, ok := cmpTimePtr(ta, tb)
			if !ok {
				return ta != nil
			}
			c = v
		case "worked_seconds":
			switch {
			case a.worked < b.worked:
				c = -1
			case a.worked > b.worked:
				c = 1
			}
		}
		if c != 0 {
			if desc {
				return c > 0
			}
			return c < 0
		}
		if n := strings.Compare(strings.ToLower(a.name), strings.ToLower(b.name)); n != 0 {
			if key == "name" && desc {
				return n > 0
			}
			return n < 0
		}
		return a.userID < b.userID
	})
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request, e *Employer, companyID int64) {
	q := r.URL.Query()
	paged := pagingRequested(q)
	var pg paging
	if paged {
		var err error
		if pg, err = parsePaging(q, overviewSorts, overviewFilters, "name"); err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
	}
	date := q.Get("date")
	start, end, err := s.dayBounds(date)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	isToday := s.dayKey(start) == s.dayKey(time.Now())
	cfg := s.store.GetSettings(companyID)
	members, err := s.members(companyID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load members")
		return
	}
	members = scopeMembers(scopeOf(r), members)
	ix, err := s.collect(companyID, 0, start, end)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not compute activity")
		return
	}
	shots, _ := s.store.CompanyScreenshots(companyID, 0, start, end)
	lastShot := map[int64]Screenshot{}
	for _, sh := range shots {
		lastShot[sh.UserID] = sh
	}

	workday := s.isWorkday(cfg, start)
	day := s.dayKey(start)
	var rows []overviewRow
	totals := map[string]any{}
	var active, idle, onBreak, offline, late, absent, workedCount int
	var workedSum float64

	for _, m := range members {
		st := ix[m.UserID][day]
		if st == nil {
			st = &dayStat{}
		}
		online := isToday && s.isOnline(m.Device)
		status := "offline"
		if online {
			status = m.Device.LastStatus
		}
		isLate := st.FirstIn != nil && st.FirstIn.After(s.lateCutoff(cfg, start))
		isAbsent := workday && st.Working() == 0 && st.FirstIn == nil && !start.After(time.Now())
		switch status {
		case "active":
			active++
		case "idle":
			idle++
		case "break":
			onBreak++
		default:
			offline++
		}
		if isLate {
			late++
		}
		if isAbsent {
			absent++
		}
		if st.Working() > 0 {
			workedCount++
			workedSum += st.Working()
		}
		var shot any
		var shotAt *time.Time
		if sh, ok := lastShot[m.UserID]; ok {
			shot = map[string]any{"id": sh.ID, "captured_at": isoPtr(&sh.CapturedAt), "url": s.screenshotURL(sh.ID)}
			t := sh.CapturedAt
			shotAt = &t
		}
		var out any
		var outAt *time.Time
		if !st.Open {
			out = isoPtr(st.LastOut)
			outAt = st.LastOut
		}
		rows = append(rows, overviewRow{
			userID: m.UserID, name: m.Name, status: status, shift: shiftLabel(cfg), clockIn: st.FirstIn, clockOut: outAt,
			worked: st.Working(), lastShot: shotAt, late: isLate, absent: isAbsent,
			data: map[string]any{
				"user_id": m.UserID, "employee_name": m.Name, "device_id": m.Device.ID, "device_name": m.Device.Name,
				"platform": m.Device.Platform, "online": online, "status": status, "is_clocked_in": st.Open,
				"current_app": m.Device.LastApp, "last_seen_at": isoPtr(m.Device.LastSeenAt),
				"clock_in": isoPtr(st.FirstIn), "clock_out": out,
				"tracked_seconds": secs(st.Worked), "worked_seconds": secs(st.Working()), "active_seconds": secs(st.Active), "idle_seconds": secs(st.Idle),
				"break_seconds": secs(st.Break), "manual_seconds": secs(st.Manual), "shift": shiftLabel(cfg),
				"late": isLate, "absent": isAbsent, "last_screenshot": shot,
			},
		})
	}
	totals["total_members"] = len(members)
	totals["active_members"] = active
	totals["idle_members"] = idle
	totals["break_members"] = onBreak
	totals["offline_members"] = offline
	totals["late_members"] = late
	totals["absent_members"] = absent
	totals["avg_worked_seconds"] = nil
	if workedCount > 0 {
		totals["avg_worked_seconds"] = int64(workedSum / float64(workedCount))
	}
	resp := map[string]any{"status": "success", "message": "successfully fetched", "date": day, "is_today": isToday, "workday": workday, "settings": cfg, "totals": totals}
	if paged {
		kept := []overviewRow{}
		for _, row := range rows {
			if row.matches(pg.Filter) && matchesSearch(row.name, pg.Search) {
				kept = append(kept, row)
			}
		}
		sortOverview(kept, pg.Sort, pg.Desc)
		var page []overviewRow
		page, resp["meta"] = pageSlice(kept, pg)
		rows = page
	}
	data := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		data = append(data, row.data)
	}
	resp["data"] = data
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleMemberDay(w http.ResponseWriter, r *http.Request, e *Employer, companyID int64) {
	userID, _ := strconv.ParseInt(r.PathValue("user"), 10, 64)
	if userID <= 0 {
		writeErr(w, http.StatusUnprocessableEntity, "invalid user")
		return
	}
	if denyMember(w, r, userID) {
		return
	}
	start, end, err := s.dayBounds(r.URL.Query().Get("date"))
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	cfg := s.store.GetSettings(companyID)
	devices, _ := s.store.Devices(companyID, userID, true)
	if len(devices) == 0 {
		writeErr(w, http.StatusNotFound, "no device registered for this employee")
		return
	}
	var dev *Device
	for _, d := range devices {
		if dev == nil || (d.LastSeenAt != nil && (dev.LastSeenAt == nil || d.LastSeenAt.After(*dev.LastSeenAt))) {
			dev = d
		}
	}
	ix, err := s.collect(companyID, userID, start, end)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not compute activity")
		return
	}
	day := s.dayKey(start)
	st := ix[userID][day]
	if st == nil {
		st = &dayStat{}
	}
	isToday := day == s.dayKey(time.Now())
	online := isToday && s.isOnline(dev)
	status := "offline"
	if online {
		status = dev.LastStatus
	}

	hourly := make([]map[string]any, 24)
	for h := 0; h < 24; h++ {
		hourly[h] = map[string]any{"hour": h, "active_seconds": secs(st.Hourly[h][0]), "idle_seconds": secs(st.Hourly[h][1])}
	}

	shots, _ := s.store.CompanyScreenshots(companyID, userID, start, end)
	bySlot := map[int][]map[string]any{}
	minH, maxH := 24, -1
	for h := 0; h < 24; h++ {
		if st.Hourly[h][0]+st.Hourly[h][1] > 0 {
			minH, maxH = min(minH, h), max(maxH, h)
		}
	}
	for _, sh := range shots {
		local := sh.CapturedAt.In(s.cfg.Location)
		slot := local.Hour()*6 + local.Minute()/10
		bySlot[slot] = append(bySlot[slot], map[string]any{"id": sh.ID, "captured_at": isoPtr(&sh.CapturedAt), "url": s.screenshotURL(sh.ID)})
		minH, maxH = min(minH, local.Hour()), max(maxH, local.Hour())
	}
	slots := []map[string]any{}
	if maxH >= 0 {
		now := time.Now()
		for slot := minH * 6; slot < (maxH+1)*6; slot++ {
			from := start.Add(time.Duration(slot*10) * time.Minute)
			if from.After(now) {
				break
			}
			to := from.Add(10*time.Minute - time.Second)
			list := bySlot[slot]
			if list == nil {
				list = []map[string]any{}
			}
			slots = append(slots, map[string]any{"start": isoPtr(&from), "end": isoPtr(&to), "screenshots": list})
		}
	}

	var out any
	if !st.Open {
		out = isoPtr(st.LastOut)
	}
	manualList, _ := s.store.Manual(companyID, userID, day, day)
	idleReports, _ := s.store.IdleReports(companyID, userID, start, end)
	writeOK(w, "successfully fetched", map[string]any{
		"user_id": userID, "employee_name": dev.EmployeeName, "device_name": dev.Name, "platform": dev.Platform, "app_version": dev.AppVersion,
		"date": day, "is_today": isToday, "online": online, "status": status, "is_clocked_in": st.Open,
		"present": st.FirstIn != nil || st.Working() > 0, "shift": shiftLabel(cfg), "late": st.FirstIn != nil && st.FirstIn.After(s.lateCutoff(cfg, start)),
		"clock_in": isoPtr(st.FirstIn), "clock_out": out,
		"tracked_seconds": secs(st.Worked), "worked_seconds": secs(st.Working()), "active_seconds": secs(st.Active), "idle_seconds": secs(st.Idle),
		"break_seconds": secs(st.Break), "manual_seconds": secs(st.Manual), "manual_entries": manualList, "idle_reports": idleReports,
		"hourly": hourly, "slots": slots, "screenshots_count": len(shots),
	})
}

type metricTotals struct {
	total   float64
	perDay  map[string]float64
	perUser map[int64]float64
	count   int
}

func (s *Server) monthMetrics(companyID int64, sc Scope, from, to time.Time) (map[string]*metricTotals, statsIndex, error) {
	ix, err := s.collect(companyID, 0, from, to)
	if err != nil {
		return nil, nil, err
	}
	for user := range ix {
		if !sc.Allows(user) {
			delete(ix, user)
		}
	}
	out := map[string]*metricTotals{}
	for _, k := range []string{"worked", "idle", "active", "manual"} {
		out[k] = &metricTotals{perDay: map[string]float64{}, perUser: map[int64]float64{}}
	}
	for user, days := range ix {
		for day, st := range days {
			vals := map[string]float64{"worked": st.Working(), "idle": st.Idle, "active": st.Active, "manual": st.Manual}
			for k, v := range vals {
				if v <= 0 {
					continue
				}
				m := out[k]
				m.total += v
				m.perDay[day] += v
				m.perUser[user] += v
				m.count++
			}
		}
	}
	return out, ix, nil
}

func pctChange(cur, prev float64) any {
	if prev <= 0 {
		return nil
	}
	return float64(int((cur-prev)/prev*1000)) / 10
}

func (s *Server) parseMonth(month string, now time.Time) (time.Time, time.Time, error) {
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, s.cfg.Location)
	if month != "" {
		t, err := time.ParseInLocation("2006-01", month, s.cfg.Location)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("month must be YYYY-MM")
		}
		first = t
	}
	return first, first.AddDate(0, 1, 0), nil
}

type attendance struct {
	present int
	late    int
	absent  int
}

func (s *Server) monthAttendance(cfg Settings, members []member, ix statsIndex, first, next, now time.Time) map[int64]*attendance {
	out := map[int64]*attendance{}
	for _, m := range members {
		out[m.UserID] = &attendance{}
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.cfg.Location)
	for d := first; d.Before(next) && !d.After(now); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		workday := s.isWorkday(cfg, d)
		for _, m := range members {
			st := ix[m.UserID][key]
			a := out[m.UserID]
			if st != nil && (st.FirstIn != nil || st.Working() > 0) {
				a.present++
			}
			if st != nil && st.FirstIn != nil && st.FirstIn.After(s.lateCutoff(cfg, d)) {
				a.late++
			}
			if workday && d.Before(today) && (st == nil || st.Working() == 0) {
				a.absent++
			}
		}
	}
	return out
}

func (s *Server) handleMonthly(w http.ResponseWriter, r *http.Request, e *Employer, companyID int64) {
	now := time.Now().In(s.cfg.Location)
	first, next, err := s.parseMonth(r.URL.Query().Get("month"), now)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	prevFirst := first.AddDate(0, -1, 0)
	sc := scopeOf(r)

	cur, ix, err := s.monthMetrics(companyID, sc, first, next.Add(-time.Second))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not compute report")
		return
	}
	prev, _, err := s.monthMetrics(companyID, sc, prevFirst, first.Add(-time.Second))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not compute report")
		return
	}
	members, _ := s.members(companyID)
	members = scopeMembers(sc, members)
	names := map[int64]string{}
	for _, m := range members {
		names[m.UserID] = m.Name
	}
	cfg := s.store.GetSettings(companyID)

	lateUsers, absentUsers := 0, 0
	for _, a := range s.monthAttendance(cfg, members, ix, first, next, now) {
		if a.late > 0 {
			lateUsers++
		}
		if a.absent > 0 {
			absentUsers++
		}
	}

	metrics := map[string]any{}
	for _, k := range []string{"worked", "idle", "active", "manual"} {
		m := cur[k]
		type ranked struct {
			UserID int64   `json:"user_id"`
			Name   string  `json:"employee_name"`
			Secs   float64 `json:"-"`
			Total  int64   `json:"seconds"`
		}
		var list []ranked
		for u, v := range m.perUser {
			list = append(list, ranked{UserID: u, Name: names[u], Secs: v, Total: int64(v)})
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Secs > list[j].Secs })
		top := list[:min(5, len(list))]
		least := make([]ranked, 0, 5)
		for i := len(list) - 1; i >= 0 && len(least) < 5; i-- {
			least = append(least, list[i])
		}
		days := []map[string]any{}
		for d := first; d.Before(next); d = d.AddDate(0, 0, 1) {
			key := d.Format("2006-01-02")
			days = append(days, map[string]any{"date": key, "seconds": int64(m.perDay[key])})
		}
		var avg any
		if m.count > 0 {
			avg = int64(m.total / float64(m.count))
		}
		var prevAvg float64
		if prev[k].count > 0 {
			prevAvg = prev[k].total / float64(prev[k].count)
		}
		var curAvg float64
		if m.count > 0 {
			curAvg = m.total / float64(m.count)
		}
		metrics[k] = map[string]any{
			"total_seconds": int64(m.total), "avg_seconds": avg, "change_pct": pctChange(curAvg, prevAvg),
			"days": days, "top": top, "least": least,
		}
	}

	writeOK(w, "successfully fetched", map[string]any{
		"month": first.Format("2006-01"), "total_members": len(members),
		"late_members": lateUsers, "absent_members": absentUsers, "metrics": metrics,
	})
}

var monthlyMemberSorts = []string{"name", "days_present", "late_days", "absent_days", "worked_seconds", "idle_seconds", "manual_seconds", "active_seconds"}

func (s *Server) handleMonthlyMembers(w http.ResponseWriter, r *http.Request, e *Employer, companyID int64) {
	q := r.URL.Query()
	pg, err := parsePaging(q, monthlyMemberSorts, []string{"all"}, "name")
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	now := time.Now().In(s.cfg.Location)
	first, next, err := s.parseMonth(q.Get("month"), now)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	sc := scopeOf(r)
	cur, ix, err := s.monthMetrics(companyID, sc, first, next.Add(-time.Second))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not compute report")
		return
	}
	members, err := s.members(companyID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load members")
		return
	}
	members = scopeMembers(sc, members)
	att := s.monthAttendance(s.store.GetSettings(companyID), members, ix, first, next, now)

	type row struct {
		name string
		vals map[string]float64
		data map[string]any
		uid  int64
	}
	rows := []row{}
	for _, m := range members {
		if !matchesSearch(m.Name, pg.Search) {
			continue
		}
		a := att[m.UserID]
		vals := map[string]float64{
			"days_present": float64(a.present), "late_days": float64(a.late), "absent_days": float64(a.absent),
			"worked_seconds": cur["worked"].perUser[m.UserID], "idle_seconds": cur["idle"].perUser[m.UserID],
			"manual_seconds": cur["manual"].perUser[m.UserID], "active_seconds": cur["active"].perUser[m.UserID],
		}
		data := map[string]any{"user_id": m.UserID, "employee_name": m.Name}
		for k, v := range vals {
			data[k] = int64(v)
		}
		rows = append(rows, row{name: m.Name, vals: vals, data: data, uid: m.UserID})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if pg.Sort != "name" && a.vals[pg.Sort] != b.vals[pg.Sort] {
			if pg.Desc {
				return a.vals[pg.Sort] > b.vals[pg.Sort]
			}
			return a.vals[pg.Sort] < b.vals[pg.Sort]
		}
		if n := strings.Compare(strings.ToLower(a.name), strings.ToLower(b.name)); n != 0 {
			if pg.Sort == "name" && pg.Desc {
				return n > 0
			}
			return n < 0
		}
		return a.uid < b.uid
	})
	page, meta := pageSlice(rows, pg)
	data := make([]map[string]any, 0, len(page))
	for _, rw := range page {
		data = append(data, rw.data)
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "message": "successfully fetched", "month": first.Format("2006-01"), "data": data, "meta": meta})
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request, e *Employer, companyID int64) {
	writeOK(w, "successfully fetched", s.store.GetSettings(companyID))
}

func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request, e *Employer, companyID int64) {
	var in Settings
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&in); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "invalid JSON")
		return
	}
	var hh, mm int
	if n, _ := fmt.Sscanf(in.WorkStart, "%d:%d", &hh, &mm); n != 2 || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		writeErr(w, http.StatusUnprocessableEntity, "work_start must be HH:MM")
		return
	}
	var eh, em int
	if in.WorkEnd == "" {
		in.WorkEnd = "18:00"
	}
	if n, _ := fmt.Sscanf(in.WorkEnd, "%d:%d", &eh, &em); n != 2 || eh < 0 || eh > 23 || em < 0 || em > 59 {
		writeErr(w, http.StatusUnprocessableEntity, "work_end must be HH:MM")
		return
	}
	in.WorkEnd = fmt.Sprintf("%02d:%02d", eh, em)
	if in.GraceMinutes < 0 || in.GraceMinutes > 240 {
		writeErr(w, http.StatusUnprocessableEntity, "grace_minutes must be 0-240")
		return
	}
	if in.ScreenshotIntervalSeconds == 0 {
		in.ScreenshotIntervalSeconds = DefaultSettings().ScreenshotIntervalSeconds
	}
	if in.ScreenshotIntervalSeconds < 30 || in.ScreenshotIntervalSeconds > 3600 {
		writeErr(w, http.StatusUnprocessableEntity, "screenshot_interval_seconds must be 30-3600")
		return
	}
	if in.ScreenshotQuality == 0 {
		in.ScreenshotQuality = DefaultSettings().ScreenshotQuality
	}
	if in.ScreenshotQuality < 30 || in.ScreenshotQuality > 95 {
		writeErr(w, http.StatusUnprocessableEntity, "screenshot_quality must be 30-95")
		return
	}
	if in.ScreenshotMaxWidth == 0 {
		in.ScreenshotMaxWidth = DefaultSettings().ScreenshotMaxWidth
	}
	if in.ScreenshotMaxWidth < 640 || in.ScreenshotMaxWidth > 3840 {
		writeErr(w, http.StatusUnprocessableEntity, "screenshot_max_width must be 640-3840")
		return
	}
	if in.ScreenshotRetentionDays == 0 {
		in.ScreenshotRetentionDays = s.store.GetSettings(companyID).ScreenshotRetentionDays
	}
	if in.ScreenshotRetentionDays < 7 || in.ScreenshotRetentionDays > 365 {
		writeErr(w, http.StatusUnprocessableEntity, "screenshot_retention_days must be 7-365")
		return
	}
	valid := map[string]bool{"sun": true, "mon": true, "tue": true, "wed": true, "thu": true, "fri": true, "sat": true}
	clean := []string{}
	for _, d := range in.WeeklyOff {
		if d = strings.ToLower(strings.TrimSpace(d)); valid[d] {
			clean = append(clean, d)
		}
	}
	in.WeeklyOff = clean
	in.WorkStart = fmt.Sprintf("%02d:%02d", hh, mm)
	if err := s.store.SaveSettings(companyID, in); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save settings")
		return
	}
	writeOK(w, "settings saved", in)
}

func memberSettingsPayload(s *Server, companyID, userID int64) map[string]any {
	own := s.store.MemberScreenshotInterval(companyID, userID)
	company := s.store.GetSettings(companyID).ScreenshotIntervalSeconds
	var override any
	if own > 0 {
		override = own
	}
	effective := company
	if own > 0 {
		effective = own
	}
	devs := []map[string]any{}
	if list, err := s.store.Devices(companyID, userID, false); err == nil {
		for _, d := range list {
			rt := s.store.GetDeviceRuntime(d.ID)
			online := d.LastSeenAt != nil && time.Since(*d.LastSeenAt) < 90*time.Second && d.LastStatus != "offline"
			devs = append(devs, map[string]any{
				"device_id": d.ID, "name": d.Name, "app_version": d.AppVersion, "online": online, "is_clocked_in": d.IsClockedIn,
				"applied_interval_seconds": rt.AppliedInterval, "capture_error": rt.CaptureError, "last_capture_at": rt.LastCaptureAt,
				"in_sync": rt.AppliedInterval == effective,
			})
		}
	}
	return map[string]any{"user_id": userID, "devices": devs, "member_tracking_enabled": s.store.MemberTrackingEnabled(companyID, userID), "company_activity_enabled": s.store.ActivityEnabled(companyID), "screenshot_interval_seconds": override, "company_screenshot_interval_seconds": company, "effective_screenshot_interval_seconds": effective, "tracking_mode": s.store.MemberTrackingMode(companyID, userID), "mode_audit": s.store.MemberModeAudit(companyID, userID, 10)}
}

func (s *Server) handleGetMemberSettings(w http.ResponseWriter, r *http.Request, e *Employer, companyID int64) {
	userID, _ := strconv.ParseInt(r.PathValue("user"), 10, 64)
	if userID <= 0 {
		writeErr(w, http.StatusUnprocessableEntity, "invalid user")
		return
	}
	if denyMember(w, r, userID) {
		return
	}
	writeOK(w, "successfully fetched", memberSettingsPayload(s, companyID, userID))
}

// handleSaveMemberSettings sets a per-member screenshot interval; null or 0 clears it (use company default).
func (s *Server) handleSaveMemberSettings(w http.ResponseWriter, r *http.Request, e *Employer, companyID int64) {
	userID, _ := strconv.ParseInt(r.PathValue("user"), 10, 64)
	if userID <= 0 {
		writeErr(w, http.StatusUnprocessableEntity, "invalid user")
		return
	}
	var in struct {
		ScreenshotIntervalSeconds *int    `json:"screenshot_interval_seconds"`
		TrackingMode              *string `json:"tracking_mode"`
		TrackingEnabled           *bool   `json:"tracking_enabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<12)).Decode(&in); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "invalid JSON")
		return
	}
	if in.TrackingMode != nil {
		m := *in.TrackingMode
		if m != "auto" && m != "manual" {
			writeErr(w, http.StatusUnprocessableEntity, "tracking_mode must be auto or manual")
			return
		}
		if err := s.store.SetMemberTrackingMode(companyID, userID, m, e.UserID, e.Name); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not save tracking mode")
			return
		}
	}
	if in.TrackingEnabled != nil {
		if err := s.store.SetMemberTrackingEnabled(companyID, userID, *in.TrackingEnabled, e.UserID, e.Name); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not save tracking state")
			return
		}
	}
	if in.ScreenshotIntervalSeconds != nil {
		secs := *in.ScreenshotIntervalSeconds
		if secs != 0 && (secs < 30 || secs > 3600) {
			writeErr(w, http.StatusUnprocessableEntity, "screenshot_interval_seconds must be 30-3600")
			return
		}
		if err := s.store.SetMemberScreenshotInterval(companyID, userID, secs); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not save member settings")
			return
		}
	}
	writeOK(w, "member settings saved", memberSettingsPayload(s, companyID, userID))
}

func shiftLabel(cfg Settings) string {
	var sh, eh int
	fmt.Sscanf(cfg.WorkStart, "%d", &sh)
	fmt.Sscanf(cfg.WorkEnd, "%d", &eh)
	kind := "Day"
	if sh >= 18 || sh < 5 || eh < sh {
		kind = "Night"
	}
	return fmt.Sprintf("%s (%s–%s)", kind, cfg.WorkStart, cfg.WorkEnd)
}

func (s *Server) handleAddManual(w http.ResponseWriter, r *http.Request, e *Employer, companyID int64) {
	userID, _ := strconv.ParseInt(r.PathValue("user"), 10, 64)
	var in struct {
		Date    string `json:"date"`
		Minutes int64  `json:"minutes"`
		Note    string `json:"note"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&in); err != nil || userID <= 0 {
		writeErr(w, http.StatusUnprocessableEntity, "invalid request")
		return
	}
	day, err := time.ParseInLocation("2006-01-02", in.Date, s.cfg.Location)
	if err != nil || day.After(time.Now()) {
		writeErr(w, http.StatusUnprocessableEntity, "date must be YYYY-MM-DD and not in the future")
		return
	}
	if denyMember(w, r, userID) {
		return
	}
	if in.Minutes <= 0 || in.Minutes > 24*60 {
		writeErr(w, http.StatusUnprocessableEntity, "minutes must be between 1 and 1440")
		return
	}
	if devices, _ := s.store.Devices(companyID, userID, true); len(devices) == 0 {
		writeErr(w, http.StatusNotFound, "no device registered for this employee")
		return
	}
	id, err := s.store.AddManual(companyID, userID, e.UserID, in.Date, in.Minutes*60, truncate(in.Note, 255))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save manual time")
		return
	}
	s.broker.Publish(companyID, userID, "device-activity-updated", map[string]any{"kind": "manual", "summary": map[string]any{"user_id": userID}})
	writeJSON(w, http.StatusCreated, map[string]any{"status": "success", "message": "manual time added", "data": map[string]any{"id": id}})
}

func (s *Server) handleDeleteManual(w http.ResponseWriter, r *http.Request, e *Employer, companyID int64) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	entry, err := s.store.ManualByID(companyID, id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "manual entry not found")
		return
	}
	if denyMember(w, r, entry.UserID) {
		return
	}
	ok, err := s.store.DeleteManual(companyID, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not delete")
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "manual entry not found")
		return
	}
	s.broker.Publish(companyID, entry.UserID, "device-activity-updated", map[string]any{"kind": "manual", "summary": map[string]any{"user_id": entry.UserID}})
	writeOK(w, "manual time removed", nil)
}
