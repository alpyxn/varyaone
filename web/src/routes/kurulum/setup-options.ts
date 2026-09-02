import type { ModuleCode } from '$lib/modules';

// Mirrors the backend seed catalog in
// internal/platform/migrations/sql/000134_expand_variant_packages.sql.
// Keep package ids, definition codes and option codes in sync with that file —
// setup copies the selected package rows into the new company.

export type SectorPackageId =
  | 'GENEL'
  | 'HAZIR_GIYIM'
  | 'AYAKKABI'
  | 'IC_GIYIM'
  | 'CANTA'
  | 'TAKI'
  | 'KOZMETIK'
  | 'GIDA'
  | 'ICECEK'
  | 'MOBILYA'
  | 'BEYAZ_ESYA'
  | 'ELEKTRONIK'
  | 'HIRDAVAT'
  | 'KIRTASIYE'
  | 'EV_TEKSTILI'
  | 'PETSHOP';

export type SetupPackageOption = {
  code: string;
  name: string;
};

export type SetupPackageDefinition = {
  code: string;
  name: string;
  options: SetupPackageOption[];
};

export type SetupSectorPackage = {
  id: SectorPackageId;
  name: string;
  description: string;
  definitions: SetupPackageDefinition[];
};

export type SetupPayload = {
  admin_name: string;
  admin_email: string;
  password: string;
  legal_name: string;
  trade_name: string;
  entity_type: 'LEGAL_ENTITY' | 'SOLE_PROPRIETOR';
  tax_number?: string;
  sector_packages: SectorPackageId[];
  modules: ModuleCode[];
};

const opts = (...pairs: [string, string][]): SetupPackageOption[] =>
  pairs.map(([code, name]) => ({ code, name }));

// ---- Shared definitions (identical wherever they appear) --------------------

const RENK: SetupPackageDefinition = {
  code: 'RENK',
  name: 'Renk',
  options: opts(
    ['SIYAH', 'Siyah'],
    ['BEYAZ', 'Beyaz'],
    ['GRI', 'Gri'],
    ['ANTRASIT', 'Antrasit'],
    ['LACIVERT', 'Lacivert'],
    ['MAVI', 'Mavi'],
    ['TURKUAZ', 'Turkuaz'],
    ['YESIL', 'Yeşil'],
    ['HAKI', 'Haki'],
    ['KIRMIZI', 'Kırmızı'],
    ['BORDO', 'Bordo'],
    ['PEMBE', 'Pembe'],
    ['MOR', 'Mor'],
    ['SARI', 'Sarı'],
    ['HARDAL', 'Hardal'],
    ['TURUNCU', 'Turuncu'],
    ['KAHVERENGI', 'Kahverengi'],
    ['BEJ', 'Bej'],
    ['KREM', 'Krem'],
    ['EKRU', 'Ekru']
  )
};

const BEDEN: SetupPackageDefinition = {
  code: 'BEDEN',
  name: 'Beden',
  options: opts(
    ['XS', 'XS'],
    ['S', 'S'],
    ['M', 'M'],
    ['L', 'L'],
    ['XL', 'XL'],
    ['XXL', 'XXL'],
    ['XXXL', 'XXXL'],
    ['XXXXL', '4XL'],
    ['STANDART', 'Standart']
  )
};

const CINSIYET: SetupPackageDefinition = {
  code: 'CINSIYET',
  name: 'Cinsiyet',
  options: opts(
    ['KADIN', 'Kadın'],
    ['ERKEK', 'Erkek'],
    ['UNISEX', 'Unisex'],
    ['KIZ_COCUK', 'Kız Çocuk'],
    ['ERKEK_COCUK', 'Erkek Çocuk'],
    ['BEBEK', 'Bebek']
  )
};

const NUMARA: SetupPackageDefinition = {
  code: 'NUMARA',
  name: 'Numara',
  options: Array.from({ length: 17 }, (_, i) => {
    const n = String(30 + i);
    return { code: n, name: n };
  })
};

