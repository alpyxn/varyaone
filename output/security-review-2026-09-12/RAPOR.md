# Varya One güvenlik incelemesi

Tarih: 12 Eylül 2026. Referans: `994e20c` üzerine mevcut çalışma ağacı değişiklikleri.

Üç bulgu: iki yüksek, bir orta önem derecesi. Dereceler istismar önkoşulları ve etkiye göre nitel değerlendirmedir; CVSS puanı değildir. Uygulama kaynak kodu değiştirilmedi. Bu klasörde yalnız rapor ve izole doğrulama dosyaları bulunur.

## 1. Yüksek — Son işlemler API'si şube/depo ve belge türü yetkilerini aşıyor

**Konum:** [dashboard/service.go:170](/home/user/projects/varyaone/internal/dashboard/service.go:170), [dashboard/service.go:180](/home/user/projects/varyaone/internal/dashboard/service.go:180), [HTTP rotası:21](/home/user/projects/varyaone/internal/platform/httpapi/dashboard.go:21).

`GET /api/v1/dashboard/recent-activity` için `inventory.read` bulunması stok sorgusunun tamamını açıyor. Sorgu yalnız `company_id` ile filtreleniyor; kullanıcının şube ve depo kapsamı uygulanmıyor. Belge sorgusunda ise `sales.quote.read` dahil listelenen izinlerden herhangi biri aynı firmanın tüm DRAFT belgelerini sorgulatıyor; `document_type_code` için yetki filtresi yok.

**Önkoşul:** İlgili firmada oturum ve örneğin yalnız bir depoyla sınırlı `inventory.read` veya yalnız `sales.quote.read` izni.

**Etki:** Kullanıcı yetkisiz depoların stok hareketlerini; yalnız teklif okuma izniyle başka belge türlerinin taslak numarası, cari adı, toplam tutarı, dövizi ve kimliğini görebilir. Detay API'sinin isteği reddetmesi bu özet sızıntısını engellemez. Bu bulgu aynı firma içindeki yetki ayrımıyla ilgilidir; başka firmanın verisinin okunduğu iddia edilmiyor.

**Kanıt:** İzole test gerçek `RecentActivity` metodunun oluşturduğu SQL'i yakaladı. İki izin senaryosunda da sorguya yalnız firma kimliği gönderiliyor; kullanıcı/şube/depo kapsamı bulunmuyor ve teklif izni genel taslak sorgusunu açıyor. Ana envanter sorguları ise `membership_branch_scopes` ve `membership_warehouse_scopes` kullanıyor ([örnek:58](/home/user/projects/varyaone/internal/inventory/service.go:58)). Baseline RLS politikaları yalnız firma seviyesinde; eksik şube/depo filtresini tamamlamıyor.

**Düzeltme:** Her kaynak sorgusuna ilgili domain okuma API'siyle aynı kapsam koşullarını uygulayın. Belge türlerini kullanıcının okuyabildiği türlerle sınırlayın. İki şube/depo ve farklı belge türleri içeren veritabanı regresyon testi ekleyin.

## 2. Yüksek — Etkin iki adımlı doğrulama yeniden kimlik doğrulaması olmadan değiştirilebiliyor

**Konum:** [BeginTOTP:741](/home/user/projects/varyaone/internal/identity/service.go:741), [ConfirmTOTP:757](/home/user/projects/varyaone/internal/identity/service.go:757), [rotalar:56](/home/user/projects/varyaone/internal/platform/httpapi/identity.go:56).

`POST /api/v1/security/totp/setup` mevcut TOTP etkin olsa bile yeni sır üretiyor ve çağırana veriyor. `/security/totp/confirm` yalnız bu yeni sırdan üretilen kodu doğrulayıp etkin sırrın üstüne yazıyor ve kurtarma kodlarını yeniliyor. Parola veya eski TOTP ile yeniden doğrulama yok. Buna karşılık `DisableTOTP` parola istiyor.

**Önkoşul:** Geçerli kullanıcı tarayıcı oturumu ve o oturumun CSRF belirteci. Örneğin ele geçirilmiş veya açık bırakılmış bir oturum. Kimlik doğrulamasız uzaktan istismar değildir.

**Etki:** Saldırgan mevcut ikinci faktörü kendi kontrolündeki faktörle değiştirebilir ve kullanıcının kurtarma kodlarını geçersiz kılabilir. Tek başına kullanıcının parolasını öğrenmez; fakat ikinci faktörün kontrolünü devralır ve sonraki girişleri engelleyebilir.

