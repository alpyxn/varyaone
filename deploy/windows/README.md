# Varya One — Windows masaüstü paketi

Windows makinesinde bir kişi sunucuyu kurar; ağdaki herkes tarayıcıdan veya ince
istemci uygulamasından erişir. Docker gerekmez.

## Bileşenler

| Bileşen | Konum | Durum |
|---|---|---|
| `varyaone.exe` — API + worker + gömülü SPA + supervisor | `cmd/varyaone` | ✅ hazır |
| Windows Service entegrasyonu (`varyaone service …`, `varyaone stack`) | `internal/platform/desktop` | ✅ hazır |
| Gömülü PostgreSQL yönetimi (`initdb`/`pg_ctl`) | `internal/platform/desktop/postgres.go` | ✅ hazır (pgsql/ paketlenmeli) |
| mDNS yayını (`_varyaone._tcp`) | `internal/platform/desktop/mdns.go` | ✅ hazır |
| Windows updater (`varyaone update-apply`) | `internal/platform/desktop/updater.go` | ✅ hazır (uçtan uca test bekliyor) |
| Updater tetikleyici (Linux systemd ajanı yerine) | `supervisor.go` `runUpdateApplier` + `spawn_windows.go` | ✅ hazır (uçtan uca test bekliyor) |
| Pulse yapılandırması (masaüstü varsayılanı) | `settings.go` + `supervisor.go` `config()` | ✅ resmi uç varsayılan, `settings.env` ile geçersiz kılınır |
| VC++ çalışma zamanı (gömülü PG için şart) | `fetch-tools.ps1` indirir, `installer.iss` `[Code]` sessiz kurar | ✅ yoksa `initdb` `0xC0000135` verir |
| WebView2 runtime (istemci için şart) | `fetch-tools.ps1` tam x64 Evergreen standalone paketi indirir, installer çevrimdışı kurar | ✅ internet gerektirmez |
| WebP codec | saf Go codec; Windows binary `CGO_ENABLED=0` kalabilir | ✅ başlangıçta native DLL gerektirmez |
| Firewall: 8080/TCP + 5353/UDP (mDNS) | `netmode.go` `reconcileFirewall` (private+domain profili) | ✅ servis LocalSystem olduğu için prompt çıkmaz |
| Port çakışması ön kontrolü | `supervisor.go` `checkHTTPPortFree` | ✅ "8080 kullanımda" net mesajı |
| Kod imzalama (SmartScreen) | `installer.iss` SignTool iskeleti | ⏳ sertifika gerek (bkz. aşağı) |
| SPA static build (`VARYAONE_ADAPTER=static`) | `web/svelte.config.js` | ✅ hazır |
| pulse: Windows artefakt alanları | `varya-pulse` migration 0004 + `/release/v1/latest` | ✅ hazır |
| Ağ modu (`varyaone netmode <local\|lan>`) | `internal/platform/desktop/netmode.go` | ✅ hazır |
| İstemci + kontrol paneli (WebView2, `--panel`) | `cmd/varyaone-client` | ✅ hazır (uçtan uca test bekliyor) |
| Inno Setup installer (sihirbaz + servis kaydı + kaldırıcı) | `deploy/windows/installer.iss` | ✅ hazır |
| CI (`windows-2025`) | `.github/workflows/desktop-windows.yml` | ✅ zip + `setup.exe` üretir; paketli stack'i ve gerçek setup/SCM yaşam döngüsünü test eder |

## Çalışma zamanı mimarisi

```
Windows Service "VaryaOne"  ->  varyaone.exe stack
   ├── bundled PostgreSQL           %ProgramData%\VaryaOne\pgdata   (127.0.0.1:5433)
   ├── migrate up
   ├── API + worker (goroutine)     local: 127.0.0.1:8080 / LAN: 0.0.0.0:8080
   ├── embedded SPA                 aynı port, /api ve /health dışındaki her GET
   └── mDNS _varyaone._tcp          LAN keşfi
```

