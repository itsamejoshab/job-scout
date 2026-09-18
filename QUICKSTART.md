# Quick start

This guide starts Job Scout on your computer. You do not need to read the Go code.

GitHub does not show tabs. Open **one** section below: Windows, macOS, or Linux (Raspberry Pi). Follow only that section.

When the stack is up:

- Live operator UI: http://localhost:5173
- API and embedded UI: http://localhost:8001
- Temporal UI: http://localhost:8082

---

<details>
<summary><strong>Windows</strong></summary>

### 1. Install the tools

1. Install [Git for Windows](https://git-scm.com/download/win). Accept the default options.
2. Install [Docker Desktop for Windows](https://docs.docker.com/desktop/setup/install/windows-install/). Start Docker Desktop and wait until it is running.

You do not need Make. The commands below use Docker Compose.

### 2. Open PowerShell

Press the Windows key. Type `PowerShell`. Open **Windows PowerShell**.

Check the tools:

```powershell
git --version
docker version
```

If `docker version` fails, start Docker Desktop and wait. Then run the command again.

### 3. Get the code

```powershell
git clone https://github.com/itsamejoshab/job-scout.git
cd job-scout
```

### 4. Create your env file

```powershell
Copy-Item .\job-scout\.env.sample .\job-scout\.env
```

Open `job-scout\.env` in a text editor. Set at least:

- `WEBHOOK_BASE` — base URL of your webhook (no trailing slash if the sample has none)
- `WEBHOOK_ID` — id that your webhook expects

Set `PIPEDREAM_API_TOKEN` if the webhook uses a static Bearer token. Do not set `WEBHOOK_URL`. That name is not the send target.

The worker must open TCP to the host in `WEBHOOK_BASE`. A timeout or connect error is an environment failure.

### 5. Start the stack

```powershell
docker compose --env-file .\job-scout\.env up -d --build
```

This starts Postgres, Temporal, the API, and the worker. The first run can take several minutes.

### 6. Check that it is up

```powershell
curl.exe http://localhost:8001/api/v0/health
```

You must get a success response. Then open http://localhost:5173 for the live operator UI. Open http://localhost:8001 for the API. Open http://localhost:8082 for the Temporal UI.

</details>

<details>
<summary><strong>macOS</strong></summary>

### 1. Install the tools

1. Open **Terminal** (press Command + Space, type `Terminal`, press Return).
2. Install Apple developer tools (this includes `git` and `make`):

```bash
xcode-select --install
```

3. Install [Docker Desktop for Mac](https://docs.docker.com/desktop/setup/install/mac-install/). Start Docker Desktop and wait until it is running.

### 2. Check the tools

In Terminal:

```bash
git --version
docker version
make --version
```

If `docker version` fails, start Docker Desktop and wait. Then run the command again.

### 3. Get the code

```bash
git clone https://github.com/itsamejoshab/job-scout.git
cd job-scout
```

### 4. Create your env file

```bash
cp job-scout/.env.sample job-scout/.env
```

Open `job-scout/.env` in a text editor. Set at least:

- `WEBHOOK_BASE` — base URL of your webhook (no trailing slash if the sample has none)
- `WEBHOOK_ID` — id that your webhook expects

Set `PIPEDREAM_API_TOKEN` if the webhook uses a static Bearer token. Do not set `WEBHOOK_URL`. That name is not the send target.

The worker must open TCP to the host in `WEBHOOK_BASE`. A timeout or connect error is an environment failure.

Optional: change search settings in `job-scout/internal/db/seed/` before the first start. The API writes those values to the database on startup.

### 5. Start the stack

```bash
make up
```

If you do not have Make:

```bash
docker compose --env-file ./job-scout/.env up -d --build
```

This starts Postgres, Temporal, the API, and the worker. The first run can take several minutes.

### 6. Check that it is up

```bash
curl http://localhost:8001/api/v0/health
```

You must get a success response. Then open http://localhost:5173 for the live operator UI. Open http://localhost:8001 for the API. Open http://localhost:8082 for the Temporal UI.

</details>

<details>
<summary><strong>Linux (Raspberry Pi)</strong></summary>

This stack can run on a Raspberry Pi. You need **64-bit ARM** (`aarch64`). The Temporal and Postgres images do not run on 32-bit Raspberry Pi OS.

**Hardware**

- Raspberry Pi **4** (4 GB RAM or more) or Raspberry Pi **5**. 8 GB is better. Temporal plus Postgres uses a lot of RAM.
- Do not use a Pi 3, Pi Zero, or 32-bit OS for this project.
- Use a 32 GB (or larger) microSD card, or a USB SSD.

**Operating system**

Use **Raspberry Pi OS (64-bit)**. It is Debian. It is the official image for the Pi.

1. On a Windows or Mac computer, install [Raspberry Pi Imager](https://www.raspberrypi.com/software/).
2. Choose **Raspberry Pi OS (64-bit)**. Do not choose the 32-bit image.
3. In Imager, open OS customisation. Set the hostname, Wi-Fi, and a user. Enable SSH.
4. Write the image to the card. Put the card in the Pi. Power on.

Then SSH from your computer (replace `pi` and `jobscout` if you set other names):

```bash
ssh pi@jobscout.local
```

If `.local` fails, use the Pi IP address from your router.

Confirm 64-bit:

```bash
uname -m
```

The output must be `aarch64`. If you see `armv7l`, flash 64-bit Raspberry Pi OS and start again.

### 1. Install the tools

On the Pi:

```bash
sudo apt update
sudo apt install -y git make curl
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker "$USER"
```

Log out of SSH. Log in again so the `docker` group applies.

### 2. Check the tools

```bash
git --version
docker version
docker compose version
uname -m
```

If `docker version` fails with a permission error, log out and log in after the group change.

### 3. Get the code

```bash
git clone https://github.com/itsamejoshab/job-scout.git
cd job-scout
```

### 4. Create your env file

```bash
cp job-scout/.env.sample job-scout/.env
nano job-scout/.env
```

Set at least:

- `WEBHOOK_BASE` — base URL of your webhook (no trailing slash if the sample has none)
- `WEBHOOK_ID` — id that your webhook expects

Set `PIPEDREAM_API_TOKEN` if the webhook uses a static Bearer token. Do not set `WEBHOOK_URL`. That name is not the send target.

The worker must open TCP to the host in `WEBHOOK_BASE`. A timeout or connect error is an environment failure. If Home Assistant runs on another device on your LAN, use that device IP in `WEBHOOK_BASE`. `localhost` on the Pi is the Pi itself, not your laptop.

Optional: change search settings in `job-scout/internal/db/seed/` before the first start. The API writes those values to the database on startup.

### 5. Start the stack

```bash
make up
```

If you do not have Make:

```bash
docker compose --env-file ./job-scout/.env up -d --build
```

This starts Postgres, Temporal, the API, the worker, and the Vite UI. The first run **builds Go on the Pi**. That can take 15 minutes or more.

### 6. Check that it is up

On the Pi:

```bash
curl http://localhost:8001/api/v0/health
hostname -I
```

You must get a success response from `curl`. Then, on a computer on the same network, open:

- Live operator UI: `http://PI_IP:5173`
- API and embedded UI: `http://PI_IP:8001`
- Temporal UI: `http://PI_IP:8082`

Replace `PI_IP` with the first address from `hostname -I`.

</details>

---

## Run a scrape and a notify (optional)

Do this after the health check. This is a manual check. Automated tests must not POST to a live webhook.

Open the live operator UI at http://localhost:5173. On a Raspberry Pi, use `http://PI_IP:5173` or the embedded UI at `http://PI_IP:8001`. Use the dashboard buttons to start a forced scrape or a notify pass. The page shows the workflow ID and a direct Temporal UI link. It polls until the workflow finishes, then refreshes its statistics and jobs. A finished scrape workflow does not prove that every provider scrape succeeded. Check the provider card and its last-scraped value.

You can also use the API:

**Windows (PowerShell)**

```powershell
curl.exe -X POST "http://localhost:8001/api/v0/run?force=1"
```

Wait until jobs exist:

```powershell
curl.exe "http://localhost:8001/api/v0/jobs"
```

Then send a notify:

```powershell
curl.exe -X POST "http://localhost:8001/api/v0/notify"
```

**macOS and Raspberry Pi**

```bash
curl -X POST "http://localhost:8001/api/v0/run?force=1"
```

Wait until jobs exist:

```bash
curl "http://localhost:8001/api/v0/jobs"
```

Then send a notify:

```bash
curl -X POST "http://localhost:8001/api/v0/notify"
```

If you have Make (macOS and Raspberry Pi):

```bash
make run
make notify
```

`make run` does not set `force=1`. LinkedIn still waits for its cadence unless you POST `/run?force=1`.

Confirm the downstream notify automation after the POST.

Scheduled scrape (default 60 seconds) and notify (default 300 seconds) also run after the API starts.

## Operator API

The API base is `http://localhost:8001/api/v0`. On a Raspberry Pi, replace `localhost` with `PI_IP`.

Read paths:

- `GET /status` reads database and Temporal status.
- `GET /config` reads the effective, redacted configuration.
- `GET /dashboard/stats` reads provider totals, states, rejection reasons, and daily counts.
- `GET /jobs` lists filtered jobs. `GET /jobs/{id}` reads one job with its description.
- `GET /jobs/stats` reads state totals.
- `GET /workflow/{id}` reads a workflow status and run ID.
- `GET /search-settings`, `GET /scraper-settings`, and `GET /scraper-settings/all` read settings.

Write and reset paths:

- `POST /run?force=1` starts a forced scrape. `POST /notify` starts a notify pass.
- `PUT /search-settings` replaces all filter lists. `POST /search-settings/reset` restores seed values.
- `PUT /scraper-settings/{job_source}` replaces one provider configuration.
- `POST /scraper-settings/{job_source}/reset` restores one provider from its seed.
- `POST /jobs/re-evaluate` moves rejected jobs to `pending` after filter changes.

`GET /jobs` returns an object with `items`, `total`, and `as_of`. It does not return a bare job array:

```json
{
  "items": [{ "id": 1, "title": "Support Engineer", "state": "pending" }],
  "total": 1,
  "as_of": "2026-09-18T20:00:00Z"
}
```

The list accepts `limit`, `offset`, `as_of`, `state`, `job_source`, `q`, `date_from`, and `date_to`.

## Frontend development (optional)

`make up` starts a Vite UI container. You do not `cd` into `job-scout/webui` and you do not run `npm` on the host for daily work. Open http://localhost:5173. The API still serves the embedded production bundle at http://localhost:8001.

Install Node.js 20+ on the host only if you want frontend tests:

```bash
make ui-test
```

## Stop the stack

From the `job-scout` repo directory:

```bash
docker compose down
```

With Make:

```bash
make down
```

## Next

See [README.md](README.md) for architecture, API paths, and Make targets.
