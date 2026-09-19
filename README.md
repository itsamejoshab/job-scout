# Job Scout

Job Scout is a homegrown application that monitors job sites, filters job postings, and sends alerts. It uses distributed services because the project is also an experiment with [Temporal](https://temporal.io/) orchestration.

The architecture is more complex than a single script, but it separates scraping, filtering, notification, and durable workflow state. This structure also gives contributors clear places to add providers and pipeline stages.

**Future Ideas**
- A UI
- Custom LLM resume, customized for each job
- LLM filtering step (not everything can be simple rules, though simple rules are _fast_)

Inspiration: 
- [JobScout by Krishna](https://github.com/krishnavalliappan/JobScout)
- [JobScout.ai by abhinav-m22 ](https://github.com/abhinav-m22/JobScout.ai) | [App](https://jobscout-ai.vercel.app/)

**Start here:** [QUICKSTART.md](QUICKSTART.md) — clone the repo, set `.env`, and start the stack on Windows, macOS, or a Raspberry Pi.

## Screenshots

Add PNG files in `docs/screenshots/` with these names. The slots below show those files when they exist.

![Dashboard](docs/screenshots/dashboard.png)

![Filtering](docs/screenshots/settings.png)

![Jobs and Notifications](docs/screenshots/jobs.png)

## Architecture

| Part | Role |
|------|------|
| **API and UI** (`internal/api`, `webui`) | `net/http` server. Starts workflows, exposes the API, and serves the operator UI. Runs migrations and seeds settings on startup. |
| **Worker** (`internal/pipeline`) | Temporal worker. Hosts the workflow and the activities. |
| **Temporal** | Workflow orchestration and state. |
| **PostgreSQL** | Primary data store. |

The API and the worker are the same Go binary. The first argument selects the process: `jobscout api` or `jobscout worker`.

`make up` starts Postgres, Temporal, the API, and the worker. On boot, the API creates or updates two Temporal schedules: `jobscout-scrape` and `jobscout-notify`. The default scrape interval is 10 minutes. The default notify interval is the same period, with a 9-minute offset, and only between 07:30 and 21:00 in `REPORTING_TIMEZONE`.

The worker must open TCP to the host in `WEBHOOK_BASE`. A timeout or connect error is an environment failure, not a filter failure.

Automated tests POST only to `httptest` servers. They must not use live webhook URLs, LAN addresses, or secrets.

### Pipeline (Temporal workflows)

Two workflows run on `main-task-queue`:

#### ScrapeWorkflow

`ScrapeWorkflow` collects and stores jobs. It does not apply content filters or send notifications.

1. **Scraper** (`scrape_jobs`): Scrapes each enabled provider that is due. A forced run ignores provider cadence. The scraper collects high-level fields such as company, title, location, URL, remote status, and any description that the provider supplies.
2. **URL Duplicate Remover** (part of `scrape_jobs`): Stores each job URL once. Repeated search results and repeated scrape rounds do not create another row.

#### NotifyWorkflow

`NotifyWorkflow` evaluates stored `pending` jobs and sends one batched notification.

1. **Filter Data Loader** (`load_jobs_for_filtering`): Loads pending jobs, all jobs needed for duplicate checks, and the current search settings.
2. **Duplicate Remover**: Rejects repeated title-and-company combinations. The earliest job is kept.
3. **Basic Filter**: Rejects jobs that fail title or company include/exclude rules.
4. **Job Detailer** (`get_job_description`): Fetches the full job description when a job passes the basic filter but has no description.
5. **Advanced Filter**: Applies description include/exclude rules and checks remote jobs for phrases that indicate an on-site requirement.
6. **Smart Filter**: Reserved for a future AI-based relevance check. It is not implemented.
7. **Filter Result Writer** (`save_job_filter_result`): Saves each decision, rejection reason, fetched description, and detail-attempt count.
8. **Notifier** (`claim_notification_batch`, `send_notification`, `finish_notification_batch`): Claims eligible jobs, sends one JSON `{ "notify": "<WEBHOOK_ID>", "message": "..." }` to `WEBHOOK_BASE`, and marks the batch as notified. It does not send a request when the claim is empty.

Manual HTTP starts unique workflow IDs. `/run` and `/notify` are not blocked when a scheduled workflow run is still active.

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
    ├── Dockerfile              # Node frontend stage, Go build stage, final image
    ├── webui/                  # Vite, React, and TypeScript operator UI
    ├── cmd/jobscout/main.go    # entrypoint: "api" | "worker"
    └── internal/
        ├── config/             # env-based config (single source of truth)
        ├── db/                 # connection, embedded migrations + seeds, queries
        ├── scraper/            # provider interface, LinkedIn (x/net/html), service
        ├── pipeline/           # temporal client, workflow, activities, worker
        └── api/                # ServeMux routes + handlers
```

## Tools for local work

You need Docker and Docker Compose to run the stack. You need Go 1.25+ only for local Go builds and tests. Node.js 20+ is optional. Use it only for `make ui-test` on the host. You need Make only if you use the `make` targets (macOS and Raspberry Pi). Windows can use `docker compose` as in [QUICKSTART.md](QUICKSTART.md).

`make up` starts Postgres, Temporal, the API, the worker, and a Vite UI container. You do not run `npm` on the host for that path. Open http://localhost:5173 for the live operator UI. The API also serves the embedded production bundle at http://localhost:8001.

To run frontend tests on the host:

```bash
make ui-test
```

## Make commands

```bash
make up           # build and start the stack, including the Vite UI
make down         # stop the stack and the Vite UI
make restart      # restart containers
make build        # build images only
make logs         # follow logs
make clean        # down + prune volumes/images
make go-build     # compile the Go binary locally
make go-test      # run Go tests
make ui-test      # run frontend tests (installs npm deps if needed)
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

## Operator UI

Open http://localhost:5173 after `make up`. That is the live operator UI. http://localhost:8001 is the API and the embedded production bundle. The dashboard shows provider and job statistics. It can start a forced scrape or a notify workflow. Each manual run shows its workflow ID and a link to the configured Temporal UI. The UI polls the workflow until it finishes. A finished workflow does not by itself mean that every provider scrape succeeded. Check each provider card and its last-scraped value.

## API endpoints

Base path: `http://localhost:8001/api/v0`

| Method | Path | Description |
|--------|------|-------------|
| POST | `/run` | Start `ScrapeWorkflow`. `?force=1` bypasses provider cadence. |
| POST | `/notify` | Start `NotifyWorkflow`. |
| POST | `/scrape` | Run a synchronous debug scrape without Temporal. Prefer `/run`. |
| GET | `/health`, `/db-test`, `/temporal-test` | Read service and dependency health. |
| GET | `/status` | Read aggregate database and Temporal status. |
| GET | `/config` | Read effective, redacted operator configuration. |
| GET | `/dashboard/stats` | Read provider aggregates and daily job counts. |
| GET | `/workflow/{id}` | Read workflow status and run ID. |
| GET | `/jobs` | List jobs with filters, stable paging, and an `as_of` anchor. |
| GET | `/jobs/{id}` | Read one job, including its description. |
| GET | `/jobs/stats` | Read job counts. `new_jobs` is the `pending` count. |
| POST | `/jobs/re-evaluate` | Move rejected jobs to `pending`. Returns the updated row count. |
| GET, PUT | `/search-settings` | Read or replace universal search settings. |
| POST | `/search-settings/reset` | Reset universal search settings to seed values. |
| GET | `/scraper-settings`, `/scraper-settings/all` | Read one provider or all providers. |
| PUT | `/scraper-settings/{job_source}` | Replace one provider configuration. |
| POST | `/scraper-settings/{job_source}/reset` | Reset one provider to seed values. |

`GET /jobs` returns an object, not a bare array:

```json
{
  "items": [{ "id": 1, "title": "Support Engineer", "state": "pending" }],
  "total": 1,
  "as_of": "2026-09-18T20:00:00Z"
}
```

It accepts `limit`, `offset`, `as_of`, `state`, `job_source`, `q`, `date_from`, and `date_to`.

## Monitoring

- Temporal UI: http://localhost:8082

## Ideas

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
