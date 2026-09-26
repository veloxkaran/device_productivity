# Device Activity Hub

`my-monitor` now runs in two modes from the same binary:

| Mode | Command | Runs on | Purpose |
|---|---|---|---|
| Agent | `./my-monitor` | each employee's computer | tracks time/activity/screenshots, syncs to the hub |
| Hub | `./my-monitor hub` (or `make hub`) | one server | stores all employees' data, serves the employer dashboard, pushes live updates over WebSocket |

hajir-v2 is **not modified**. The hub only calls the existing `GET /api/v2/chat/verify-token` to identify the employer and which companies they own.

```
Employee PC: my-monitor ──sync + heartbeat (device token)──► HUB :4010 (own SQLite + files)
                                                               ▲        │ WebSocket /ws
hajir-v2  ◄── verify-token (employer's login token) ───────────┘        ▼
                                          hajir_dashboard /employer/dashboard/activity
```

## Environment (hub)

| Var | Default | |
|---|---|---|
| `HUB_ADDR` | `:4010` | listen address |
| `HUB_PUBLIC_URL` | `http://localhost:4010` | used in screenshot links and the pairing info |
| `HAJIR_API_URL` | `http://localhost:8001/api/v2` | hajir-v2 API base |
| `HUB_ALLOWED_ORIGINS` | `http://localhost:3000` | comma-separated dashboard origins |
| `HUB_DATA_DIR` | `hub-data` | `hub.db`, `screenshots/`, `secret.key` |
| `HUB_TIMEZONE` | `Asia/Kathmandu` | day boundaries for "today" |
| `HUB_SECRET` | auto-generated in data dir | signs screenshot URLs |
| `HUB_SCREENSHOT_RETENTION_DAYS` / `HUB_SAMPLE_RETENTION_DAYS` | 30 / 90 | auto-pruned every 6 h |
| `HUB_ONLINE_THRESHOLD_SECONDS` | 90 | no heartbeat for this long = offline |

## Pairing an employee

1. Employer: Activity page → "Add device" → pick employee → copy the token (shown once).
2. Employee: open `http://localhost:8090/cloud`, paste hub URL + token, Connect, then restart my-monitor.
3. The agent sends a heartbeat every 20 s and syncs data every minute.

## API

Device (Bearer `hdv_…`): `GET /api/sync/config`, `POST /api/heartbeat`, `POST /api/sync/time-entries`, `POST /api/sync/activity`, `POST /api/sync/screenshots`.

Employer (Bearer = the hajir login token; must be `is_owner` of the company):
`GET|POST /api/employer/{company}/devices`, `DELETE /api/employer/{company}/devices/{id}`,
`GET /api/employer/{company}/time-entries?user_id&date_from&date_to`,
`GET /api/employer/{company}/timeline?user_id&date`, `GET /api/employer/{company}/screenshots?user_id&date`,
`GET /api/employer/{company}/overview?date` (team table + totals: active/idle/offline/late/absent/avg hours),
`GET /api/employer/{company}/members/{user}/day?date` (clock in/out, tracked/active/idle, 24 h bar, 10-min screenshot slots),
`GET /api/employer/{company}/monthly?month=YYYY-MM` (per-day heatmaps, totals, averages, % vs last month, top/least 5),
`GET|PUT /api/employer/{company}/settings` (`work_start`, `work_end`, `grace_minutes`, `weekly_off`) — drives shift, late, absent.
`POST /api/employer/{company}/members/{user}/manual` `{date, minutes, note}`, `DELETE /api/employer/{company}/manual/{id}`.

Device also: `POST /api/sync/breaks` `[{start, end|null}]`. Heartbeat `status` may be `break`; `app_name` = frontmost app.

Working hours = tracked (clock-in time) − breaks + manual.

WebSocket: `ws://HUB/ws?token=<hajir token>&company_id=<id>` → messages `{"event":"device-activity-updated","data":{"kind":"heartbeat|activity|time-entries|screenshot|created|revoked","summary":{…same as one /devices row…}}}`.

## Limitations

- Only the company **owner** can view (verify-token exposes `is_owner`, not sub-employer permissions).

## Desktop app (v2.0)

`./my-monitor` now opens a compact desktop window (Chrome/Edge app mode, 400×760; falls back to the default browser) at `http://127.0.0.1:8090/app`. Pass `--no-window` or set `MM_NO_WINDOW=1` to skip it.

- **Sign in with Hajir:** employees use their normal Hajir email/phone + password (`POST {HAJIR_API_URL}/candidate/login`). The agent then calls the hub's `POST /api/device/register` with that token; the hub checks it with `verify-token`, picks the company (asks if there are several) and issues the device token. No pasting tokens.
- **Screens:** Sign in → Start/Stop Tracking + Break → idle prompt ("Let's resume!") after `idle_minutes` (default 5) with a reason, synced to the hub as idle reports.
- **Server URLs are baked in at build time** (no settings screen for employees):
  `make package-macos HAJIR_API_URL=https://your-api/api/v2 HUB_URL=https://your-hub`
  Defaults are `http://localhost:8001/api/v2` and `http://localhost:4010`. The same env vars at runtime override them (handy for development).
- The local server now listens on `127.0.0.1` only; the app API also rejects other hosts and requests without the `X-Hajir-Client` header.
