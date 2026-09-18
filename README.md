# Job Scout

Job Scout monitors job sites. It stores each new job URL. It sends a webhook when a job passes the filters.

The pipeline uses [Temporal](https://temporal.io/). This project started as a Python/FastAPI app. It is now a Go service. The goal is idiomatic Go, few external libraries, and explicit control flow.

**Start here:** [QUICKSTART.md](QUICKSTART.md) — clone the repo, set `.env`, and start the stack on Windows, macOS, or a Raspberry Pi.

## Screenshots

Add PNG files in `docs/screenshots/` with these names. The slots below show those files when they exist.

![Job list](docs/screenshots/jobs.png)

![Temporal UI](docs/screenshots/temporal-ui.png)

![Notify in action](docs/screenshots/notify.png)

## Architecture

| Part | Role |
|------|------|
| **API** (`internal/api`) | `net/http` server. Starts workflows. Exposes read endpoints. Runs migrations and seeds settings on startup. |
| **Worker** (`internal/pipeline`) | Temporal worker. Hosts the workflow and the activities. |
| **Temporal** | Workflow orchestration and state. |
| **PostgreSQL** | Primary data store. |

The API and the worker are the same Go binary. The first argument selects the process: `jobscout api` or `jobscout worker`.

`make up` starts Postgres, Temporal, the API, and the worker. On boot, the API creates or updates two Temporal interval schedules: `jobscout-scrape` and `jobscout-notify`.

The worker must open TCP to the host in `WEBHOOK_BASE`. A timeout or connect error is an environment failure, not a filter failure.

Automated tests POST only to `httptest` servers. They must not use live webhook URLs, LAN addresses, or secrets.

### Pipeline (Temporal workflows)

Two workflows run on `main-task-queue`:

1. **ScrapeTick** — scrape enabled due providers (LinkedIn every 15 minutes unless `force=1`). Store every distinct job URL. No filters. No webhook POST.
2. **NotifyTick** — filter `pending` jobs, claim a batch, POST one JSON `{ "notify": "<WEBHOOK_ID>", "message": "..." }` to `WEBHOOK_BASE`. Skip the POST when the claim is empty.

Manual HTTP starts unique workflow IDs. `/run` and `/notify` are not blocked when a scheduled tick is still running.

## Dependencies

The module uses a small set of libraries:

- `go.temporal.io/sdk` — Temporal SDK
- `github.com/jackc/pgx/v5` — Postgres driver (behind stdlib `database/sql`)
- `golang.org/x/net/html` — HTML parsing

All other code uses the standard library (`net/http`, `database/sql`, `encoding/json`, `log/slog`, `embed`).

## Project structure

```text
.
├── docs/screenshots/           # PNG captures for this README
├── data/                       # local Postgres data (gitignored)
├── config/                     # Temporal dynamic config
├── docker-compose.yml          # postgres, temporal, temporal-ui, api, worker
├── Makefile
├── QUICKSTART.md
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

## Tools for local work

You need Docker and Docker Compose to run the stack. You need Go 1.25+ only for local builds and tests. You need Make only if you use the `make` targets (macOS and Raspberry Pi). Windows can use `docker compose` as in [QUICKSTART.md](QUICKSTART.md).

## Make commands

```bash
make up           # build and start the stack
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
make notify       # POST /api/v0/notify
```

To scrape or notify now without waiting for the schedule:

```bash
make run                 # POST /api/v0/run (force off; LinkedIn still waits for cadence unless force=1)
curl -X POST "http://localhost:8001/api/v0/run?force=1"
make notify              # POST /api/v0/notify
```

## API endpoints

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

## Ideas

- A UI
- Custom LLM resume, customized for each job
- LLM filtering step (not everything can be simple rules, though simple rules are _fast_)

## Inspiration

- [JobScout by Krishna](https://github.com/krishnavalliappan/JobScout)
- [JobScout.ai by abhinav-m22](https://github.com/abhinav-m22/JobScout.ai) | [App](https://jobscout-ai.vercel.app/)

## Contributing

1. Fork the repository.
2. Create a feature branch.
3. Commit your changes.
4. Push the branch.
5. Create a pull request.