Tek Go binary + PostgreSQL. Node runtime yok: SvelteKit sunucu katmanı
(`/api/v1/[...path]`, `/api/health/[kind]` proxy'leri) yerine tarayıcı doğrudan
Go sunucusundaki `/api/v1/*` ve `/health/*` uçlarına aynı origin üzerinden gider.

## Yerel deneme (Linux/Windows, sistemde PostgreSQL varken)

```bash
cd web && VARYAONE_ADAPTER=static npm run build && cd ..
rm -rf internal/platform/spa/dist && cp -r web/build internal/platform/spa/dist
go build -buildvcs=false -o /tmp/varyaone ./cmd/varyaone

export VARYAONE_DESKTOP_HOME=/tmp/varyaone-home
export VARYAONE_DESKTOP_PG_PORT=5433   # sistemde pg_ctl PATH'te olmalı
/tmp/varyaone stack
# tarayıcı: http://localhost:8080
```

## Paket üretimi

```bash
# 1. PostgreSQL Windows binary'lerini indir (Windows'ta, bir kez):
pwsh deploy/windows/fetch-tools.ps1

# 2. Bundle + zip + sha256 + setup.exe (iscc PATH'te ise sihirbazı da derler):
#    Inno Setup: `choco install innosetup` veya https://jrsoftware.org/isdl.php
deploy/windows/build-bundle.sh v0.3.0
#    -> dist/windows/VaryaOne-Setup-0.3.0.exe   (çift tıkla kurulum sihirbazı)
#    -> dist/windows/varyaone-v0.3.0-windows-amd64.zip  (updater artefaktı)

# 3. Release'i pulse'a kaydet (updater bunu görür):
curl -X POST https://varya-pulse.varyaone.workers.dev/admin/v1/releases \
  -H "authorization: Bearer $PULSE_ADMIN_TOKEN" \
  -d '{"version":"v0.3.0","channel":"stable",
       "win_artifact_url":"https://github.com/alpyxn/varyaone/releases/download/v0.3.0/varyaone-v0.3.0-windows-amd64.zip",
       "win_artifact_sha256":"<zip.sha256 içeriği>"}'
```

`pgsql/` yoksa betik eksik bir zip üretmez, hata ile durur. CI,
`VARYAONE_REQUIRE_INSTALLER=1` kullandığı için Inno Setup veya installer
önkoşulları eksikken de release işi başarılı sayılmaz. PostgreSQL + VC++
dosyaları sürüm ve indirme betiği hash'ine göre cache'lenir.

## Kurulum sihirbazı (`VaryaOne-Setup-<v>.exe`)

`deploy/windows/installer.iss` (Inno Setup 6 — `x64compatible`, `choco install
innosetup`). Kullanıcı çift tıklar:

- Program `C:\Program Files\VaryaOne` altına kurulur. Masaüstüne **iki kısayol**
  düşer: **Varya One** (`appicon.ico`) ve **Varya Kontrol Paneli**
  (`panelicon.ico`, `varyaone-client.exe --panel`).
- Kurulum: `netmode <lan|local>` → `service repair` → `service wait-ready`.
  `VaryaOne` servisi (gecikmeli otomatik başlatma) `varyaone stack`'i sürer.
- Sihirbazdaki **"Ağdaki diğer bilgisayarlar erişebilsin"** görevi (varsayılan
  işaretli) ağ modunu belirler → `netmode`:
  - `lan`: sunucu `0.0.0.0:8080`, 8080 güvenlik duvarı kuralı, mDNS yayını.
  - `local`: sunucu `127.0.0.1:8080`, kural yok, yayın yok.
- Kurulum sonu `varyaone-client.exe` açılır.
- **Kaldırma**: servisi durdurur + kaldırır, firewall kuralını siler,
  `C:\Program Files\VaryaOne`'ı temizler. `%ProgramData%\VaryaOne` (pgdata,
  `master.key`, storage, backups) **korunur** — silinmez.
- Aynı/yeni sürüm üzerine kurulum: sihirbaz önce eski servisin gerçekten
  durduğunu doğrular, dosyaları değiştirir, sonra SCM kaydını sıfırdan onarır.
- Servis, dosya kopyalama başlamadan önce durdurulur; SCM kaydı sıfırdan
  onarılır ve `/health/ready` iki dakika içinde başarılı olmadan kurulum
  tamamlanmış sayılmaz.

## İstemci ve kontrol paneli (`cmd/varyaone-client`, Windows)

Tek `varyaone-client.exe` (WebView2 penceresi, saf Go — `jchv/go-webview2`):

- **Varya One** (argümansız): mDNS ile ağdaki sunucuyu arar, bulursa adresi
  doldurur; `%AppData%\VaryaOne\client.json` son çalışan adresi + geçmişi hatırlar.
  "Bağlan" → pencere sunucu arayüzüne geçer. Sağ altta "⚙" → kontrol paneli.
- **Varya Kontrol Paneli** (`--panel`): servis durumu, erişim adresleri, **Ağ
  modu** radio'ları (yükseltilmiş `varyaone netmode` çağırır — bir kez UAC),
  "Servisi yeniden başlat", "Loglar". Ağ modu satırı yalnızca yerel servis varsa
  anlamlı.