**Kanıt:** İzole test `TOTPEnabled: true` kullanıcıyla gerçek `BeginTOTP` ve `ConfirmTOTP` metotlarını çağırdı. Yeni sırla üretilen kod, etkin sırrı değiştiren SQL'e ulaştı ve sekiz kurtarma kodu döndü; parola/eski kod verilmedi. Veritabanı test dublörü kullanıldı; canlı kullanıcı hesabı değiştirilmedi.

**Düzeltme:** Etkin TOTP'nin değiştirilmesini yakın zamanda doğrulanmış parola ve mevcut ikinci faktöre bağlayın veya ayrı güvenli kurtarma akışına yönlendirin. Bekleyen kurulumu süreli ve doğrulanan oturuma bağlı tutun; faktör değişiminde diğer oturumların iptalini uygulayın.

## 3. Orta — Sahte X-Forwarded-For giriş deneme sınırını etkisizleştiriyor

**Konum:** [clientIP:629](/home/user/projects/varyaone/internal/platform/httpapi/identity.go:629), [Login:508](/home/user/projects/varyaone/internal/identity/service.go:508), [compose.yaml:87](/home/user/projects/varyaone/compose.yaml:87).

`clientIP`, isteğin güvenilir bir proxy'den gelip gelmediğini kontrol etmeden `X-Forwarded-For` başlığının ilk değerini kullanıyor. Giriş sınırı `email_hash AND ip_hash` çifti üzerinde beş başarısız deneme. Aynı e-posta için başlık değiştirilince farklı sayaç seçiliyor.

**Önkoşul:** Backend API'sine doğrudan ağ erişimi veya başlığı güvenli biçimde yeniden yazmayan bir proxy. Compose varsayılanı API portunu `0.0.0.0` üzerinde yayımlıyor. Gerçek kurulumun ağdan erişilebilirliği test edilmedi. API yalnız güvenilir proxy arkasında kapalıysa bu doğrudan yolun etkisi azalır.

**Etki:** Tek istemci giriş deneme sınırını farklı IP başlıklarıyla aşabilir; parola ve TOTP tahminlerine karşı koruma zayıflar. Denetim kayıtlarının kaynak IP'si de sahteleştirilebilir.

**Kanıt:** İzole test aynı `RemoteAddr` için iki farklı başlık gönderdi; gerçek `clientIP` fonksiyonu ikisini de kabul etti. Sayaç seçiminin bu değere bağlı olduğu `Login` sorgusundan doğrulandı. Canlı giriş denemesi veya parola taraması yapılmadı.

**Düzeltme:** Yönlendirilmiş IP'yi yalnız açıkça tanımlı güvenilir proxy adreslerinden kabul edin. Doğrudan API portunu varsayılan olarak dış erişime kapatın. Hesap ve IP için bağımsız deneme sınırları uygulayın.

## Doğrulama ve kapsam

- `go test ./internal/identity ./internal/platform/httpapi ./internal/storage`: başarılı; bazı sonuçlar Go test önbelleğinden.
- Üç özel doğrulama testi başarılı. Bu testlerde PASS, raporlanan mevcut davranışın yeniden üretildiği anlamına gelir; güvenli olduğu anlamına gelmez.
- Tekrar çalıştırma (proje kökünde):

```sh
go test -overlay /home/user/projects/varyaone/output/security-review-2026-09-12/overlay.json ./internal/identity ./internal/platform/httpapi ./internal/dashboard -run TestSecurityReview -v
```

Overlay kaynak ağacına test eklemeden bu klasördeki dosyaları Go derlemesine dahil eder. TOTP testi veritabanı dublörü, dashboard testi SQL yakalama yöntemi, IP testi gerçek HTTP istek nesnesi kullanır.

`VARYAONE_TEST_DATABASE_URL` tanımlı olmadığından veritabanına bağlı giriş/TOTP entegrasyon testi atlandı. Uçtan uca istismar ve gerçek veritabanı üzerinde veri sızıntısı yeniden üretilmedi. Güncel bağımlılık CVE taraması yapılmadı; bağımlılıkların açık içermediği sonucu çıkarılamaz.

İnceleme kimlik doğrulama, oturum/CSRF, yetki ve firma kapsamı, seçili medya/dosya ve frontend proxy akışlarına odaklandı. Mevcut graphify haritası yön bulmak için kullanıldı; eski tarihli olduğu için bulgular doğrudan güncel kaynaklardan doğrulandı. İnceleme projenin tamamında başka açık bulunmadığı garantisi değildir.

Sistem yedeklerinin tüm firmaları kapsaması kaynakta açıkça kurulum operatörü politikası olarak tanımlandığı için ayrı bir yetki açığı olarak sayılmadı.