const DESEN: SetupPackageDefinition = {
  code: 'DESEN',
  name: 'Desen',
  options: opts(
    ['DUZ', 'Düz'],
    ['CIZGILI', 'Çizgili'],
    ['EKOSE', 'Ekose'],
    ['KARELI', 'Kareli'],
    ['KAZAYAGI', 'Kazayağı'],
    ['PUANTIYE', 'Puantiye'],
    ['CICEKLI', 'Çiçekli'],
    ['GEOMETRIK', 'Geometrik'],
    ['BASKILI', 'Baskılı']
  )
};

// ---- Packages --------------------------------------------------------------

export const SETUP_SECTOR_PACKAGES: readonly SetupSectorPackage[] = [
  {
    id: 'GENEL',
    name: 'Genel',
    description: 'Her sektör için temel varyant tanımları: renk, beden, model, malzeme.',
    definitions: [
      RENK,
      BEDEN,
      {
        code: 'MODEL',
        name: 'Model',
        options: opts(
          ['STANDART', 'Standart'],
          ['KLASIK', 'Klasik'],
          ['MODERN', 'Modern'],
          ['PREMIUM', 'Premium'],
          ['EKONOMIK', 'Ekonomik']
        )
      },
      {
        code: 'MALZEME',
        name: 'Malzeme',
        options: opts(
          ['PAMUK', 'Pamuk'],
          ['POLYESTER', 'Polyester'],
          ['DERI', 'Deri'],
          ['METAL', 'Metal'],
          ['PLASTIK', 'Plastik'],
          ['AHSAP', 'Ahşap'],
          ['CAM', 'Cam'],
          ['SERAMIK', 'Seramik'],
          ['KAGIT', 'Kağıt']
        )
      }
    ]
  },
  {
    id: 'HAZIR_GIYIM',
    name: 'Hazır Giyim & Tekstil',
    description: 'Renk, beden, cinsiyet, kumaş ve desen matrisiyle konfeksiyon ürünleri.',
    definitions: [
      RENK,
      BEDEN,
      CINSIYET,
      {
        code: 'KUMAS',
        name: 'Kumaş',
        options: opts(
          ['PAMUK', 'Pamuk'],
          ['POLYESTER', 'Polyester'],
          ['KETEN', 'Keten'],
          ['YUN', 'Yün'],
          ['VISKON', 'Viskon'],
          ['KOT', 'Kot (Denim)'],
          ['KADIFE', 'Kadife'],
          ['SATEN', 'Saten'],
          ['TRIKO', 'Triko'],
          ['KASMIR', 'Kaşmir']
        )
      },
      DESEN
    ]
  },
  {
    id: 'AYAKKABI',
    name: 'Ayakkabı & Terlik',
    description: 'Renk, numara, cinsiyet, model ve malzeme ile ayakkabı ürünleri.',
    definitions: [
      RENK,
      NUMARA,
      CINSIYET,
      {
        code: 'AYAKKABI_MODEL',
        name: 'Model',
        options: opts(
          ['SNEAKER', 'Sneaker'],
          ['KLASIK', 'Klasik'],
          ['BOT', 'Bot'],
          ['CIZME', 'Çizme'],
          ['SANDALET', 'Sandalet'],
          ['TERLIK', 'Terlik'],
          ['BABET', 'Babet'],
          ['TOPUKLU', 'Topuklu'],
          ['LOAFER', 'Loafer'],
          ['KOSU', 'Koşu Ayakkabısı']
        )
      },
      {
        code: 'AYAKKABI_MALZEME',
        name: 'Malzeme',
        options: opts(
          ['HAKIKI_DERI', 'Hakiki Deri'],
          ['SUNI_DERI', 'Suni Deri'],
          ['NUBUK', 'Nubuk'],
          ['SUET', 'Süet'],
          ['TEKSTIL', 'Tekstil'],
          ['KAUCUK', 'Kauçuk']
        )
      }
    ]
  },
  {
    id: 'IC_GIYIM',
    name: 'İç Giyim & Çorap',
    description: 'Renk, beden, cinsiyet, paket adedi ve ürün tipiyle iç giyim.',
    definitions: [
      RENK,
      BEDEN,
      CINSIYET,
      {
        code: 'PAKET_ADEDI',
        name: 'Paket Adedi',
        options: opts(
          ['TEKLI', 'Tekli'],
          ['IKILI', 'İkili'],
          ['UCLU', 'Üçlü'],
          ['DORTLU', 'Dörtlü'],
          ['ALTILI', 'Altılı'],
          ['DOKUZLU', 'Dokuzlu'],
          ['ONLU', 'Onlu']
        )
      },
      {
        code: 'URUN_TIPI',
        name: 'Ürün Tipi',
        options: opts(
          ['SUTYEN', 'Sütyen'],
          ['KULOT', 'Külot'],
          ['BOXER', 'Boxer'],
          ['ATLET', 'Atlet'],
          ['CORAP', 'Çorap'],
          ['PIJAMA', 'Pijama'],
          ['FANILA', 'Fanila']
        )
      }
    ]
  },
  {
    id: 'CANTA',
    name: 'Çanta & Valiz',
    description: 'Renk, çanta tipi, malzeme ve boyut ile çanta ürünleri.',
    definitions: [
      RENK,
      {
        code: 'CANTA_TIPI',
        name: 'Çanta Tipi',
        options: opts(
          ['OMUZ', 'Omuz Çantası'],
          ['SIRT', 'Sırt Çantası'],
          ['EL', 'El Çantası'],
          ['CUZDAN', 'Cüzdan'],
          ['BEL', 'Bel Çantası'],
          ['LAPTOP', 'Laptop Çantası'],
          ['VALIZ', 'Valiz'],
          ['SPOR', 'Spor Çantası'],
          ['POSTACI', 'Postacı Çantası'],
          ['PORTFOY', 'Portföy (Clutch)']
        )
      },
      {
        code: 'CANTA_MALZEME',
        name: 'Malzeme',
        options: opts(
          ['HAKIKI_DERI', 'Hakiki Deri'],
          ['SUNI_DERI', 'Suni Deri'],
          ['KANVAS', 'Kanvas'],
          ['NAYLON', 'Naylon'],
          ['KOT', 'Kot'],
          ['SUET', 'Süet']
        )
      },
      {
        code: 'BOYUT',
        name: 'Boyut',
        options: opts(['MINI', 'Mini'], ['KUCUK', 'Küçük'], ['ORTA', 'Orta'], ['BUYUK', 'Büyük'])
      }
    ]
  },
  {
    id: 'TAKI',
    name: 'Takı & Aksesuar',
    description: 'Metal rengi, takı tipi, taş, ayar ve yüzük ölçüsü.',
    definitions: [
      {
        code: 'METAL_RENGI',
        name: 'Metal Rengi',
        options: opts(
          ['ALTIN', 'Altın'],
          ['GUMUS', 'Gümüş'],
          ['ROSE_GOLD', 'Rose Gold'],
          ['PLATIN', 'Platin'],
          ['BAKIR', 'Bakır'],
          ['CELIK', 'Çelik'],
          ['PIRINC', 'Pirinç']
        )
      },
      {
        code: 'TAKI_TIPI',
        name: 'Takı Tipi',
        options: opts(
          ['YUZUK', 'Yüzük'],
          ['KOLYE', 'Kolye'],
          ['BILEKLIK', 'Bileklik'],
          ['KUPE', 'Küpe'],
          ['HALHAL', 'Halhal'],
          ['BROS', 'Broş'],
          ['SET', 'Set'],
          ['KUNYE', 'Künye']
        )
      },
      {
        code: 'TAS',
        name: 'Taş',
        options: opts(
          ['PIRLANTA', 'Pırlanta'],
          ['ZIRKON', 'Zirkon'],
          ['INCI', 'İnci'],
          ['YAKUT', 'Yakut'],
          ['ZUMRUT', 'Zümrüt'],
          ['SAFIR', 'Safir'],
          ['AKIK', 'Akik'],
          ['TASSIZ', 'Taşsız']
        )
      },
      {
        code: 'AYAR',
        name: 'Ayar',
        options: opts(
          ['AYAR_8', '8 Ayar'],
          ['AYAR_14', '14 Ayar'],
          ['AYAR_18', '18 Ayar'],
          ['AYAR_22', '22 Ayar'],
          ['GUMUS_925', '925 Gümüş'],
          ['CELIK_316L', '316L Çelik']
        )
      },
      {
        code: 'YUZUK_OLCUSU',
        name: 'Yüzük Ölçüsü',
        options: [8, 10, 12, 14, 16, 18, 20, 22, 24].map((n) => ({
          code: `OLCU_${n}`,
          name: String(n)
        }))
      }
    ]
  },
  {
    id: 'KOZMETIK',
    name: 'Kozmetik & Kişisel Bakım',
    description: 'Ton, hacim, cilt tipi, koku ve SPF ile kozmetik ürünleri.',
    definitions: [
      {
        code: 'URUN_TONU',
        name: 'Ton',
        options: opts(
          ['TON_01', '01 Fildişi'],
          ['TON_02', '02 Açık'],
          ['TON_03', '03 Orta'],
          ['TON_04', '04 Buğday'],
          ['TON_05', '05 Bal'],
          ['TON_06', '06 Koyu'],
          ['RENKSIZ', 'Renksiz']
        )
      },
      {
        code: 'HACIM',
        name: 'Hacim',
        options: opts(
          ['ML_15', '15 ml'],
          ['ML_30', '30 ml'],
          ['ML_50', '50 ml'],
          ['ML_75', '75 ml'],
          ['ML_100', '100 ml'],
          ['ML_150', '150 ml'],
          ['ML_200', '200 ml'],
          ['ML_400', '400 ml'],
          ['ML_500', '500 ml']
        )
      },
      {
        code: 'CILT_TIPI',
        name: 'Cilt Tipi',
        options: opts(
          ['NORMAL', 'Normal'],
          ['KURU', 'Kuru'],
          ['YAGLI', 'Yağlı'],
          ['KARMA', 'Karma'],
          ['HASSAS', 'Hassas'],
          ['TUM', 'Tüm Ciltler']
        )
      },
      {
        code: 'KOKU',
        name: 'Koku',
        options: opts(
          ['CICEKSI', 'Çiçeksi'],
          ['ODUNSU', 'Odunsu'],
          ['FERAH', 'Ferah'],
          ['TATLI', 'Tatlı'],
          ['MISK', 'Misk'],
          ['KOKUSUZ', 'Kokusuz']
        )
      },
      {
        code: 'SPF',
        name: 'SPF',
        options: opts(
          ['SPF_15', 'SPF 15'],
          ['SPF_30', 'SPF 30'],
          ['SPF_50', 'SPF 50'],
          ['SPF_50_PLUS', 'SPF 50+'],
          ['YOK', 'Yok']
        )
      }
    ]
  },
  {
    id: 'GIDA',
    name: 'Gıda & Şarküteri',
    description: 'Gramaj, ambalaj, içerik, saklama koşulu ve ürün özelliği.',
    definitions: [
      {
        code: 'GRAMAJ',
        name: 'Gramaj',
        options: opts(
          ['G_100', '100 g'],
          ['G_250', '250 g'],
          ['G_500', '500 g'],
          ['KG_1', '1 kg'],
          ['KG_2', '2 kg'],
          ['KG_5', '5 kg'],
          ['KG_10', '10 kg'],
          ['KG_25', '25 kg']
        )
      },
      {
        code: 'AMBALAJ',
        name: 'Ambalaj',
        options: opts(
          ['POSET', 'Poşet'],
          ['KUTU', 'Kutu'],
          ['KAVANOZ', 'Kavanoz'],
          ['TENEKE', 'Teneke'],
          ['VAKUM', 'Vakumlu'],
          ['DOYPACK', 'Doypack'],
          ['FILE', 'File']
        )
      },
      {
        code: 'ICERIK',
        name: 'İçerik',
        options: opts(
          ['SADE', 'Sade'],
          ['KAKAOLU', 'Kakaolu'],
          ['FINDIKLI', 'Fındıklı'],
          ['MEYVELI', 'Meyveli'],
          ['BALLI', 'Ballı'],
          ['ACILI', 'Acılı'],
          ['BAHARATLI', 'Baharatlı'],
          ['TUZSUZ', 'Tuzsuz']
        )
      },
      {
        code: 'SAKLAMA',
        name: 'Saklama Koşulu',
        options: opts(
          ['ODA', 'Oda Sıcaklığı'],
          ['SOGUK', 'Soğuk Zincir'],
          ['DONMUS', 'Dondurulmuş']
        )
      },
      {
        code: 'OZELLIK',
        name: 'Ürün Özelliği',
        options: opts(
          ['STANDART', 'Standart'],
          ['ORGANIK', 'Organik'],
          ['GLUTENSIZ', 'Glutensiz'],
          ['LAKTOZSUZ', 'Laktozsuz'],
          ['VEGAN', 'Vegan'],
          ['SEKERSIZ', 'Şekersiz']
        )
      }
    ]
  },
  {
    id: 'ICECEK',
    name: 'İçecek',
    description: 'Hacim, ambalaj, aroma ve şeker durumu ile içecekler.',
    definitions: [
      {
        code: 'HACIM_ICECEK',
        name: 'Hacim',
        options: opts(
          ['ML_200', '200 ml'],
          ['ML_250', '250 ml'],
          ['ML_330', '330 ml'],
          ['ML_500', '500 ml'],
          ['L_1', '1 lt'],
          ['L_1_5', '1,5 lt'],
          ['L_2_5', '2,5 lt'],
          ['L_5', '5 lt'],
          ['L_19', '19 lt']
        )
      },
      {
        code: 'AMBALAJ_ICECEK',
        name: 'Ambalaj',
        options: opts(
          ['PET', 'Pet Şişe'],
          ['CAM', 'Cam Şişe'],
          ['TENEKE', 'Teneke Kutu'],
          ['KARTON', 'Karton Kutu'],
          ['DAMACANA', 'Damacana']
        )
      },
      {
        code: 'AROMA',
        name: 'Aroma',
        options: opts(
          ['SADE', 'Sade'],
          ['KOLA', 'Kola'],
          ['PORTAKAL', 'Portakal'],
          ['LIMON', 'Limon'],
          ['VISNE', 'Vişne'],
          ['SEFTALI', 'Şeftali'],
          ['KAYISI', 'Kayısı'],
          ['NANE_LIMON', 'Nane Limon'],
          ['KARISIK', 'Karışık']
        )
      },
      {
        code: 'SEKER_DURUMU',
        name: 'Şeker Durumu',
        options: opts(
          ['SEKERLI', 'Şekerli'],
          ['SEKERSIZ', 'Şekersiz'],
          ['LIGHT', 'Light'],
          ['ZERO', 'Zero']
        )
      }
    ]
  },
  {
    id: 'MOBILYA',
    name: 'Mobilya & Dekorasyon',
    description: 'Renk, malzeme, kaplama, ölçü ve stil ile mobilya ürünleri.',
    definitions: [
      RENK,
      {
        code: 'MOBILYA_MALZEME',
        name: 'Malzeme',
        options: opts(
          ['MASIF_AHSAP', 'Masif Ahşap'],
          ['MDF', 'MDF'],
          ['SUNTA', 'Sunta'],
          ['METAL', 'Metal'],
          ['CAM', 'Cam'],
          ['RATTAN', 'Rattan'],
          ['PLASTIK', 'Plastik'],
          ['MERMER', 'Mermer'],
          ['SUNGER', 'Sünger']
        )
      },
      {
        code: 'KAPLAMA',
        name: 'Kaplama',
        options: opts(
          ['MAT', 'Mat'],
          ['PARLAK', 'Parlak'],
          ['LAKE', 'Lake'],
          ['DERI', 'Deri'],
          ['SUNI_DERI', 'Suni Deri'],
          ['KUMAS', 'Kumaş'],
          ['KADIFE', 'Kadife'],
          ['VELVET', 'Velvet']
        )
      },
      {
        code: 'OLCU_MOBILYA',
        name: 'Ölçü',
        options: opts(
          ['TEK_KISILIK', 'Tek Kişilik'],
          ['CIFT_KISILIK', 'Çift Kişilik'],
          ['IKILI', 'İkili'],
          ['UCLU', 'Üçlü'],
          ['KOSE', 'Köşe']
        )
      },
      {
        code: 'STIL',
        name: 'Stil',
        options: opts(
          ['MODERN', 'Modern'],
          ['KLASIK', 'Klasik'],
          ['RUSTIK', 'Rustik'],
          ['ENDUSTRIYEL', 'Endüstriyel'],
          ['ISKANDINAV', 'İskandinav'],
          ['COUNTRY', 'Country'],
          ['AVANGARDE', 'Avangarde']
        )
      }
    ]
  },
  {
    id: 'BEYAZ_ESYA',
    name: 'Beyaz Eşya',
    description: 'Renk, kapasite, enerji sınıfı ve montaj tipi.',
    definitions: [
      RENK,
      {
        code: 'KAPASITE',
        name: 'Kapasite',
        options: opts(
          ['KG_6', '6 kg'],
          ['KG_7', '7 kg'],
          ['KG_8', '8 kg'],
          ['KG_9', '9 kg'],
          ['KG_10', '10 kg'],
          ['KG_11', '11 kg'],
          ['LT_300', '300 lt'],
          ['LT_400', '400 lt'],
          ['LT_500', '500 lt'],
          ['LT_600', '600 lt']
        )
      },
      {
        code: 'ENERJI_SINIFI',
        name: 'Enerji Sınıfı',
        options: opts(
          ['SINIF_A', 'A'],
          ['SINIF_B', 'B'],
          ['SINIF_C', 'C'],
          ['SINIF_D', 'D'],
          ['SINIF_E', 'E'],
          ['SINIF_F', 'F'],
          ['SINIF_G', 'G']
        )
      },
      {
        code: 'MONTAJ_TIPI',
        name: 'Montaj Tipi',
        options: opts(
          ['SOLO', 'Solo'],
          ['ANKASTRE', 'Ankastre'],
          ['YARIM_ANKASTRE', 'Yarı Ankastre']
        )
      }
    ]
  },
  {
    id: 'ELEKTRONIK',
    name: 'Elektronik & Telefon',
    description: 'Renk, dahili hafıza, RAM, garanti ve ürün durumu.',
    definitions: [
      RENK,
      {
        code: 'DAHILI_HAFIZA',
        name: 'Dahili Hafıza',
        options: opts(
          ['GB_32', '32 GB'],
          ['GB_64', '64 GB'],
          ['GB_128', '128 GB'],
          ['GB_256', '256 GB'],
          ['GB_512', '512 GB'],
          ['TB_1', '1 TB']
        )
      },
      {
        code: 'RAM',
        name: 'RAM',
        options: opts(
          ['GB_2', '2 GB'],
          ['GB_3', '3 GB'],
          ['GB_4', '4 GB'],
          ['GB_6', '6 GB'],
          ['GB_8', '8 GB'],
          ['GB_12', '12 GB'],
          ['GB_16', '16 GB'],
          ['GB_32', '32 GB']
        )
      },
      {
        code: 'GARANTI',
        name: 'Garanti',
        options: opts(
          ['RESMI', 'Resmi Distribütör'],
          ['ITHALATCI', 'İthalatçı Garantili'],
          ['GARANTISIZ', 'Garantisiz']
        )
      },
      {
        code: 'URUN_DURUMU',
        name: 'Ürün Durumu',
        options: opts(
          ['SIFIR', 'Sıfır'],
          ['YENILENMIS', 'Yenilenmiş'],
          ['TESHIR', 'Teşhir Ürünü'],
          ['IKINCI_EL', 'İkinci El']
        )
      }
    ]
  },
  {
    id: 'HIRDAVAT',
    name: 'Hırdavat & Yapı Market',
    description: 'Renk, ebat, malzeme ve paket miktarı ile hırdavat ürünleri.',
    definitions: [
      RENK,
      {
        code: 'EBAT',
        name: 'Ebat',
        options: opts(
          ['MM_3', '3 mm'],
          ['MM_4', '4 mm'],
          ['MM_5', '5 mm'],
          ['MM_6', '6 mm'],
          ['MM_8', '8 mm'],
          ['MM_10', '10 mm'],
          ['MM_12', '12 mm'],
          ['MM_16', '16 mm'],
          ['MM_20', '20 mm'],
          ['INC_YARIM', '1/2 inç'],
          ['INC_UC_CEYREK', '3/4 inç'],
          ['INC_BIR', '1 inç']
        )
      },
      {
        code: 'HIRDAVAT_MALZEME',
        name: 'Malzeme',
        options: opts(
          ['CELIK', 'Çelik'],
          ['PASLANMAZ', 'Paslanmaz Çelik'],
          ['PIRINC', 'Pirinç'],
          ['GALVANIZ', 'Galvanizli'],
          ['ALUMINYUM', 'Alüminyum'],
          ['PLASTIK', 'Plastik'],
          ['BAKIR', 'Bakır'],
          ['DOKUM', 'Döküm']
        )
      },
      {
        code: 'PAKET_MIKTARI',
        name: 'Paket Miktarı',
        options: opts(
          ['ADET', 'Adet'],
          ['PK_10', "10'lu Paket"],
          ['PK_25', "25'li Paket"],
          ['PK_50', "50'li Paket"],
          ['PK_100', "100'lü Paket"],
          ['KUTU', 'Kutu'],
          ['KOLI', 'Koli']
        )
      }
    ]
  },
  {
    id: 'KIRTASIYE',
    name: 'Kırtasiye & Ofis',
    description: 'Renk, kağıt ebadı, çizgi tipi, yaprak sayısı ve cilt tipi.',
    definitions: [
      RENK,
      {
        code: 'KAGIT_EBADI',
        name: 'Kağıt Ebadı',
        options: opts(
          ['A3', 'A3'],
          ['A4', 'A4'],
          ['A5', 'A5'],
          ['A6', 'A6'],
          ['A7', 'A7'],
          ['B5', 'B5']
        )
      },
      {
        code: 'CIZGI_TIPI',
        name: 'Çizgi Tipi',
        options: opts(
          ['CIZGILI', 'Çizgili'],
          ['KARELI', 'Kareli'],
          ['NOKTALI', 'Noktalı'],
          ['CIZGISIZ', 'Çizgisiz'],
          ['PLANLI', 'Planlı'],
          ['MUZIK', 'Müzik']
        )
      },
      {
        code: 'YAPRAK_SAYISI',
        name: 'Yaprak Sayısı',
        options: opts(
          ['YP_40', '40 Yaprak'],
          ['YP_60', '60 Yaprak'],
          ['YP_80', '80 Yaprak'],
          ['YP_100', '100 Yaprak'],
          ['YP_120', '120 Yaprak'],
          ['YP_160', '160 Yaprak'],
          ['YP_200', '200 Yaprak']
        )
      },
      {
        code: 'CILT_TIPI_DEFTER',
        name: 'Cilt Tipi',
        options: opts(
          ['SPIRALLI', 'Spiralli'],
          ['TUTKALLI', 'Tutkallı'],
          ['DIKISLI', 'Dikişli'],
          ['PP_KAPAK', 'PP Kapak'],
          ['SERT_KAPAK', 'Sert Kapak']
        )
      }
    ]
  },
  {
    id: 'EV_TEKSTILI',
    name: 'Ev Tekstili',
    description: 'Renk, ürün tipi, ölçü, kumaş ve desen ile ev tekstili.',
    definitions: [
      RENK,
      {
        code: 'EV_URUN_TIPI',
        name: 'Ürün Tipi',
        options: opts(
          ['NEVRESIM', 'Nevresim Takımı'],
          ['CARSAF', 'Çarşaf'],
          ['YASTIK', 'Yastık'],
          ['YORGAN', 'Yorgan'],
          ['HAVLU', 'Havlu'],
          ['BORNOZ', 'Bornoz'],
          ['BATTANIYE', 'Battaniye'],
          ['PIKE', 'Pike'],
          ['PERDE', 'Perde'],
          ['MASA_ORTUSU', 'Masa Örtüsü']
        )
      },
      {
        code: 'EV_OLCU',
        name: 'Ölçü',
        options: opts(
          ['TEK_KISILIK', 'Tek Kişilik'],
          ['CIFT_KISILIK', 'Çift Kişilik'],
          ['KING', 'King Size'],
          ['BEBEK', 'Bebek'],
          ['EL_HAVLUSU', 'El Havlusu'],
          ['YUZ_HAVLUSU', 'Yüz Havlusu'],
          ['BANYO_HAVLUSU', 'Banyo Havlusu']
        )
      },
      {
        code: 'EV_KUMAS',
        name: 'Kumaş',
        options: opts(
          ['PAMUK', 'Pamuk'],
          ['RANFORCE', 'Ranforce'],
          ['BAMBU', 'Bambu'],
          ['SATEN', 'Saten'],
          ['PIKE', 'Pike'],
          ['MIKROFIBER', 'Mikrofiber'],
          ['POLAR', 'Polar'],
          ['KADIFE', 'Kadife']
        )
      },
      DESEN
    ]
  },
  {
    id: 'PETSHOP',
    name: 'Pet Shop & Evcil Hayvan',
    description: 'Hayvan türü, mama tipi, yaş grubu, ağırlık ve tat.',
    definitions: [
      {
        code: 'HAYVAN_TURU',
        name: 'Hayvan Türü',
        options: opts(
          ['KEDI', 'Kedi'],
          ['KOPEK', 'Köpek'],
          ['KUS', 'Kuş'],
          ['BALIK', 'Balık'],
          ['KEMIRGEN', 'Kemirgen'],
          ['SURUNGEN', 'Sürüngen'],
          ['KUMES', 'Kümes Hayvanı']
        )
      },
      {
        code: 'MAMA_TIPI',
        name: 'Mama Tipi',
        options: opts(
          ['KURU_MAMA', 'Kuru Mama'],
          ['YAS_MAMA', 'Yaş Mama'],
          ['ODUL_MAMASI', 'Ödül Maması'],
          ['TAHILSIZ', 'Tahılsız Mama'],
          ['KONSERVE', 'Konserve']
        )
      },
      {
        code: 'YAS_GRUBU',
        name: 'Yaş Grubu',
        options: opts(
          ['YAVRU', 'Yavru'],
          ['YETISKIN', 'Yetişkin'],
          ['SENIOR', 'Yaşlı (Senior)'],
          ['TUM_YASLAR', 'Tüm Yaşlar']
        )
      },
      {
        code: 'AGIRLIK',
        name: 'Ağırlık',
        options: opts(
          ['G_400', '400 g'],
          ['KG_1_5', '1,5 kg'],
          ['KG_3', '3 kg'],
          ['KG_10', '10 kg'],
          ['KG_15', '15 kg'],
          ['KG_20', '20 kg']
        )
      },
      {
        code: 'TAT',
        name: 'Tat',
        options: opts(
          ['TAVUK', 'Tavuklu'],
          ['SIGIR', 'Sığır Etli'],
          ['KUZU', 'Kuzu Etli'],
          ['SOMON', 'Somonlu'],
          ['HINDI', 'Hindili'],
          ['SEBZELI', 'Sebzeli'],
          ['KARISIK', 'Karışık']
        )
      }
    ]
  }
];