- İkon/manifest exe'ye `cmd/varyaone-client/winres/` + `rsrc_windows_amd64.syso`
  ile gömülür (`go-winres make`; `.syso` commit'li, build sırasında varsa
  tazelenir). WebView2 Runtime gerekir (Win10 21H2+/Win11'de hazır).

## Updater akışı (web ile aynı durum makinesi)

`internal/update` durum makinesi ve `/internal/update/*` uçları değişmedi. Fark
yalnızca taşıyıcıda:

Pulse toplayıcısı **varsayılan olarak** resmi uç + paylaşımlı anahtara ayarlıdır
(compose.yaml ile aynı), yani düz bir kurulum ekstra ayar olmadan yeni sürümleri
görür. Kendi kataloğunu kullanmak için `%ProgramData%\VaryaOne\settings.env`:

```
VARYAONE_PULSE_ENDPOINT=https://kendi-worker.example.workers.dev
VARYAONE_PULSE_INGEST_KEY=kendi-anahtarin
# kurulum ping'ini kapatmak istersen:
# VARYAONE_PULSE_INSTALL_PING=false
```

| Web (Linux) | Windows masaüstü |
|---|---|
| `deploy/varyaone-update-agent.sh` (systemd) `/internal/update/next` yoklar | `varyaone stack` içindeki döngü (`runUpdateApplier`, 2 dk) DB'den kuyruğu okur → `varyaone update-apply --target <v>`'yi **ayrık süreç** olarak başlatır (servisi durdurup yeniden başlatacağı için çocuk süreç olamaz) |
| `deploy.sh update`: git checkout + `docker compose build` | zip indir + SHA-256 doğrula + dosya değiştir |
| PG majör: yeni imajın sürümlü PGDATA'sına initdb + `.varya` restore (eski dizin volume'de kalır) | eski `pgdata` arşivle + yeni majör initdb + `.varya` restore |
| rollback: `git checkout <prev>` + `backup restore` | `Home\rollback\` geri kopyala + `backup restore` |

Fazlar aynı: `preflight → backup → download → stop → swap → [pg-upgrade] →
migrate → restart → healthcheck` (hata → `rollback`).

Operatör güncellemeyi kuyruğa aldığı anda hedef sürümün Windows artefakt URL'si
ve SHA-256 değeri sabitlenir. Katalog daha yeni bir sürüme ilerlese bile çalışan
iş yanlış zip'e kaymaz. Rollback hem uygulama exe'lerini hem `pgsql/`
binary'lerini geri getirir; sağlık kontrolü geçmezse “geri alındı” raporlanmaz.

### PostgreSQL majör yükseltmesi (`pg-upgrade` fazı)

Yeni zip'teki `pgsql/` binary'leri veri dizininden (`Home\pgdata\PG_VERSION`) daha
yeni bir majörse (örn. 18 → 19), cluster o veri dizinine karşı açılamaz. Updater
`swap`'tan sonra şunu yapar: eski `pgdata`'yı kenara al (`pgdata.pg18-<ts>`) →
yeni majörde `initdb` → `swap` öncesi alınan `.varya` dökümünü içine geri yükle →
`migrate`. Herhangi bir hata: yeni `pgdata` silinir, arşivlenen eski cluster
(tam pre-update hali) geri konur, eski binary'ler `Home\rollback\`'ten döner.

Tetikleyici, çalışan binary'ler ile veri dizininin majör karşılaştırmasıdır;
pulse'taki `pg_major` alanı yalnızca bilgilendirme/panel içindir.

## Kod imzalama (SmartScreen / Defender)

İmzasız `setup.exe` ve `.exe`'ler "bilinmeyen yayıncı" uyarısı verir. Bir
Authenticode sertifikası (tercihen OV/EV) alındığında:

1. `varyaone.exe` + `varyaone-client.exe`'yi `build-bundle.sh` içinde `signtool`
   ile imzala (Go derlemesinden sonra, zip'lemeden önce).
2. `iscc`'ye SignTool tanımını geç:
   ```
   iscc "/Ssigntool=signtool sign /fd sha256 /tr http://timestamp.digicert.com /td sha256 /f cert.pfx /p $PAROLA $f" ...
   ```
   ve `installer.iss`'te `SignTool=signtool` + `SignedUninstaller=yes` satırlarının
   yorumunu kaldır — Inno hem `setup.exe`'yi hem de kaldırıcıyı imzalar.

VERSIONINFO alanları (`build-bundle.sh` → `//DMyNumericVersion`, go-winres
`--file-version`) zaten dolduruluyor; imza olmadan tek başına yetmez ama yardımcı olur.

## initdb / postgres ve LocalSystem

Servis LocalSystem olarak çalışır. `postgres.exe` yükseltilmiş bir token ile
çalışmayı reddeder — ama biz onu hiç doğrudan çağırmıyoruz; `pg_ctl start`
kısıtlı bir token oluşturup postgres'i onunla başlatır (EDB'nin kendi servisiyle
aynı desen). `initdb` doğrudan çağrılır ve LocalSystem'de sorunsuz çalışır (Unix'teki
"root ile çalışamaz" kontrolü Windows'ta yok). VM testinde yine de gözlenecek.

## Sonraki adımlar

- Kontrol panelini genişlet: yedek al/geri yükle, güncelleme ilerlemesini
  `update.Service.Status()`'tan canlı göster.
- Ayrık updater sürecinin servis durdurulunca sağ kaldığını VM'de doğrula
  (`DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP`); gerekiyorsa
  `CREATE_BREAKAWAY_FROM_JOB` ekle. Sürekli çöken bir güncelleme 15 dk'da bir
  yeniden denenir (sıkı döngü değil ama Linux ajanındaki gibi bir "pes et" sayacı yok).
- CI'da release'i pulse admin ucuna otomatik kaydet.
- Kod imzalama sertifikası al, `build-bundle.sh` + `installer.iss` SignTool'u aç.
- 8080 dolu ise otomatik alternatif port dene (şu an sadece net hata veriyor).
