# Tarayıcı testleri

Uygulamayı gerçek bir tarayıcıda, gerçek API'ye karşı çalıştırır. Üç şeyi
kanıtlar: her ekran doğru sayfayı açıyor, hedef genişliklerde **sayfa
seviyesinde yatay taşma** yok ve kritik akışlar UI → API → kalıcı kayıt
zincirini gerçekten tamamlıyor.

## Testler artık yazar

Eski hâli "yalnızca okur" diyordu; doğru değildi. Bugün `critical-flows.spec.ts`
cari kartı açar, fatura taslağı kaydeder, bakiye hareketlendirir ve ters kayıt
atar. Bu yüzden testler **paylaşılan bir kuruluma karşı çalıştırılmaz**: kendi
veritabanı, kendi API süreci ve kendi frontend süreci olan, sonunda atılan bir
yığın gerekir (aşağıya bakın).

Yazan testler kendi kayıtlarını, worker'a özel bir kodla üretir; paralel iki
koşu aynı bakiyeyi veya aynı belgeyi paylaşmaz.

## Yığını kurmak

CI ile aynı komutlar (`.github/scripts/e2e_stack.sh`):

```sh
# 1. Boş bir PostgreSQL
docker run -d --name varyaone-e2e-pg -p 15432:5432 \
  -e POSTGRES_DB=varyaone_e2e -e POSTGRES_USER=varyaone \
  -e POSTGRES_PASSWORD=e2e-only-password postgres:18.4-alpine

export VARYAONE_DATABASE_URL='postgres://varyaone:e2e-only-password@127.0.0.1:15432/varyaone_e2e?sslmode=disable'
export VARYAONE_MASTER_KEY='AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA='
export VARYAONE_HTTP_ADDR=127.0.0.1:18099

# 2. Şema ve fikstürler
go build -o /tmp/varyaone ./cmd/varyaone
/tmp/varyaone migrate up
VARYAONE_DEMO_MODE=true VARYAONE_DEMO_RESET_INTERVAL=0 /tmp/varyaone demo seed

# 3. API (demo modu KAPALI: test edilen şey sıradan bir kurulum olmalı)
/tmp/varyaone server &

# 4. Frontend (üretim derlemesi)
cd web && npm run build
VARYAONE_API_INTERNAL_URL=http://127.0.0.1:18099 PORT=5199 HOST=127.0.0.1 node build &
```

Sonra:

```sh
cd web
VARYAONE_E2E_BASE_URL=http://127.0.0.1:5199 npx playwright test
```

`VARYAONE_E2E_BASE_URL` verilmezse yerel geliştirme için Vite dev sunucusu
başlatılır. CI her zaman üretim derlemesini servis eder: dev sunucusunda geçen
bir test, kullanıcıya giden derlemeyi doğrulamış sayılmaz.

İlk kullanımda tarayıcı: `npx playwright install chromium`.

### Gereken iki şey

- **`postgresql-client`** kurulu olmalı. Sunucu yedekleme ve işlem rotalarını
  yalnızca `pg_dump`/`pg_restore` varsa bağlar; onlarsız `/ayarlar/yedekleme`
  sahada çalışandan başka bir sayfadır ve o rota testi kırmızı olur.
- **Demo modu yalnızca seed komutunda.** Firmayı, kullanıcıyı ve testlerin
  beklediği kayıtları o üretir. API demo modunda çalıştırılırsa otomatik giriş,
  sıfırlama perdesi ve sıfırlama uçları devreye girer; o zaman test edilen şey
  hiçbir müşteride çalışmayan bir kurulum olur.

### Ortam değişkenleri

`VARYAONE_E2E_BASE_URL`, `VARYAONE_E2E_API_URL`, `VARYAONE_E2E_EMAIL`,
`VARYAONE_E2E_PASSWORD`, `VARYAONE_E2E_COMPANY`, `VARYAONE_E2E_WORKERS`,
`VARYAONE_E2E_LAYER` (`quick` veya `full`).

