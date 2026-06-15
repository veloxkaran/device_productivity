# My Monitor

A lightweight, self-hosted productivity monitor for your desktop. Tracks active/idle time, takes periodic screenshots while you're clocked in, and exposes everything through a local web dashboard.

## Features

- **Time tracking** — clock in/out to log work sessions
- **Activity monitoring** — detects active vs idle state every 30 seconds (5-minute idle threshold)
- **Screenshot capture** — takes a screenshot every 3 minutes while clocked in, stored locally
- **Web dashboard** — browse screenshots, view activity history, and manage sessions at `http://localhost:8080`
- **Cloud sync** — optional push of screenshots and activity data to a remote server
- **Cross-platform** — runs on macOS, Linux, and Windows

## Tech Stack

- **Go 1.21** — single binary, no runtime dependencies
- **SQLite** — local database via `modernc.org/sqlite` (pure Go, no CGo)
- **HTML templates** — embedded in the binary at build time

## Quick Start

### Prerequisites

- Go 1.21+

### Build & Run

```bash
go build -o my-monitor .
./my-monitor
```

The server starts on `http://localhost:8080`.

**Default credentials:** `admin` / `admin`
> Change the password immediately via **Settings → Setup**.

### macOS / Mac Intel (setup script)

```bash
./setup.sh
./my-monitor
```

## Routes

| URL | Description |
|-----|-------------|
| `http://localhost:8080/` | Dashboard — screenshot gallery + activity feed |
| `http://localhost:8080/time` | Time tracker — clock in/out |
| `http://localhost:8080/setup` | Settings — change password, configure autostart |
| `http://localhost:8080/cloud` | Cloud sync configuration |

## Data

All data is stored under `data/`:

```
data/
  monitor.db          # SQLite database (activity, time entries, screenshot metadata)
  screenshots/        # PNG screenshots (gitignored)
  cloud.json          # Cloud sync config (optional)
```

## Cloud Sync

To enable cloud sync, create `data/cloud.json`:

```json
{
  "url": "https://your-server.example.com/ingest",
  "sync_token": "your-secret-token"
}
```

The syncer runs every 5 minutes and pushes screenshots and activity records to the configured endpoint.

## Build for Distribution

```bash
# macOS Intel
GOARCH=amd64 GOOS=darwin go build -o my-monitor .

# macOS Apple Silicon
GOARCH=arm64 GOOS=darwin go build -o my-monitor .

# Linux
GOARCH=amd64 GOOS=linux go build -o my-monitor .
```

A `.dmg` installer can be built with:

```bash
./build-dmg.sh
```
