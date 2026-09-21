---
name: apify-actors
description: Build, deploy, monetize, and publish Apify Actors (Python or JS) using the Apify CLI, PPE pricing, Standby APIs, and Store README. Always set seoTitle and seoDescription and test Console-shaped input. Use when creating a new Actor repo, running apify push, setting pay-per-event prices, Standby FastAPI, publishing to Apify Store, or editing seoTitle/seoDescription.
---

# Apify Actors

Personal workflow for new Actors. **Read the repo `AGENTS.md` first** when present. Use Apify MCP tools (`search-apify-docs`, `fetch-apify-docs`, `search-actors`) for current platform docs.

Do **not** `apify push`, change live pricing, or set `isPublic` unless the user asked.

## Bootstrap

```bash
apify login -m console    # if apify info fails
apify create [name]       # or apify init in an existing dir
```

- Fill `.actor/actor.json` `meta.generatedBy` with the current tool and model.
- Keep `usesStandbyMode: true` unless the user explicitly wants it off.
- Always implement the Standby readiness probe (`x-apify-container-server-readiness-probe` → 200 `ok`).
- Log with `Actor.log` only (censors tokens).
- Handle `Event.ABORTING` and exit quickly.
- Set `minMemoryMbytes` / `maxMemoryMbytes` tight (short CPU jobs: 256–512).
- **Before calling the Actor done:** write `seoTitle` / `seoDescription`, and pytest (or equivalent) that replays a **Console Input-tab payload**, not only a clean API body.

## Store discovery — seoTitle and seoDescription

Apify uses these heavily so people can **find** the Actor. Treat them as required launch work, not a Publishing-tab afterthought. Set them on **Publishing → Display information**, keep copies in `.actor/actor.json`, and PUT if Console and git have drifted:

```bash
apify api PUT /v2/actors/{username}~{name} -d '{"seoTitle":"…","seoDescription":"…"}'
```

| Field | Who sees it | Length | Job |
| --- | --- | --- | --- |
| `title` | Store card, Console | 40–50 chars | Warm visitor already on Apify. Speak to the outcome. |
| `description` | Store page, Console, **Store search** | ~300 chars | Same audience; more room for features. |
| `seoTitle` | Google title / meta | 40–50 chars | Cold searcher. Keywords + use case. |
| `seoDescription` | Google snippet | **145–155 chars** | Convince them to click vs other results. |

**Store search** matches `title`, `name`, `description`, `username`, and README — not the SEO fields. Google uses `seoTitle` / `seoDescription` (Google may still pull README text for some queries). Write both layers.

Do **not** leave SEO blank so Apify copies `title`/`description`. Those strings are usually the wrong length and the wrong voice (warm vs cold). Put the words people type (`irrigation calculator`, `Instagram scraper`, …) in `seoTitle`/`seoDescription` and in README headings.

Checklist when creating or renaming an Actor:

1. Draft `seoTitle` (40–50) and `seoDescription` (145–155) **before** polish on the long README.
2. Put them in `.actor/actor.json` and PUT the live Actor so Store/Google do not wait on a rebuild.
3. Verify Publishing → Display information shows the SEO pair, not duplicates of `title`/`description`.

## Testing — Console input is the real input

The Input tab is how most Store users run the Actor. `Actor.get_input()` is **not** the same shape as a hand-written Standby POST. A passing HTTP test suite can still explode on the first Console Start.

Console gotchas to design for and **test with fixtures**:

- XOR fields (`this` or `that`) — users fill **both**. Prefer the more specific field, record an assumption, do not 400.
- Empty number widgets often arrive as `0` or `""`. Treat unused `0`/`""` as omitted for optional XOR fields.
- Schema `default` values are **always present** in GUI JSON even if the user never touched them.
- Optional fields the user skipped may be missing (no pressure, no date, no plant).
- `extra` keys sometimes appear; do not crash the one-shot path on a Console-only field if you can ignore it.
- One-shot `main()` and Standby `/calculate` must both accept the same business payload.

Required tests in the plan (do not ship without them):