## Projeler

- `setup` — bir kez giriş yapar, oturumu `e2e/.auth/user.json`'a yazar. Aynı
  zamanda giriş testidir: `/giris` dışına çıkmak yetmez, doğru şirketle
  uygulama kabuğunun açıldığı doğrulanır.
- `chromium` — masaüstü işaretleyici (`pointer: fine`). Diğer her şey.
- `touch` — Pixel 7 dokunmatik emülasyonu (`pointer: coarse`). Yalnızca
  `touch.spec.ts`; 44 px kuralları yalnızca kaba işaretleyicide geçerlidir.
  Aynı iş akışları iki projede tekrarlanmaz.

## Kapsam

| Dosya                       | Ne ölçüyor                                                                    |
| --------------------------- | ----------------------------------------------------------------------------- |
| `auth.setup.ts`             | Giriş, doğru şirket, kabuğun açılması                                         |
| `overflow-detector.spec.ts` | Taşma dedektörünün kendisi, sentetik küçük belgelerle                         |
| `shell.spec.ts`             | 320–1920 px taşma, mobil menü odak kapanı (Tab **ve** Shift+Tab), araç menüsü |
| `routes.spec.ts`            | Envanterdeki her rota, 390/768/1440 px; iniş URL'i ve sayfa başlığı           |
| `detail.spec.ts`            | Liste → kayıt → detay sayfası, 390/768/1440 px; **atlama yok**                |
| `workflows.spec.ts`         | Satır detayı, filtre, belge/ürün/cari formları, tablo kaydırması, tema        |
| `touch.spec.ts`             | Dokunma hedefi boyutları: kabuk, form aksiyonları, açılır pencere             |
| `critical-flows.spec.ts`    | Cari oluştur/oku, faturaya satır ekle-kaydet-aç, tahsilat ve ters kayıt       |
| `unsaved-changes.spec.ts`   | Kaydedilmemiş değişiklik koruması ve yerel kurtarma taslağı                   |

`routes.ts` rota envanteridir ve her rotanın **indiği URL ile başlığını** tutar.
`src/lib/routes-inventory.test.ts` bunu `src/routes` ile karşılaştırır: yeni bir
sayfa envantere eklenmeden Vitest kırmızı olur, yani hiçbir ekran sessizce
kapsam dışı kalamaz. Kapsam dışı bırakılanlar `EXCLUDED_ROUTES` içinde
gerekçesiyle yazılıdır.

## Neden `settle()` yok

Eskiden her gezinmeden sonra `networkidle` beklenip hatası yutuluyor, üstüne
sabit 250 ms ekleniyordu. Bu üç şeyi birbirinden ayırt edemez hâle getiriyordu:
ekranın yüklenmesi, giriş ekranına atılması ve 500 dönen bir sayfa — çünkü hata
ekranı da viewport'una gayet güzel sığar.

Yerine `fixtures/ready.ts` var: açılış perdesi kalkar, kabuk gerçekten oturumla
gelir, `/api/v1/*` trafiği durur (yalnız uygulamanın istekleri sayılır; açık bir
soket veya beş dakikalık bir yoklama beklemeyi kilitleyemez) ve sayfa üç
durumdan birine oturur — içerik, gerçek boş durum, hata. Hangisinin kabul
edildiğini test söyler.

Ayrıca her testte `fixtures/monitors.ts` çalışır: beklenmeyen bir API hatası
veya sayfa istisnası testi düşürür. Genel bir ignore listesi yoktur; tek
istisna, demo olmayan her kurulumun "hayır" cevabı olan `/demo/state` 404'üdür.

## Fikstür boşlukları

Seed'in doldurmadığı listeler `detail.spec.ts` içindeki `UNSEEDED_LISTS`
sabitinde gerekçesiyle yazılıdır. Bunlar çalışma anında `skip` edilmez — atlanan
bir test yeşil görünür ve kimse bakmaz; dosyadaki bir satır ise iş kalemidir.
