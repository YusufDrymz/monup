# Kullanım

> English version: [usage.md](usage.md)

## Kurulum

```console
$ go install github.com/YusufDrymz/monup/cmd/monup@latest
```

Ya da kaynaktan:

```console
$ git clone https://github.com/YusufDrymz/monup && cd monup
$ make build   # binary bin/monup altına düşer
```

Gereksinim: compose v2 plugin'li Docker (`docker compose`). monup Docker
socket'iyle doğrudan konuşur; izlemek istediğin host'ta çalıştır (ya da
`--docker-socket` ile bir socket göster).

## Akış

monup terraform gibi çalışır: önce önizleme, sonra uygulama.

### 1. `monup plan`

Ne çalıştığını keşfeder, ne üretileceğini gösterir. Hiçbir şey yazmaz.

```console
$ monup plan

Discovered services:
  ✓ postgres   container shop-db (postgres:16) — via network "shop_default", matched by image
  ✓ redis      container shop-cache (redis:7) — via host.docker.internal:16379, matched by image
  ? unknown    container shop-api (shop/api:1.0) — no catalog match

Core stack: prometheus, grafana, node-exporter
...
```

Satır önekleri:

- `✓` eşleşti — bu servis için exporter ve scrape job üretilecek.
- `!` eşleşti ama erişilemiyor — monup servisi buldu ama ulaşacak bir yol
  yok (user-defined network yok, published port yok). Uyarı neyi düzelteceğini
  söyler; bozuk config üretilmez.
- `?` katalogda karşılığı yok — container yok sayılır. Tanınan servisleri
  `monup catalog` listeler.

### 2. `monup apply`

Her şeyi `./monup/` altına yazar (`--out` ile değiştirilebilir):

```
monup/
├── docker-compose.yml            # prometheus + grafana + exporter'lar
├── .env.example                  # exporter'ların ihtiyaç duyduğu bilgiler
├── .env                          # ilk apply'da example'dan kopyalanır
├── prometheus/
│   ├── prometheus.yml
│   └── rules/*.yml               # servis başına alert kuralları
└── grafana/
    ├── provisioning/             # datasource + dashboard provider
    └── dashboards/*.json         # servis başına bir dashboard
```

`apply` idempotent'tir: her dosya `created`, `updated` ya da `unchanged`
olarak raporlanır. Üretilenler düz Prometheus/Grafana konfigürasyonudur —
düzenleyebilir, git'e commit'leyebilir ya da başlangıç noktası olarak alıp
monup'ı tamamen bırakabilirsin.

### 3. Kimlik bilgileri

Secret'lar asla üretilen dosyalara yazılmaz; docker compose'un `.env`'den
çözdüğü `${VAR}` referansları olarak kalır. İlk apply'dan sonra boşları doldur:

```
MONUP_PG_USER=postgres
MONUP_PG_PASSWORD=...
```

Bir exporter kimlik bilgileri girilmeden başlarsa crash-loop'a girer;
`.env`'i düzeltip tekrar `docker compose up -d` çalıştırman yeterli.

### 4. Başlatma

```console
$ monup apply --start        # ya da: cd monup && docker compose up -d
```

Grafana: `http://localhost:3000` (`.env`'de `MONUP_GRAFANA_ADMIN_USER` /
`MONUP_GRAFANA_ADMIN_PASSWORD` ayarlamadıysan admin/admin). Dashboard'lar
**monup** klasöründe. Prometheus: `http://localhost:9090` (target'lar
Status → Targets altında, alert kuralları Alerts altında).

## Exporter'lar servislerine nasıl ulaşır

Eşleşen her container için monup çalışan ilk stratejiyi seçer:

1. **User-defined network** — container bir compose/user network'teyse:
   exporter o network'e katılır (üretilen compose'da `external` olarak
   tanımlanır) ve container DNS adıyla bağlanır. En temiz yol.
2. **Published port** — container yalnızca host'a port açmışsa: exporter
   `host.docker.internal` üzerinden bağlanır (Linux için `host-gateway`
   eklenir).
3. **İkisi de yoksa** — servis `!` ile raporlanır ve atlanır.

Linux port taramasının bulduğu host servisleri her zaman 2. stratejiyi kullanır.

## Desteklenen servisler

`monup catalog` builtin kataloğu basar. Şu an: postgres, redis,
mysql/mariadb, nginx, mongodb, rabbitmq; ayrıca node-exporter ile host
metrikleri (her zaman dahil).

Servis notları:

- **nginx**: exporter `stub_status` okur; nginx config'inde etkinleştir
  (`location /stub_status { stub_status; }`).
- **rabbitmq**: built-in prometheus plugin'i üzerinden doğrudan scrape
  edilir (port 15692); `rabbitmq-plugins enable rabbitmq_prometheus` ile aç.
- **mysql**: exporter `MONUP_MYSQL_USER` / `MONUP_MYSQL_PASSWORD` ile
  bağlanır; kullanıcıda `PROCESS, REPLICATION CLIENT, SELECT` yetkileri olmalı.

## Bayraklar

```
monup plan|apply
  --docker-socket path   docker socket (varsayılan: otomatik, $DOCKER_HOST dahil)
  --only a,b             sadece bu katalog girdileri
  --exclude a,b          bu katalog girdilerini atla
  --no-host-scan         host TCP listener taramasını atla (yalnız linux)

monup apply
  --out dir              çıktı dizini (varsayılan "monup")
  --start                dosyaları yazdıktan sonra `docker compose up -d`
```

Renkli çıktıyı kapatmak için `NO_COLOR=1`.

## Sorun giderme

- **"no docker socket found"** — Docker çalışmıyor ya da socket alışılmadık
  bir yerde: `--docker-socket /path/to/docker.sock` ver.
- **Bir servis `!` (unreachable) görünüyor** — container'ı bir user-defined
  network'e bağla ya da portunu publish et, sonra `plan`'ı tekrar çalıştır.
- **Prometheus'ta postgres/mysql target'ı `down`** — neredeyse her zaman
  kimlik bilgisi: `.env`'i kontrol et, çıktı dizininde `docker compose up -d`.
- **9090/3000 portları dolu** — üretilen `docker-compose.yml`'ı düzenle;
  değişikliğin korunur (`apply` yalnızca monup'ın kendi çıktısı değişince
  dosyayı `updated` olarak raporlar — diff'i gözden geçir).
