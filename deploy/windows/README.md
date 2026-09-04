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
| Pulse yapılandırması (kurulum sayacı + geri bildirim) | `settings.go` + `supervisor.go` `config()` | ✅ resmi uç varsayılan, `settings.env` ile geçersiz kılınır — kullanım telemetrisi yok |
| VC++ çalışma zamanı (gömülü PG için şart) | `fetch-tools.ps1` indirir, `installer.iss` `[Code]` sessiz kurar | ✅ yoksa `initdb` `0xC0000135` verir |
| WebView2 runtime (istemci için şart) | `fetch-tools.ps1` tam x64 Evergreen standalone paketi indirir, installer çevrimdışı kurar | ✅ internet gerektirmez |
| WebP codec | saf Go codec; Windows binary `CGO_ENABLED=0` kalabilir | ✅ başlangıçta native DLL gerektirmez |
| Firewall: 8080/TCP + 5353/UDP (mDNS) | `netmode.go` `reconcileFirewall` (private+domain profili) | ✅ servis LocalSystem olduğu için prompt çıkmaz |
| Port çakışması ön kontrolü | `supervisor.go` `checkHTTPPortFree` | ✅ "8080 kullanımda" net mesajı |
| Kod imzalama (SmartScreen) | `installer.iss` SignTool iskeleti | ⏳ sertifika gerek (bkz. aşağı) |
| SPA static build (`VARYAONE_ADAPTER=static`) | `web/svelte.config.js` | ✅ hazır |
| Windows artefaktları (zip + `setup.exe` + sha256) | `.github/workflows/desktop-windows.yml` | ✅ hazır — her tag'de otomatik derlenip release'e eklenir |
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
#    -> dist/windows/varyaone-v0.3.0-windows-amd64.zip  (dağıtım arşivi)
```

Elle akışta 3. adım yoktur: `.github/workflows/desktop-windows.yml` bir `v*`
tag'i push'landığında paketleri derler, release'i draft olarak açar ve her
asset yüklendikten sonra yayınlar.

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
  `VaryaOne` servisi (otomatik başlatma, açılışta) `varyaone stack`'i sürer.
  Servis her başlayışında kendi SCM kaydını denetler: eski kurulumlardaki
  *gecikmeli* otomatik başlatma / manuel başlatma otomatiğe çekilir ve çökme
  sonrası 15 sn'lik yeniden başlatma aksiyonu tazelenir.
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
  "Bağlan" → pencere sunucu arayüzüne geçer. Sağ altta "⚙" → kontrol panelini
  **ayrı pencerede** açar (uygulama penceresi sunucuda kalır).
- **Varya Kontrol Paneli** (`--panel`): servis durumu, erişim adresleri (salt
  bilgi), **Ağ modu** radio'ları (yükseltilmiş `varyaone netmode` çağırır — bir
  kez UAC) ve dört eylem: "Servisi yeniden başlat", "Servisi durdur", "Servisi
  onar", "Loglar". Panel sunucu arayüzünü **açmaz**; o iş uygulama penceresinin.
- Her iki mod da adlandırılmış mutex ile **tek örnek**: ikinci bir kısayol
  tıklaması (ya da açılıştaki `--tray` girdisiyle çakışma) yeni pencere/tepsi
  ikonu üretmez, çalışan örneği öne getirir.
- İkon/manifest exe'ye `cmd/varyaone-client/winres/` + `rsrc_windows_amd64.syso`
  ile gömülür (`go-winres make`; `.syso` commit'li, build sırasında varsa
  tazelenir). WebView2 Runtime gerekir (Win10 21H2+/Win11'de hazır).

## Güncelleme

Varya One kendi kendini güncellemez. Yeni sürüm, GitHub Release'teki kurulum
sihirbazı (`setup.exe`) çalıştırılarak kurulur; kurulum mevcut veriyi ve
ayarları yerinde bırakır.

Sunucu (Linux/compose) tarafında karşılığı:

```
git pull && ./deploy.sh rebuild
```

Her iki tarafta da önce yedek almak iyi olur (`./deploy.sh backup`, ya da
Windows'ta Ayarlar → Yedekleme).

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
- Kod imzalama sertifikası al, `build-bundle.sh` + `installer.iss` SignTool'u aç.
- 8080 dolu ise otomatik alternatif port dene (şu an sadece net hata veriyor).
