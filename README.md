<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.svg">
  <img src="assets/logo-light.svg" alt="Varya One" width="300">
</picture>

### Açık kaynak. Tamamen ücretsiz.

</div>

Bir işletmenin hafızası hesaplarda, faturalarda, stok hareketlerinde ve
borçlarda yaşar. Bu hafızanın başka bir sistemin içinde kiracı olması gerekmez.

Varya One, Türkiye'deki işletmeler için geliştirilen açık kaynak bir ön muhasebe
ve ERP yazılımıdır. Kendi sunucunuza ya da ofisinizdeki bir bilgisayara kurulur.
Verileriniz sizin sisteminizde kalır.

Aylık abonelik yoktur. Kullanıcı başına ücret yoktur. Özellikleri kısıtlanmış bir
ücretsiz sürüm ile üzerine oturtulmuş bir "Pro" sürüm de yoktur. Tek sürüm vardır
ve o sürüm herkesindir.


---

## Varya One nedir?

Cari hesapları, stokları, satışları, alışları, kasa ve banka hareketlerini tek
yerde yönetmek için tasarlandı.

Türkiye'de bir işletmenin konuştuğu dili konuşur: cari, stok, irsaliye, tahsilat,
ödeme, kasa, banka, çek, senet, fatura, bordro. Bildik kavramlara yabancı isimler
vermez. Basit bir işi gereksiz adımlarla büyütmez. Ama yapılan işlemin geçmişini
de kolaylık uğruna ortadan kaldırmaz.

---

## Neden var?

Bir yazılım, işletmenin kendi verisini yine işletmeye kiralamamalıdır. Sunucu
başkasının, kurallar başkasının, fiyat başkasının ve kapatma kararı başkasının
elindeyse; veri size ait olsa bile onun üzerindeki kontrol eksik kalır.

Varya One bu durumu kabul etmez.

- Kendi sunucunuza kurulur.
- Veritabanı sizin denetiminizde kalır.
- Yerel ağda kullanılabilir.
- Kullanıcı veya şirket başına ücret talep etmez.
- Birden fazla şirket, şube ve depo aynı kurulumdan yönetilebilir.
- Kaynak kodu herkes tarafından incelenebilir ve değiştirilebilir.
- Ücretli sürümü yoktur.

---

## Kimler için?

Varya One;

- ön muhasebesini ve stoklarını tek yerde toplamak isteyen,
- verisini kendi sisteminde tutmayı önemseyen,
- aylık abonelik ve kullanıcı başına ücret ödemek istemeyen,
- birden fazla şirket, şube veya depo yöneten,
- muhasebecisiyle aynı kayıtlar üzerinden çalışmak isteyen,
- kullandığı yazılım üzerinde söz sahibi olmak isteyen

işletmeler için geliştiriliyor.

Küçük bir işletme için gereksiz derecede ağır, büyüyen bir işletme için kısa
sürede yetersiz kalan bir program olmamayı amaçlar.

---

## Neler yapabilirsiniz?

| Alan | Yapabilecekleriniz |
|---|---|
| **Cari** | Müşteri ve tedarikçi kartları, cari grupları, ekstreler ve bakiye takibi |
| **Stok** | Ürünler, hizmetler, varyantlar, barkodlar, çok depolu stok, transfer ve sayım |
| **Satış / Alış** | Teklif, sipariş, irsaliye, fatura, iade ve kısmi sevkiyat süreçleri |
| **Kasa / Banka** | Tahsilatlar, ödemeler, hesap hareketleri, çek ve senet işlemleri |
| **Personel** | Çalışan kartları, puantaj, izin, avans, maaş hesaplama ve bordro |
| **Sabit Kıymetler** | Demirbaş kartları ve çalışan zimmetleri |
| **Raporlar** | İşletme raporları, Excel aktarımları ve kişiye özel kontrol panelleri |

Yetkilendirme, iki adımlı doğrulama ve işlem günlüğü sonradan eklenen ayrıcalıklar
değil, sistemin temel parçalarıdır.

---

## Kurulum

### Sunucuya kurulum

```bash
git clone https://github.com/alpyxn/varyaone.git
cd varyaone
./deploy.sh install
```

Kurulum sihirbazı alan adı, e‑posta adresi ve kullanılacak portları sorar. Bir
alan adı tanımlarsanız HTTPS yapılandırmasını da hazırlar.


### Windows

Windows için kurulum dosyası
[GitHub Releases](https://github.com/alpyxn/varyaone/releases) sayfasından
indirilebilir. Kurulum sihirbazı gerekli bileşenleri hazırlar; ayrı bir
veritabanı kurmanız gerekmez.

---

## Yedekleme

Yedekleme ve geri yükleme işlemleri yönetim panelinden yapılır. Tek tıklamayla
alınan `.varya` yedeği; veritabanını, logoları, belge eklerini ve yüklenen diğer
dosyaları birlikte saklar. Aynı dosya daha sonra yine panelden geri yüklenebilir.

Düzenli aralıklarla yedek indirmeniz ve bu dosyaları farklı bir cihazda
saklamanız önerilir.

---

## Lisans

[GNU AGPL-3.0](LICENSE)
