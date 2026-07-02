# monup

`terraform plan` for monitoring: point it at a Docker host, it figures out
what's running and generates a Prometheus + Grafana stack tailored to it.

```console
$ monup plan          # discover services, preview what will be generated
$ monup apply --start # write the files and bring the stack up
```

monup discovers your containers (PostgreSQL, Redis, nginx, MySQL, MongoDB,
RabbitMQ...), then generates everything monitoring them needs: the right
exporters, `prometheus.yml`, alert rules and Grafana dashboards. All output
is plain config files you can read, edit and commit. No lock-in: it sets up
the standard Prometheus + Grafana stack, so if you delete monup everything
keeps working.

## Install

```console
$ go install github.com/YusufDrymz/monup/cmd/monup@latest
```

## Quick start

On a host running some containers:

```console
$ monup plan

Discovered services:
  ✓ postgres   container shop-db (postgres:16) — via network "shop_default", matched by image
  ✓ redis      container shop-cache (redis:7) — via host.docker.internal:16379, matched by image
  ? unknown    container shop-api (shop/api:1.0) — no catalog match

Core stack: prometheus, grafana, node-exporter

Files to generate (13):
  + docker-compose.yml
  + prometheus/prometheus.yml
  + prometheus/rules/postgres.yml
  ...

$ monup apply --start
```

Then open Grafana at `http://localhost:3000` (admin/admin by default) —
dashboards and alerts for your actual services are already provisioned.

Credentials (e.g. the postgres exporter's DSN) are never written into the
generated files; they stay as `${VAR}` references. `apply` seeds a `.env`
from `.env.example`, fill it in and restart the affected exporter.

## How it works

- **Discovery**: containers via the Docker Engine API (unix socket, no
  Docker SDK), plus host-level TCP listeners on Linux.
- **Matching**: a built-in catalog of service fingerprints (image names,
  well-known ports). `monup catalog` lists it.
- **Reachability**: if the target is on a user-defined network, the
  exporter joins that network and uses container DNS. Otherwise it falls
  back to `host.docker.internal` and a published port. If neither works,
  monup tells you instead of generating a config that can't connect.
- **Output**: everything lands in `./monup/` as plain files. Re-running
  `apply` is idempotent and reports created/updated/unchanged per file.

## Docs

Full usage guide (all flags, reachability strategies, per-service notes,
troubleshooting): [docs/usage.md](docs/usage.md) · Türkçe:
[docs/usage.tr.md](docs/usage.tr.md)

## License

MIT

<details>
<summary>Türkçe</summary>

monup, Docker host'unda ne çalıştığını keşfedip (PostgreSQL, Redis, nginx...)
ona özel Prometheus config'i, exporter'lar, alert kuralları ve Grafana
dashboard'ları üreten tek binary bir araçtır. Üretilen her şey okunabilir düz
dosyadır; `monup plan` yazmadan önce ne üretileceğini gösterir. Standart
Prometheus + Grafana stack'ini kurduğu için bağımlılık yaratmaz: monup'ı
silseniz de stack çalışmaya devam eder.

Hızlı başlangıç: `monup plan` ile önizleyin, `monup apply --start` ile
başlatın, `http://localhost:3000` üzerinden Grafana'ya girin.

</details>