export function packageById(id: SectorPackageId) {
  return SETUP_SECTOR_PACKAGES.find((item) => item.id === id);
}

export function selectedPackageDefinitions(ids: readonly SectorPackageId[]) {
  const definitions = new Map<string, SetupPackageDefinition>();
  for (const id of ids) {
    for (const definition of packageById(id)?.definitions ?? []) {
      const current = definitions.get(definition.code);
      if (!current) {
        definitions.set(definition.code, {
          ...definition,
          options: [...definition.options]
        });
        continue;
      }
      const options = new Map(current.options.map((option) => [option.code, option]));
      for (const option of definition.options) options.set(option.code, option);
      current.options = [...options.values()];
    }
  }
  return [...definitions.values()];
}

export function packageConflicts(ids: readonly SectorPackageId[]) {
  const definitions = new Map<string, { packageName: string; definitionName: string }>();
  const conflicts: string[] = [];
  for (const id of ids) {
    const packageName = packageById(id)?.name ?? id;
    for (const definition of packageById(id)?.definitions ?? []) {
      const previous = definitions.get(definition.code);
      if (previous && previous.definitionName !== definition.name) {
        conflicts.push(
          `${definition.name} tanımı ${previous.packageName} ve ${packageName} paketlerinde farklı.`
        );
      }
      definitions.set(definition.code, {
        packageName,
        definitionName: definition.name
      });
    }
  }
  return [...new Set(conflicts)];
}

export function buildSetupPayload(
  input: Omit<SetupPayload, 'sector_packages' | 'modules'>,
  ids: readonly SectorPackageId[],
  modules: readonly ModuleCode[]
) {
  return {
    ...input,
    sector_packages: [...new Set(ids)],
    modules: [...new Set(modules)]
  } satisfies SetupPayload;
}
