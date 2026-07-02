# Usage

> Türkçe sürüm: [usage.tr.md](usage.tr.md)

## Install

```console
$ go install github.com/YusufDrymz/monup/cmd/monup@latest
```

Or build from source:

```console
$ git clone https://github.com/YusufDrymz/monup && cd monup
$ make build   # binary lands in bin/monup
```

Requirements: Docker with the compose v2 plugin (`docker compose`). monup
talks to the Docker socket directly, so run it on the host you want to
monitor (or with `--docker-socket` pointing at one).

## The workflow

monup works like terraform: preview first, then apply.

### 1. `monup plan`

Discovers what is running and shows what would be generated. Writes nothing.

```console
$ monup plan

Discovered services:
  ✓ postgres   container shop-db (postgres:16) — via network "shop_default", matched by image
  ✓ redis      container shop-cache (redis:7) — via host.docker.internal:16379, matched by image
  ? unknown    container shop-api (shop/api:1.0) — no catalog match

Core stack: prometheus, grafana, node-exporter
...
```

Line prefixes:

- `✓` matched — an exporter and scrape job will be generated for it.
- `!` matched but unreachable — monup found the service but has no way to
  reach it (no user-defined network, no published port). The warning tells
  you what to fix; nothing broken is generated.
- `?` no catalog match — the container is ignored. `monup catalog` shows
  what is recognized.

### 2. `monup apply`

Writes everything into `./monup/` (change with `--out`):

```
monup/
├── docker-compose.yml            # prometheus + grafana + exporters
├── .env.example                  # credentials the exporters need
├── .env                          # seeded from the example on first apply
├── prometheus/
│   ├── prometheus.yml
│   └── rules/*.yml               # alert rules per service
└── grafana/
    ├── provisioning/             # datasource + dashboard provider
    └── dashboards/*.json         # one dashboard per service
```

Re-running `apply` is idempotent: each file is reported as
`created`, `updated` or `unchanged`. The files are plain Prometheus/Grafana
configuration — edit them, commit them to git, or use them as a starting
point and drop monup entirely.

### 3. Credentials

Secrets are never written into the generated files; they stay as `${VAR}`
references resolved by docker compose from `.env`. After the first apply,
fill in the blanks:

```
MONUP_PG_USER=postgres
MONUP_PG_PASSWORD=...
```

If an exporter starts before its credentials are set it will crash-loop;
fix `.env` and `docker compose up -d` again.

### 4. Start

```console
$ monup apply --start        # or: cd monup && docker compose up -d
```

Grafana: `http://localhost:3000` (admin/admin unless you set
`MONUP_GRAFANA_ADMIN_USER` / `MONUP_GRAFANA_ADMIN_PASSWORD` in `.env`).
Dashboards are in the **monup** folder. Prometheus: `http://localhost:9090`
(targets under Status → Targets, alert rules under Alerts).

## How exporters reach your services

For each matched container monup picks the first strategy that works:

1. **User-defined network** — the container is on a compose/user network:
   the exporter joins that network (declared `external` in the generated
   compose file) and connects by container DNS name. Cleanest path.
2. **Published port** — the container only publishes a host port: the
   exporter connects via `host.docker.internal` (`host-gateway` is added
   for Linux).
3. **Neither** — the service is reported with `!` and skipped.

Host-level (non-container) listeners found by the Linux port scan always
use strategy 2.

## Supported services

`monup catalog` prints the built-in catalog. Currently: postgres, redis,
mysql/mariadb, nginx, mongodb, rabbitmq, plus host metrics via
node-exporter (always included).

Service notes:

- **nginx**: the exporter reads `stub_status`; enable it in your nginx
  config (`location /stub_status { stub_status; }`).
- **rabbitmq**: scraped directly through the built-in prometheus plugin
  (port 15692); enable with `rabbitmq-plugins enable rabbitmq_prometheus`.
- **mysql**: the exporter connects with `MONUP_MYSQL_USER` /
  `MONUP_MYSQL_PASSWORD`; that user needs `PROCESS, REPLICATION CLIENT,
  SELECT` grants.

## Flags

```
monup plan|apply
  --docker-socket path   docker socket (default: auto-detect, incl. $DOCKER_HOST)
  --only a,b             only these catalog entries
  --exclude a,b          skip these catalog entries
  --no-host-scan         skip host TCP listener scan (linux only)

monup apply
  --out dir              output directory (default "monup")
  --start                run `docker compose up -d` after writing
```

`NO_COLOR=1` disables colored output.

## AI-assisted discovery (`--ai`)

`monup plan --ai` (or `apply --ai`) upgrades the containers the catalog
doesn't recognize:

1. **Custom `/metrics` endpoints** — if an unknown container serves
   Prometheus metrics on a published port, monup reads the metric names
   and has an LLM generate a tailored Grafana dashboard and alert rules
   for it. The output is validated before use: it must be well-formed and
   may only reference metrics that actually exist — anything else is
   rejected (one retry with the validation error, then give up).
2. **Classification** — containers without metrics are checked against
   the known service types (a custom-built postgres image the
   fingerprints miss, for example). Only high-confidence answers are
   accepted; only container metadata is sent, never env values or
   command lines.

AI never degrades the plan — it only adds to it, and everything still
works without any API key. Configure a provider via environment:

```
ANTHROPIC_API_KEY=...          # Anthropic (default model claude-opus-4-8)
OPENAI_API_KEY=...             # OpenAI   (default model gpt-4o-mini)
OLLAMA_HOST=localhost:11434    # Ollama   (default model llama3.1, fully local)

MONUP_AI_PROVIDER=anthropic|openai|ollama   # explicit choice
MONUP_AI_MODEL=<model-id>                   # model override
```

## Troubleshooting

- **"no docker socket found"** — Docker isn't running, or the socket is
  somewhere unusual: pass `--docker-socket /path/to/docker.sock`.
- **A service shows `!` (unreachable)** — attach the container to a
  user-defined network or publish its port, then re-run `plan`.
- **Postgres/MySQL target is `down` in Prometheus** — almost always
  credentials: check `.env`, then `docker compose up -d` in the output dir.
- **Ports 9090/3000 already in use** — edit the generated
  `docker-compose.yml`; your change survives (`apply` will report the file
  as `updated` only when monup's own output changes — review the diff).
