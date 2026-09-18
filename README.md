# job-scout

Job Scout is yet another homegrown application designed to monitor job sites for job postings, filter, and send alerts. What makes this one different is that it is built on a Temporal-orchestrated pipeline. Why? Because I already had a Python script to monitor for job changes, and I wanted to experiment and learn with Temporal orchestration.

This started as a Python/FastAPI app and was rewritten in Go as a learning exercise: idiomatic Go, minimal external dependencies, explicit over clever.

**Future Ideas**
- A UI
- Custom LLM resume, customized for each job
- LLM filtering step (not everything can be simple rules, though simple rules are _fast_)

Inspiration:
- [JobScout by Krishna](https://github.com/krishnavalliappan/JobScout)
- [JobScout.ai by abhinav-m22](https://github.com/abhinav-m22/JobScout.ai) | [App](https://jobscout-ai.vercel.app/)

## Architecture Overview

- **API** (`internal/api`): `net/http` server that triggers workflows and exposes read endpoints. Also runs migrations + seeds settings on startup.
- **Worker** (`internal/pipeline`): Temporal worker hosting the workflow + activities.
- **Temporal**: workflow orchestration and state management.
- **PostgreSQL**: primary data store.

The API and worker are the same Go binary, selected by the first arg (`jobscout api` / `jobscout worker`).

`make up` starts Postgres, Temporal, the API, and the worker. The API process creates or updates two Temporal interval schedules (`jobscout-scrape`, `jobscout-notify`) on boot.

The API and worker containers must be able to open TCP to the LAN Home Assistant host `192.168.1.222:8123`. Docker Desktop usually routes to the LAN; a timeout or connect error is an environment failure, not a filter failure.

Automated tests never POST to the live `allenjobhit` webhook.

### Pipeline (Temporal workflows)

Two workflows run on the `main-task-queue`:

1. **ScrapeTick** – scrape enabled due providers (LinkedIn every 15 minutes unless `force=1`), store every distinct job URL. No filters and no webhook POST.
2. **NotifyTick** – filter `pending` jobs, claim a batch, POST one JSON `{ "notify": "<WEBHOOK_ID>", "message": "..." }` to `WEBHOOK_BASE`. Skip the POST when the claim is empty.

Manual HTTP starts unique workflow IDs so `/run` and `/notify` are not blocked when a scheduled tick is still running.

## Dependencies (intentionally minimal)

- `go.temporal.io/sdk` – Temporal SDK
- `github.com/jackc/pgx/v5` – Postgres driver (used behind stdlib `database/sql`)
- `golang.org/x/net/html` – HTML parsing

Everything else is the standard library (`net/http`, `database/sql`, `encoding/json`, `log/slog`, `embed`).

## Project Structure

```bash
.
├── data/                       # local Postgres data (gitignored)
├── config/                     # Temporal dynamic config
├── docker-compose.yml          # postgres, temporal, temporal-ui, api, worker
├── Makefile
└── job-scout/                  # Go module
    ├── go.mod
    ├── Dockerfile              # multi-stage Go build
    ├── cmd/jobscout/main.go    # entrypoint: "api" | "worker"
    └── internal/
        ├── config/             # env-based config (single source of truth)
        ├── db/                 # connection, embedded migrations + seeds, queries
        ├── scraper/            # provider interface, LinkedIn (x/net/html), service
        ├── pipeline/           # temporal client, workflow, activities, worker
        └── api/                # ServeMux routes + handlers
```

## Prerequisites

- Docker and Docker Compose
- Go 1.25+ (only for local builds/tests; the stack runs in containers)
- Make

## Quick Start

1. Clone the repository.
2. Copy `job-scout/.env.sample` to `job-scout/.env`. Set Postgres credentials, the webhook (`WEBHOOK_BASE`, `WEBHOOK_ID`), and OAuth (`CLIENT_ID`, `CLIENT_SECRET`). Do not use `WEBHOOK_URL`; it is not the send target.
3. (Optional) Tune search settings in `job-scout/internal/db/seed/` before first run; they seed the DB on startup.
4. Start Postgres, Temporal, the API, and the worker:

```bash
make up
```

5. Manual acceptance (not CI): set `.env` webhook to local Home Assistant, then:

```bash
curl -X POST "http://localhost:8001/api/v0/run?force=1"
# wait until GET /api/v0/jobs shows rows
curl -X POST "http://localhost:8001/api/v0/notify"
```

Confirm the Home Assistant automation on `allenjobhit`. Automated tests never POST to that live webhook.

Scheduled scrape (default 60s) and notify (default 300s) also run from Temporal after API boot. To scrape or notify now without waiting:

```bash
make run                 # POST /api/v0/run (force off; LinkedIn still waits for cadence unless force=1)
curl -X POST "http://localhost:8001/api/v0/run?force=1"
make notify              # POST /api/v0/notify
```

## Make Commands

```bash
make up           # build & start the stack
make down         # stop
make restart      # restart containers
make build        # build images only
make logs         # follow logs
make clean        # down + prune volumes/images
make go-build     # compile the Go binary locally
make go-test      # run Go tests
make fmt          # gofmt the module
make vet          # go vet the module
make connect-db   # psql into the postgres container
make run          # POST /api/v0/run
make notify        # POST /api/v0/notify
```

## API Endpoints

Base path: `http://localhost:8001/api/v0`

| Method | Path                     | Description                              |
|--------|--------------------------|------------------------------------------|
| POST   | `/run`                   | Start ScrapeTick (`?force=1` skips LinkedIn cadence) |
| POST   | `/notify`                | Start NotifyTick (filters, claim, Home Assistant POST) |
| POST   | `/scrape`                | Debug-only sync scrape (no Temporal); prefer `/run` |
| GET    | `/health`                | Health check                             |
| GET    | `/db-test`               | DB connectivity check                    |
| GET    | `/temporal-test`         | Temporal connectivity check              |
| GET    | `/search-settings`       | Universal search settings                |
| GET    | `/scraper-settings`      | Per-source scraper settings              |
| GET    | `/scraper-settings/all`  | All scraper settings                     |
| GET    | `/jobs`                  | List jobs (`?limit&offset`), includes `state` |
| GET    | `/jobs/stats`            | Job counts. `new_jobs` is the `pending` state count; `by_state` lists every state |
| GET    | `/workflow/{id}`         | Workflow status                          |
| GET    | `/config`                | Effective config                         |

## Monitoring

- Temporal UI: http://localhost:8082

## Contributing

1. Fork the repository
2. Create a feature branch
3. Commit your changes
4. Push to the branch
5. Create a Pull Request
