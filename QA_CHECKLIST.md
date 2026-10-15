# Hajir Desktop Agent — Release QA Checklist

Run this before marking any desktop-agent (MyMonitor) release live. Covers tracking
mode, capture, permissions, and tamper alerts. Last updated for **v2.1.5**.

---

## 0. Pre-check — confirm the new build is actually live
- [ ] Downloads page (`/api/downloads`) shows the new version for **Mac, Windows, Linux** — not the previous one.
- [ ] App footer shows the new **Version** number after reinstall.
- [ ] `grep HAJIR_API_URL .env.production` on the server = `https://backend.veloxlabs.net/api/v2`.

> If the version didn't change, the build/deploy didn't take. Stop and fix that first — every test below would be on old code. Bump `cloud/heartbeat.go` AppVersion for each release so filenames are cache-busted.

## 1. Manual = no auto-start
- [ ] Fresh install -> "Set up this computer" -> **Manual** -> sign in.
- [ ] Timer at 00:00:00, **Start** button shown, **no** "Automatic tracking is on" note. Nothing tracked yet.
- [ ] Press **Start** -> timer runs, screenshots begin (confirm on dashboard).
- [ ] Press **End** -> timer and screenshots stop.
- [ ] PASS: Tracking happens only between Start and End.

## 2. In-app toggle (policy = Flexible, default)
- [ ] **Tracking** toggle (Manual | Automatic) visible under the timer.
- [ ] Switch to **Automatic** -> tracking starts immediately, note appears.
- [ ] Switch to **Manual** -> auto-tracking stops; Start button returns.
- [ ] Quit + reopen -> remembers last choice (persisted in `data/client.json`).

## 3. Employer lock — Always automatic
- [ ] Dashboard -> member settings -> **Who controls tracking mode -> Always automatic** -> Save.
- [ ] App after ~1 min (heartbeat) or reopen: toggle **hidden**, auto note shown, tracking runs.
- [ ] PASS: Employee cannot switch to manual.

## 4. Employer lock — Always manual
- [ ] Dashboard -> member settings -> **Always manual** -> Save.
- [ ] App after heartbeat: toggle **hidden**, only **Start/End** shown, no auto-start.
- [ ] PASS: Forced manual; employee controls start/end but can't switch to auto.

## 5. Dual-monitor capture
- [ ] Second display connected, tracking on, let a screenshot fire.
- [ ] Dashboard capture shows **both screens** in one image, laid out side by side.
- [ ] PASS: No single-screen-only grab.

## 6. Screen Recording permission sticks (macOS)
- [ ] Grant Screen Recording once; quit + reopen several times.
- [ ] No repeat permission prompt; screenshots keep working.
- [ ] `codesign -dvvv ~/Applications/MyMonitor.app` shows `Authority=Hajir Local Signing` (or Developer ID), `flags=0x0(none)` — not adhoc.
- [ ] PASS: In-process capture fixed the re-prompt.

## 7. Tamper / offline alert
- [ ] Member tracking (clocked in) -> **quit** the app.
- [ ] Dashboard devices tab within ~1 min: amber banner + **"Not reporting"** badge on that device.
- [ ] PASS: Alert fires.

---

## Highest-risk areas per release
- **Mode wiring** (sections 1-4) — precedence is: employer lock > employee app choice > member default.
- **CGO capture** (sections 5-6) — `capture/screenshot_darwin.go` can't be compiled outside macOS; the Mac build (`make installer-gui-macos-prod`) and the server's containerized Go build (`./release-server.sh`) are the real compile checks.

## Deploy reference
```bash
# Mac — push + build + upload mac agent
cd device_productivity && git push
make installer-gui-macos-prod
scp dist/MyMonitor-GUI-Setup-macos.zip root@152.53.124.158:/opt/hajir/hajir-monitor/downloads/MyMonitor-GUI-Setup-macos-v<VERSION>.zip

# Server — Windows/Linux + hub, then dashboard
ssh root@152.53.124.158
cd /opt/hajir/hajir-monitor && git checkout -- .env.production && git pull && ./release-server.sh
cd /opt/hajir/hajir-dashboard && git pull && docker compose up -d --build
```