1. Domain/unit tests for the actual work.
2. HTTP tests: success, 400s, readiness probe (`x-apify-container-server-readiness-probe`).
3. **A frozen Console payload** — copy JSON from a real Input-tab run (or construct every field the schema shows) and `calculate()` / POST it.
4. XOR cases: both filled, neither filled, unused side `0`.
5. Prefill-only Start: schema `prefill`/`default` must produce a valid run. Never prefill **both** sides of an XOR.
6. One-shot path: invalid input → `Actor.exit_code` + `status_message`, not a raw Pydantic traceback and exit 91.

Run `pytest` (or the JS equivalent) **before** `apify push`. If the GUI failed once, add that exact JSON as a regression test.

**pytest is not a Console run.** It never calls `Actor.get_input()` or one-shot `main()`. After a push, verify the **live** Actor without the GUI:

```bash
# Local one-shot path (Actor SDK + INPUT), same JSON the Input tab sends:
apify run --purge --input-file tests/fixtures/console_gui_input.json

# Cloud run on tagged `latest` (this is what a new Console Start uses):
apify call --build latest --input-file tests/fixtures/console_gui_input.json --output-dataset
```

Do **not** resurrect a failed run to check a fix. Resurrect pulls the **original build** (the log line `Pulling container image of build …`). Use **Start** (new run) or `apify call --build latest`. Confirm the log shows the new build number, not the one that failed.

Cloud `Actor.push_data()` also AJV-validates `.actor/dataset_schema.json`. Local `storage/` and pytest do **not**. Optional strings that can be JSON `null` (`plant_key`, …) must be `"type": ["string", "null"]`. After every schema or output-shape change, `apify call --build latest` is the check that matches Console Start.

## Python Standby HTTP

FastAPI + uvicorn on `Actor.configuration.web_server_port`, host `0.0.0.0`. Charge **after** a successful response:

```python
await Actor.charge(event_name='your-event')
```

Standby URL: `https://{username}--{actor-name}.apify.actor`

If the Apify username changes, update README examples. Re-login after a username change (`apify login -m console`).

## Store README

The root `README.md` **is** the Store page. No H1 (Actor title is the H1). Sell the outcome; keep FAO/method details when they help trust. Omit local `apify run` / `pip install` unless the user wants a contributor guide.

## PPE pricing (easy to get wrong)

`eventPriceUsd` is **per event**. The Store card shows **per 1,000** = `eventPriceUsd * 1000`. There is **no** per-call listing toggle.

| You want on the card | Set `eventPriceUsd` |
| --- | ---: |
| $5 / 1,000 | `0.005` |
| $0.25 / 1,000 | `0.00025` |
| $0.25 per call | `0.25` → card shows **$250 / 1,000** |

Author share is **80%** of paid-plan event revenue. Standby + **PPE only** (not “PPE + usage”): Apify covers **users’** platform usage. The author’s **own** test runs still use the author’s compute.

Payout billing must exist before monetize API works (`cannot-monetize-without-payout-billing-info`).

Updating price: **PUT the existing `pricingInfos` array unchanged, then append one new object.** Include `apifyMarginPercentage` (0.2) on records the API already returned. Do not replace the array with only the new price (`incorrect-pricing-modifier-prefix`).

Keep `apify-actor-start` at the default `$0.00005` unless the user wants otherwise. Do not also charge `apify-default-dataset-item` if you already charge a custom “one result” event.

## Deploy

```bash
apify push --wait-for-finish=300
# if platform says remote is newer:
apify push --force --wait-for-finish=300
```

`--force` drops a Git-repo source connection and uploads local files. Confirm before using it on an Actor that auto-builds from GitHub.

GitHub integration (webhook on the Actor version) **builds again on `git push`** and moves the `latest` tag. So `apify push` may create 0.0.15, then the PR push creates 0.0.16 from git and **that** becomes `latest`. Console **Use Actor** runs the tagged `latest` build, not “the last CLI build number.” Refresh if the UI still shows an older number; check **Builds** for the full list. Do not resurrect an old run to pick up a new build.

## Publish on Store

The button is on **Publishing**, not Source/Input:

`https://console.apify.com/actors/{actorId}/publication`

It stays hidden until every section is complete: **logo**, description, monetization, sample output, output/dataset/OpenAPI schemas, permission level. Limited permissions are preferred.

## MCP

Project MCP (repo `opencode.json`): `https://mcp.apify.com/?tools=actors%2Cdocs`. Auth via OAuth: `opencode mcp auth apify`. Do not commit API tokens.

More gotchas: [reference.md](reference.md)
