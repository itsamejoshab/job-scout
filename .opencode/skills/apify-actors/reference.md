# Apify Actors — extra gotchas

## CLI

| Command | Use |
| --- | --- |
| `apify info` | Confirm username after login / rename |
| `apify actors ls` | Own Actors |
| `apify actors info {username}/{name}` | Title, public/private, PPE table |
| `apify api GET /v2/actors/{actorId}` | Full metadata (`pricingInfos`, `standbyUrl`) |
| `apify api PUT /v2/actors/{actorId} -d '...'` | Partial update (only sent fields). `actorId` is `{username}~{name}` or the id. |

After an Apify **username** change, CLI login is invalid until `apify login -m console`. Standby hostnames change to `{newUser}--{name}.apify.actor`.

## Pricing PUT shape

```json
{
  "pricingInfos": [
    { "...existing record copied from GET..." },
    {
      "pricingModel": "PAY_PER_EVENT",
      "minimalMaxTotalChargeUsd": 0.01,
      "apifyMarginPercentage": 0.2,
      "reasonForChange": "…",
      "forceContainsSignificantPriceChange": true,
      "pricingPerEvent": {
        "actorChargeEvents": {
          "your-event": {
            "eventTitle": "…",
            "eventDescription": "…",
            "eventPriceUsd": 0.005,
            "isPrimaryEvent": true
          },
          "apify-actor-start": {
            "eventTitle": "Actor Start",
            "eventDescription": "Charged when the Actor starts running.",
            "eventPriceUsd": 0.00005
          }
        }
      }
    }
  ]
}
```

Price **increases** can require a 14-day notice on public Actors. Decreases apply immediately.

## Standby vs idle cost

PPE-only Standby: you are not billed for **customers’** compute. Idle timeout still stops unused runs. Author tests (Console Start, your token on the Standby URL) bill **your** account.

## seoTitle / seoDescription

Required. Publishing → Display information, also `.actor/actor.json`, PUT `/v2/actors/{username}~{name}` so it does not wait on a build.

- `seoTitle`: 40–50 chars, keyword-first (what people Google).
- `seoDescription`: 145–155 chars, cold-click snippet. Count characters.
- Store `title`/`description` are a different voice (warm, on-platform). Store **search** uses title, name, description, username, README — not SEO fields.
- If unset, Apify duplicates the Store copy, which is usually too long or too vague for Google.

## Console vs API testing

Ship tests that replay Input-tab JSON. Console will send both sides of XOR fields, `0` for empty numbers, and schema defaults the user never touched. Prefill a complete valid example; never prefill both XOR options. Catch `ValidationError` in one-shot `main()` and set `Actor.status_message` instead of a traceback / exit 91.

pytest / HTTP tests do **not** replace a platform run. After `apify push`, prove the fix with `apify call --build latest --input-file <console.json> --output-dataset`. **Resurrect reuses the old build**; it will keep failing even after a good push. New Console Start and `apify call` use `latest`. Cloud `push_data` AJV-validates `dataset_schema.json` (nullable fields need `"type": ["string", "null"]`); local `storage/` does not.

## Quality

- Validate input early; fail with clear 400s (HTTP) and Console status messages (one-shot).
- Charge only after the user can see the result.
- Store README ≥ ~300 words, H2 sections, JSON output example, pricing section that matches live PPE (mention both per-event and per-1,000 if they differ).
