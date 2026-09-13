#!/bin/sh
set -eu

project_dir=${VARYAONE_PROJECT_DIR:-$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)}
cd "$project_dir"

# Domainli kurulumda host nginx yapılandırması buraya üretilir; oradan
# /etc/nginx altına bağlanır (symlink veya kopya). Bkz. configure_host_nginx.
NGINX_CONF_DIR="$project_dir/deploy/nginx/host"
CERTBOT_WEBROOT="/var/www/certbot"

banner() {
  printf '\n'
  cat <<'EOF'
   __     __                        ___
   \ \   / /_ _  _ __ _   _  __ _   / _ \  _ __   ___
    \ \ / / _` || '__| | | |/ _` | | | | || '_ \ / _ \
     \ V / (_| || |  | |_| | (_| | | |_| || | | |  __/
      \_/ \__,_||_|   \__, |\__,_|  \___/ |_| |_|\___|
                      |___/
EOF
  if [ -n "${1:-}" ]; then
    printf '        Kurumsal Kaynak Planlama  ·  %s\n\n' "$1"
  else
    printf '        Kurumsal Kaynak Planlama\n\n'
  fi
}

# ask <soru> <varsayilan> -> stdout: kullanicinin girdisi (bos ise varsayilan)
ask() {
  _prompt=$1
  _default=${2:-}
  if [ -n "$_default" ]; then
    printf '  %s [%s]: ' "$_prompt" "$_default" >&2
  else
    printf '  %s: ' "$_prompt" >&2
  fi
  IFS= read -r _answer || _answer=""
  [ -z "$_answer" ] && _answer=$_default
  printf '%s' "$_answer"
}

# ask_yesno <soru> <e|h> -> exit 0 (evet) / 1 (hayir)
ask_yesno() {
  _default=${2:-h}
  while :; do
    _hint=$( [ "$_default" = "e" ] && echo "E/h" || echo "e/H" )
    printf '  %s (%s): ' "$1" "$_hint" >&2
    IFS= read -r _a || _a=""
    [ -z "$_a" ] && _a=$_default
    case "$_a" in
      e|E|evet|Evet|y|Y|yes) return 0 ;;
      h|H|hayir|hayır|Hayir|n|N|no) return 1 ;;
      *) printf '  Lütfen e veya h girin.\n' >&2 ;;
    esac
  done
}

interactive() {
  [ -t 0 ] && [ -t 1 ]
}

# ===========================================================================
#  Ortam hazırlığı (bulletproof kurulum): OS algıla, Docker + Compose kur
# ===========================================================================

DK="docker"          # ensure_docker_access gerekirse "sudo docker" yapar
SUDO=""
NEED_RELOGIN=0
PREREQS_READY=0

have() { command -v "$1" >/dev/null 2>&1; }

fetch() {
  if have curl; then curl -fsSL --retry 3 --retry-delay 2 --max-time 120 --proto '=https' --tlsv1.2 "$1"
  elif have wget; then wget -q --tries=3 --timeout=60 -O- "$1"
  else return 1
  fi
}

# Uzak bir betiği indirip çalıştır — ama indirmeyi ve çalıştırmayı AYIR.
#
# `fetch <url> | sh` POSIX sh'de indirmenin başarısını taşımaz: boru hattının
# çıkış kodu son komuta aittir, yani yarım inen ya da hiç inmeyen bir betik
# `sh`'e verilir ve `sh` memnuniyetle 0 döner. Yarım inmiş bir kurulum betiği,
# tam olarak çalıştırılmaması gereken şeydir.
#
# Önce geçici bir dosyaya indir, boş olmadığını ve makul göründüğünü doğrula,
# sonra çalıştır.
fetch_and_run_root() {
  _url=$1
  _f=$(mktemp "${TMPDIR:-/tmp}/varyaone-fetch.XXXXXX") || return 1
  chmod 600 "$_f" 2>/dev/null || true
  if ! fetch "$_url" > "$_f"; then
    rm -f "$_f"
    echo "  İndirilemedi: $_url" >&2
    return 1
  fi
  if [ ! -s "$_f" ]; then
    rm -f "$_f"
    echo "  İndirilen dosya boş: $_url" >&2
    return 1
  fi
  # Bir kabuk betiği mi, yoksa proxy'nin araya soktuğu bir HTML hata sayfası mı?
  if ! head -c 2 "$_f" | grep -q '#!' && ! head -n 20 "$_f" >/dev/null 2>&1; then
    rm -f "$_f"
    echo "  İndirilen içerik bir betik değil: $_url" >&2
    return 1
  fi
  case "$(head -n1 "$_f")" in
    '#!'*) : ;;
    *) rm -f "$_f"; echo "  İndirilen içerik bir betik değil (shebang yok): $_url" >&2; return 1 ;;
  esac
  _rc=0; run_root sh "$_f" || _rc=$?
  rm -f "$_f"
  return $_rc
}

elevate() {
  if [ "$(id -u)" = 0 ]; then SUDO=""; return 0; fi
  if have sudo; then SUDO="sudo"; return 0; fi
  return 1
}

run_root() {
  if [ -n "$SUDO" ]; then $SUDO "$@"; else "$@"; fi
}

OS_ID=""; OS_NAME=""; OS_FAMILY=""; PKG=""; ARCH=""
detect_os() {
  ARCH=$(uname -m 2>/dev/null || echo unknown)
  if [ -r /etc/os-release ]; then
    _osr=$(. /etc/os-release 2>/dev/null; printf '%s\n%s\n%s' "${ID:-}" "${ID_LIKE:-}" "${PRETTY_NAME:-${NAME:-}}")
    OS_ID=$(printf '%s' "$_osr" | sed -n 1p)
    _like=$(printf '%s' "$_osr" | sed -n 2p)
    OS_NAME=$(printf '%s' "$_osr" | sed -n 3p)
  else
    OS_ID=$(uname -s 2>/dev/null | tr 'A-Z' 'a-z'); OS_NAME=$OS_ID; _like=""
  fi
  [ -n "$OS_NAME" ] || OS_NAME=$OS_ID
  case " $OS_ID $_like " in
    *" debian "* | *" ubuntu "* | *" raspbian "* | *" linuxmint "* | *" pop "* | *" pop_os "* | *" neon "* | *" elementary "*)
      OS_FAMILY=debian; PKG=apt ;;
    *" fedora "* | *" rhel "* | *" centos "* | *" rocky "* | *" almalinux "* | *" ol "* | *" amzn "* | *" cloudlinux "*)
      OS_FAMILY=rhel; if have dnf; then PKG=dnf; else PKG=yum; fi ;;
    *" suse "* | *" opensuse "* | *" opensuse-leap "* | *" opensuse-tumbleweed "* | *" sles "*)
      OS_FAMILY=suse; PKG=zypper ;;
    *" arch "* | *" archarm "* | *" manjaro "* | *" endeavouros "* | *" cachyos "*)
      OS_FAMILY=arch; PKG=pacman ;;
    *" alpine "*)
      OS_FAMILY=alpine; PKG=apk ;;
    *)
      OS_FAMILY=unknown; PKG="" ;;
  esac
}

_pkg_refreshed=0
pkg_install() {
  [ $# -gt 0 ] || return 0
  case "$PKG" in
    apt)
      if [ "$_pkg_refreshed" = 0 ]; then
        run_root env DEBIAN_FRONTEND=noninteractive apt-get update -qq || true
        _pkg_refreshed=1
      fi
      run_root env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "$@" ;;
    dnf)    run_root dnf install -y "$@" ;;
    yum)    run_root yum install -y "$@" ;;
    zypper) run_root zypper --non-interactive install --no-recommends "$@" ;;
    pacman)
      # Arch'ta `pacman -Sy` ardından tek paket kurmak kısmi yükseltmedir ve
      # sistemi tutarsız bırakabilir; desteklenen yol `-Syu`'dur. Host'u
      # sessizce topluca yükseltmemek için bu açık bir tercihtir: varsayılan
      # olarak yalnız kurar ve depo listesinin güncelliğini operatöre bırakır.
      if [ "$_pkg_refreshed" = 0 ]; then
        if [ "${VARYAONE_ARCH_FULL_UPGRADE:-0}" = 1 ]; then
          run_root pacman -Syu --noconfirm || return 1
        else
          echo "  Not: Arch'ta depo listesi yenilenmiyor (kısmi yükseltmeyi önlemek için)." >&2
          echo "  Paket bulunamazsa:  sudo pacman -Syu  ya da VARYAONE_ARCH_FULL_UPGRADE=1" >&2
        fi
        _pkg_refreshed=1
      fi
      run_root pacman -S --noconfirm --needed "$@" ;;
    apk)    run_root apk add --no-cache "$@" ;;
    *)      return 1 ;;
  esac
}

# Betiğin gerçekten kullandığı araçlar. Bunlar Docker hazır olsa bile
# kontrol edilir: eskiden Docker varsa bu adım tümden atlanıyordu ve curl'e
# ihtiyaç duyan ilerideki adımlar sebebi belirsiz biçimde başarısız oluyordu.
ensure_basic_tools() {
  _need=""
  # curl VEYA wget yeterli demek, curl'ü doğrudan çağıran kodla çelişiyordu.
  # Gerçekten gereken araçlar tek tek istenir.
  have curl || _need="$_need curl"
  have openssl || _need="$_need openssl"
  have tar || _need="$_need tar"
  { [ -e /etc/ssl/certs/ca-certificates.crt ] || [ -e /etc/pki/tls/certs/ca-bundle.crt ] || [ -e /etc/ssl/cert.pem ]; } \
    || _need="$_need ca-certificates"
  if [ -n "$_need" ]; then
    echo "  Temel araçlar kuruluyor:$_need"
    pkg_install $_need || echo "  Uyarı: paket kurulumu başarısız ($_need)." >&2
  fi

  # Kurulamayanları açıkça söyle. "Devam ediliyor" deyip sonra o araç yokken
  # anlaşılmaz bir hata vermek, eksikliği gizlemenin uzun yoludur.
  _missing=""
  for _t in curl openssl tar; do have "$_t" || _missing="$_missing $_t"; done
  if [ -n "$_missing" ]; then
    echo "  Eksik araçlar:$_missing" >&2
    echo "  Bunlar olmadan kurulum tamamlanamaz; elle kurup tekrar deneyin." >&2
    return 1
  fi
  return 0
}

install_docker_engine() {
  echo "  Docker Engine kuruluyor (${OS_NAME:-?} / ${ARCH})..."
  case "$OS_FAMILY" in
    debian | rhel | suse)
      fetch_and_run_root https://get.docker.com ;;
    arch)
      pkg_install docker docker-compose ;;
    alpine)
      pkg_install docker docker-cli-compose ;;
    *)
      if have curl || have wget; then
        echo "  Bilinmeyen dağıtım — resmi Docker betiği deneniyor."
        fetch_and_run_root https://get.docker.com
      else
        return 1
      fi ;;
  esac
}

enable_docker_service() {
  if have systemctl; then
    run_root systemctl enable --now docker >/dev/null 2>&1 \
      || run_root systemctl start docker >/dev/null 2>&1 || true
  elif have rc-update; then
    run_root rc-update add docker default >/dev/null 2>&1 || true
    run_root rc-service docker start >/dev/null 2>&1 \
      || run_root service docker start >/dev/null 2>&1 || true
  elif have service; then
    run_root service docker start >/dev/null 2>&1 || true
  fi
  # daemon soketinin gelmesini kısa süre bekle
  _i=0
  while [ "$_i" -lt 15 ]; do
    $DK info >/dev/null 2>&1 && return 0
    { [ "$(id -u)" != 0 ] && run_root docker info >/dev/null 2>&1; } && return 0
    _i=$((_i + 1)); sleep 1
  done
}

install_compose_plugin() {
  $DK compose version >/dev/null 2>&1 && return 0
  case "$PKG" in
    apt)         pkg_install docker-compose-plugin || pkg_install docker-compose-v2 || true ;;
    dnf | yum)   pkg_install docker-compose-plugin || true ;;
    zypper)      pkg_install docker-compose || true ;;
    pacman)      pkg_install docker-compose || true ;;
    apk)         pkg_install docker-cli-compose || true ;;
  esac
}

ensure_docker_access() {
  [ "$(id -u)" = 0 ] && return 0
  $DK info >/dev/null 2>&1 && return 0
  if have usermod; then
    run_root usermod -aG docker "$(id -un)" >/dev/null 2>&1 || true
  elif have gpasswd; then
    run_root gpasswd -a "$(id -un)" docker >/dev/null 2>&1 || true
  fi
  # Yeni grup üyeliği bu oturumda geçerli değil; kalan işleri sudo ile sürdür.
  if ! $DK info >/dev/null 2>&1 && [ -n "$SUDO" ]; then
    DK="$SUDO docker"
    NEED_RELOGIN=1
  fi
}

# Docker + Compose hazır değilse kurar; hazırsa hızlıca döner.
ensure_prereqs() {
  [ "$PREREQS_READY" = 1 ] && return 0

  if $DK info >/dev/null 2>&1 && $DK compose version >/dev/null 2>&1; then
    # Docker hazır olması, betiğin kullandığı diğer araçların var olduğunu
    # söylemez. Eskiden bu kontrol tümden atlanıyordu ve curl'e ihtiyaç duyan
    # ilerideki adımlar sebebi belirsiz biçimde başarısız oluyordu.
    ensure_basic_tools || exit 1
    PREREQS_READY=1
    return 0
  fi
  # Docker kurulu ama gruba erişim yoksa: sudo ile dene, kurulumu atla.
  if [ "$(id -u)" != 0 ] && have sudo && have docker \
     && sudo docker info >/dev/null 2>&1 && sudo docker compose version >/dev/null 2>&1; then
    DK="sudo docker"
    NEED_RELOGIN=1
    SUDO="sudo"
    ensure_docker_access
    echo "  (Docker kurulu; bu oturumda sudo ile kullanılacak — kalıcı için çıkış/giriş yapın.)"
    PREREQS_READY=1
    return 0
  fi

  detect_os
  echo
  echo "  Ortam hazırlanıyor — sistem: ${OS_NAME:-bilinmiyor} (${ARCH})"

  if ! elevate; then
    echo >&2
    echo "  Eksik bileşenler var (Docker/Compose) ama root veya sudo yok." >&2
    echo "  Bu betiği 'sudo ./deploy.sh install' ile veya root olarak çalıştırın." >&2
    exit 1
  fi

  if ! have docker && [ -z "$PKG" ] && ! { have curl || have wget; }; then
    echo "  Paket yöneticisi tanınamadı ve curl/wget yok. Docker'ı elle kurun:" >&2
    echo "    https://docs.docker.com/engine/install/" >&2
    exit 1
  fi

  if interactive; then
    echo
    echo "  Yapılacaklar:"
    have docker || echo "    • Docker Engine + Compose eklentisi kurulumu"
    { have docker && ! $DK compose version >/dev/null 2>&1; } && echo "    • Docker Compose eklentisi kurulumu"
    echo "    • Docker servisi etkinleştirilip başlatılır"
    [ "$(id -u)" != 0 ] && echo "    • Kullanıcın ('$(id -un)') 'docker' grubuna eklenir"
    [ -n "$SUDO" ] && echo "    • Paket kurulumları 'sudo' ile yapılır"
    echo
    ask_yesno "Devam edilsin mi?" e || { echo "  İptal edildi." >&2; exit 1; }
  fi

  ensure_basic_tools || exit 1

  if ! have docker; then
    install_docker_engine || { echo "  Docker kurulumu başarısız oldu." >&2; exit 1; }
  fi
  have docker || { echo "  Docker kurulduktan sonra da bulunamadı (PATH?)." >&2; exit 1; }

  enable_docker_service
  install_compose_plugin
  ensure_docker_access

  if ! $DK info >/dev/null 2>&1; then
    echo >&2
    echo "  Docker daemon'a erişilemiyor." >&2
    if [ "$NEED_RELOGIN" = 1 ]; then
      echo "  Oturumu kapatıp açın (grup üyeliği) veya: newgrp docker — sonra tekrar deneyin." >&2
    else
      echo "  'systemctl status docker' ile kontrol edin." >&2
    fi
    exit 1
  fi
  if ! $DK compose version >/dev/null 2>&1; then
    echo "  Docker Compose eklentisi kurulamadı. Elle: https://docs.docker.com/compose/install/" >&2
    exit 1
  fi

  echo "  ✓ Docker $($DK version --format '{{.Server.Version}}' 2>/dev/null) + Compose $($DK compose version --short 2>/dev/null) hazır."
  if [ "$NEED_RELOGIN" = 1 ]; then
    echo "    (Bu kurulum 'sudo' ile sürüyor. Sudosuz kullanım için bir kez çıkış/giriş yapın.)"
  fi
  PREREQS_READY=1
}

# Etkin bir güvenlik duvarı varsa domain modunun portlarını aç (best-effort).
open_firewall_ports() {
  _hp=${1:-80}
  _sp=${2:-443}
  elevate 2>/dev/null || return 0
  if have ufw && run_root ufw status 2>/dev/null | grep -qi "Status: active"; then
    run_root ufw allow "$_hp/tcp" >/dev/null 2>&1 || true
    run_root ufw allow "$_sp/tcp" >/dev/null 2>&1 || true
    echo "  Güvenlik duvarı (ufw): $_hp/tcp, $_sp/tcp açıldı."
  elif have firewall-cmd && run_root firewall-cmd --state >/dev/null 2>&1; then
    run_root firewall-cmd --permanent --add-port="$_hp/tcp" >/dev/null 2>&1 || true
    run_root firewall-cmd --permanent --add-port="$_sp/tcp" >/dev/null 2>&1 || true
    run_root firewall-cmd --reload >/dev/null 2>&1 || true
    echo "  Güvenlik duvarı (firewalld): $_hp/tcp, $_sp/tcp açıldı."
  fi
}

bootstrap() {
  banner "Ortam Kurulumu"
  ensure_prereqs
  echo
  echo "  Hazır. Şimdi: ./deploy.sh install"
}

usage() {
  cat >&2 <<'EOF'
Kullanım: ./deploy.sh <komut> [seçenekler]

Komutlar:
  install                 Adım adım kurulum sihirbazı: domainli mi domainsiz mi,
                          alan adı, e-posta, port — hepsi tek tek sorulur.
                          Domainli modda sunucunun KENDİ nginx'i yapılandırılır
                          (yoksa kurulur) ve Let's Encrypt ile otomatik HTTPS
                          alınır; frontend/api yalnız 127.0.0.1'e bağlanır.
  bootstrap               Sunucuyu hazırla: OS'u algıla, Docker + Docker Compose
                          kur/başlat, kullanıcıyı docker grubuna ekle.
                          (install bunu zaten otomatik çağırır.)
  rebuild [--no-cache] [--skip-backup]
                          Görüntüleri yeniden derleyip servisleri yeniden başlatır.
                          Önce mevcut sürümü kaydeder ve doğrulanmış bir yedek
                          alır; migration, servis başlatma veya sağlık kontrolü
                          başarısız olursa önceki sürüme döner.
                          Güncellemek için:  git pull && ./deploy.sh rebuild
  status                  Servis durumu + sağlık kontrolü.
  restart                 Servisleri yeniden başlat (derleme/migration/yedek yok).
                          Geri yüklemeden sonra gereken budur; rebuild değil.
  doctor                  Ön koşul ve ortam denetimi.
  repair-app-role         varyaone_app rolünün parolasını ve yetkilerini yeniden uygular
                          (majör PostgreSQL yükseltmesi sonrası gerekebilir).
  renew-cert [--force]    Bu kurulumun Let's Encrypt sertifikasını yenile.
                          Varsayılan: yalnız vadesi geldiyse. --force zorlar.
                          Sunucudaki diğer sertifikalara dokunulmaz.
  backup                  Tam sistem yedeği al: backups/ altına tek .varya dosyası
                          (veritabanı + yüklenen dosyalar).
  restore <dosya.varya> --confirm [--force] [--skip-safety-backup]
                          .varya yedeğinden tam sistemi geri yükle. Önce mevcut
                          durumun doğrulanmış güvenlik yedeği alınır; alınamazsa
                          geri yükleme başlamaz.
                          --skip-safety-backup yalnız mevcut kurulum zaten
                          kurtarılamaz durumdaysa, geri dönüş noktası olmadan
                          devam etmeyi kabul eder.
  system-status           Yarıda kalmış bir sistem işlemi var mı? Servislerin
                          açılabilir olup olmadığını söyler.
  system-history [id]     Bir sistem işleminin adım adım kaydı.
  system-resolve "<not>"  Yarıda kalmış işlemi elle incelenip düzeltildi olarak
                          işaretle ve servisleri aç. Not zorunludur.
  prune-backups [--dry-run]
                          Saklama süresini geçmiş .varya dosyalarını sil.
                          En yeni birkaç yedek ve geri dönüş noktaları korunur.
  recovery-bundle <dizin> Yeni bir sunucuda sıfırdan kurtarmak için gereken her
                          şeyi tek dizinde toplar: doğrulanmış yedek, kurulum
                          bilgisi, ana anahtar (ayrı dosyada) ve adımlar.
  system-retained         Geri yüklemelerden saklanan eski veritabanları.
  system-prune [--dry-run]
                          Saklama süresini geçmiş eski veritabanlarını sil.
                          En yenisi her zaman korunur.
  system-promote <db> --confirm
                          Saklanan bir veritabanını yeniden canlıya al
                          (geri yüklemeden geri dönüş). Önce yedek alınır.
  uninstall --confirm [--keep-backups] [--purge] [--yes]
                          Her şeyi kaldır: konteynerler, volume'ler (VERİTABANI
                          DAHİL), derlenen image'lar, ağlar, üretilen .env ve
                          host nginx yapılandırması (Let's Encrypt sertifikaları
                          /etc/letsencrypt'te bırakılır).
                          --keep-backups verilmezse backups/ de silinir.
                          --purge ayrıca proje dizininin kendisini de siler
                          (hiçbir iz bırakmaz). --keep-backups ile birlikte
                          kullanılamaz: backups/ proje dizininin içindedir.
EOF
  exit 2
}

require_docker() {
  if $DK info >/dev/null 2>&1 && $DK compose version >/dev/null 2>&1; then
    return 0
  fi
  # sudo ile erişilebiliyorsa ona geç
  if [ "$(id -u)" != 0 ] && have sudo && sudo docker info >/dev/null 2>&1; then
    DK="sudo docker"
    sudo docker compose version >/dev/null 2>&1 && return 0
  fi
  echo "Docker / Docker Compose hazır değil. Şunu çalıştırın: ./deploy.sh bootstrap" >&2
  exit 1
}

# --- .env yardımcıları --------------------------------------------------------

# .env okuma/yazma.
#
# Compose'un dotenv ayrıştırıcısı ile aynı dosyayı okuyoruz, bu yüzden okuma
# tarafı onun kurallarına yaklaştırılır: son tanım kazanır, CRLF satır sonları
# temizlenir, değerin çevresindeki tırnaklar kaldırılır. Bunlar yapılmazsa
# betiğin gördüğü değer ile Compose'un kullandığı değer sessizce ayrışır —
# ve ayrıştıkları yer genelde bir parola olur.
env_get() {
  [ -f .env ] || return 0
  _v=$(grep "^$1=" .env 2>/dev/null | tail -n1 | cut -d= -f2- | tr -d '\r')
  # Çevreleyen tırnakları soy (Compose da soyar).
  _v=$(printf '%s' "$_v" | sed -e 's/^"\(.*\)"$/\1/' -e "s/^'\(.*\)'\$/\1/")
  printf '%s' "$_v"
}

# Tek anahtar yaz.
env_set() {
  env_set_many "$1=$2"
}

# Birden çok anahtarı TEK atomik işlemle yaz: env_set_many KEY=VAL [KEY=VAL...]
#
# Tek tek yazmak her anahtar için ayrı ayrı atomiktir, ama birlikte değildir:
# ikisinin arasında kesilen bir kurulum, parolası yazılmış ama DSN'i yazılmamış
# bir .env bırakır — yani hiçbir zaman var olmamış bir yapılandırma.
env_set_many() {
  [ $# -gt 0 ] || return 0
  touch "$project_dir/.env"
  chmod 600 "$project_dir/.env" 2>/dev/null || true
  _tmp=$(mktemp "$project_dir/.env.tmp.XXXXXX") || {
    echo "env_set: geçici dosya oluşturulamadı" >&2; return 1
  }
  chmod 600 "$_tmp" 2>/dev/null || true

  _filter="$_tmp.keys"
  for _pair in "$@"; do printf '%s\n' "^${_pair%%=*}="; done > "$_filter" || {
    rm -f "$_tmp" "$_filter"; return 1
  }
  if [ -s "$project_dir/.env" ]; then
    # grep, eşleşmeyen satır kalmadığında da 1 döner; bu bir hata değildir.
    # Gerçek okuma hatası 2'dir. Hepsini `|| true` ile yutmak, okunamayan bir
    # .env'i sessizce birkaç satıra indirmek demekti.
    _grc=0; grep -v -f "$_filter" "$project_dir/.env" > "$_tmp" 2>/dev/null || _grc=$?
    if [ "$_grc" -gt 1 ]; then
      rm -f "$_tmp" "$_filter"
      echo "env_set: .env okunamadı" >&2
      return 1
    fi
  fi
  rm -f "$_filter"
  for _pair in "$@"; do
    printf '%s=%s\n' "${_pair%%=*}" "${_pair#*=}" >> "$_tmp" || {
      rm -f "$_tmp"; echo "env_set: yazılamadı" >&2; return 1
    }
  done
  # Rename'den önce diske indir; sonra da dizini. Aksi halde elektrik kesintisi
  # yeni içeriği de eski adı da kaybettirebilir.
  have sync && { sync "$_tmp" 2>/dev/null || sync; }
  mv -f "$_tmp" "$project_dir/.env" || { rm -f "$_tmp"; return 1; }
  have sync && { sync "$project_dir" 2>/dev/null || sync; }
  return 0
}

# Domainli mod (host nginx + Let's Encrypt) .env içinde VARYAONE_DOMAIN dolu ise
# etkindir. Bu modda frontend/api yalnız 127.0.0.1'e bağlıdır ve dışarıya bakan
# tek şey sunucunun kendi nginx'idir.
domain_mode() {
  [ -n "$(env_get VARYAONE_DOMAIN)" ]
}

# `docker compose` çağrısı. Domainli modda ters proxy artık host nginx'tir;
# ekstra compose override yok.
compose() {
  $DK compose "$@"
}

# Superuser olmayan varyaone_app rolünü kurar ve VARYAONE_APP_DATABASE_URL'i
# .env'e yazar; böylece sunucu/worker bu rolden bağlanır ve firma izolasyonu
# row-level-security ile veritabanı seviyesinde zorlanır.
#
# postgres ayakta ve migration'lar (000148 rolü NOLOGIN olarak oluşturur)
# uygulanmış olmalı. İdempotent: parola varsa yeniden kullanılır.
#
# Başarısız olursa UYARI verir ve döner (return 1): sistem superuser bağlantısıyla
# çalışmaya devam eder (izolasyon yalnızca uygulama yüklemine dayanır), kurulum
# yarıda kalmaz.
ensure_app_role() {
  _pguser=$(env_get POSTGRES_USER); _pguser=${_pguser:-varyaone}
  _pgdb=$(env_get POSTGRES_DB); _pgdb=${_pgdb:-varyaone}

  if ! compose exec -T postgres pg_isready -U "$_pguser" -d "$_pgdb" >/dev/null 2>&1; then
    echo "  ! varyaone_app rolü kurulamadı: postgres hazır değil. Superuser bağlantısıyla devam ediliyor." >&2
    return 1
  fi

  _apppw=$(env_get VARYAONE_APP_DB_PASSWORD)
  if [ -z "$_apppw" ]; then
    if have openssl; then
      _apppw=$(openssl rand -hex 24)
    else
      _apppw=$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')
    fi
  fi

  # Komutu bir string'e koyup genişletmek, kullanıcı adı ya da veritabanı adı
  # boşluk içerdiğinde argüman sınırlarını kaybettirir. Argümanlar tek tek
  # geçirilir.
  if ! compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$_pguser" -d "$_pgdb" \
       -f - < "$project_dir/internal/platform/migrations/app_role.sql" >/dev/null 2>&1; then
    echo "  ! varyaone_app grant'leri uygulanamadı." >&2
    return 1
  fi
  # Parola SQL metnine gömülmez: psql değişkeni olarak geçirilir, :'apppw'
  # psql tarafında SQL literali olarak kaçırılır. Parola .env'den gelmiş
  # olabilir ve hex olduğu garanti değildir; içindeki tek tırnak, SQL'i
  # operatörün beklemediği bir şeye çevirirdi.
  #
  # :'var' interpolasyonu psql'in kendi script okuyucusunda olur — `-c` ile
  # verilen komutlarda ÇALIŞMAZ (psql onları yorumlamadan sunucuya geçirir),
  # bu yüzden ifade stdin'den, grants dosyasıyla aynı `-f -` deseniyle
  # verilir; tek bir exec, iki bağımsız execi pipe'lamanın kırılganlığı da
  # (ikinci uç boş girdi alıp hiçbir şey yapmadan "başarılı" dönebiliyordu)
  # böylece ortadan kalkar.
  if ! printf "ALTER ROLE varyaone_app LOGIN PASSWORD :'apppw';\n" | \
       compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$_pguser" -d "$_pgdb" \
       -v apppw="$_apppw" -f - \
       >/dev/null 2>&1; then
    echo "  ! varyaone_app rolüne parola verilemedi." >&2
    return 1
  fi

  # Parola ve DSN birlikte yazılır: biri olmadan diğeri işe yaramaz.
  if ! env_set_many \
      "VARYAONE_APP_DB_PASSWORD=$_apppw" \
      "VARYAONE_APP_DATABASE_URL=postgres://varyaone_app:$(url_encode "$_apppw")@postgres:5432/$_pgdb?sslmode=disable"; then
    echo "  ! varyaone_app yapılandırması .env'e yazılamadı." >&2
    return 1
  fi

  # Yazdığımız DSN gerçekten çalışıyor mu? "Rol kuruldu" demek, o rolle
  # bağlanılabildiğini kanıtlamaz — ve kanıtlanmadığında belirti çok sonra,
  # "api hazır olmuyor" olarak ortaya çıkar.
  if ! compose exec -T postgres env PGPASSWORD="$_apppw" \
       psql -h 127.0.0.1 -U varyaone_app -d "$_pgdb" -c 'SELECT 1' >/dev/null 2>&1; then
    echo "  ! varyaone_app ile bağlanılamadı; yapılandırma yazıldı ama çalışmıyor." >&2
    return 1
  fi

  echo "  ✓ varyaone_app rolü etkin — firma izolasyonu veritabanı seviyesinde zorlanıyor."
  return 0
}

# DSN içine gömülecek parolayı yüzde kodla. Rastgele hex için gereksiz, ama
# parola .env'den gelebilir ve içindeki @ ya da / DSN'i sessizce başka bir
# sunucuya veya veritabanına işaret eder hale getirir.
url_encode() {
  printf '%s' "$1" | od -An -tx1 -v | tr -s ' ' '\n' | while read -r _b; do
    [ -n "$_b" ] || continue
    _c=$(printf "\\$(printf '%03o' "0x$_b")")
    case "$_c" in
      [a-zA-Z0-9.~_-]) printf '%s' "$_c" ;;
      *) printf '%%%s' "$_b" ;;
    esac
  done
}

# Yığını doğru sırada ayağa kaldır.
#
# Sıra: postgres -> migration -> uygulama rolü + giriş testi -> api/worker/frontend.
#
# Eskiden `compose up --build -d` her şeyi birden başlatıyor, rol ise sonradan
# kuruluyordu: api ve worker, kendilerine ait rol henüz yokken açılıyordu.
# Compose'un bağımlılık koşulları migration'ın bittiğini garanti eder, rolün
# hazır olduğunu değil.
#
# Rol kurulamazsa sessizce superuser bağlantısıyla devam EDİLMEZ. Bu, firma
# izolasyonunu veritabanı seviyesinden uygulama seviyesine düşürmek demektir ve
# bunun bilmeden olması, olmasından daha kötüdür. Bilinçli tercih için
# VARYAONE_ALLOW_SUPERUSER_FALLBACK=1.
bring_up_stack() {
  _build=${1:-}

  echo "  postgres başlatılıyor..."
  compose up -d $_build postgres >/dev/null 2>&1 || {
    echo "  postgres başlatılamadı." >&2; return 1
  }
  ensure_postgres_up || { echo "  postgres hazır olmadı." >&2; return 1; }

  oplog "aşama: migration"
  echo "  Migration uygulanıyor..."
  compose run --rm --no-deps $_build migrate || {
    echo "  Migration başarısız." >&2; return 1
  }

  echo "  Uygulama rolü hazırlanıyor..."
  if ! ensure_app_role; then
    if [ "${VARYAONE_ALLOW_SUPERUSER_FALLBACK:-0}" = 1 ]; then
      echo "  UYARI: superuser bağlantısıyla devam ediliyor (bilinçli tercih)." >&2
      echo "  Firma izolasyonu artık yalnız uygulama katmanında." >&2
      env_set VARYAONE_APP_DATABASE_URL ""
    else
      echo "  Uygulama rolü kurulamadı; servisler başlatılmadı." >&2
      echo "  Onarmak için:  ./deploy.sh repair-app-role" >&2
      echo "  Bilinçli olarak superuser ile devam etmek için:" >&2
      echo "    VARYAONE_ALLOW_SUPERUSER_FALLBACK=1 ./deploy.sh install" >&2
      return 1
    fi
  fi

  echo "  Servisler başlatılıyor..."
  compose up -d $_build || { echo "  Servisler başlatılamadı." >&2; return 1; }
  return 0
}

# Servisleri yeniden başlat. Yeniden derleme yok, migration yok, yedek yok.
#
# Geri yüklemeden sonra gereken şey budur, `rebuild` değil. rebuild image'ları
# yeniden derler, deploy öncesi yedek alır, migration çalıştırır ve sürüm kaydı
# tutar — hepsi bir DEPLOY için doğru, ama kodun hiç değişmediği bir durumda
# dakikalar süren gereksiz iş. Ayrıca gereksiz risk: her migration çalıştırması,
# çalıştırılmasa hiç olmayacak bir hata ihtimalidir.
#
# Yeniden başlatma zaten zorunlu değil (geri yükleme sonrası havuz kendiliğinden
# geri yüklenen veritabanına bağlanır); bu komut yalnız temiz bir başlangıç ister.
restart_services() {
  require_docker
  [ -f .env ] || { echo ".env yok; önce ./deploy.sh install çalıştırın." >&2; exit 1; }
  oplog_start restart
  acquire_lock update
  trap 'release_lock' EXIT INT TERM

  # Yarıda kalmış bir işlem varsa servisleri açma.
  ensure_postgres_up >/dev/null 2>&1
  _ss=0; system_serviceable || _ss=$?
  case "$_ss" in
    1)
      echo "Yarıda kalmış bir sistem işlemi var; servisler açılmıyor." >&2
      echo "  ${SYSTEM_STATUS_REASON:-}" >&2
      echo "  ./deploy.sh system-status" >&2
      exit 3
      ;;
  esac

  echo "Servisler yeniden başlatılıyor..."
  compose up -d --force-recreate api worker frontend >/dev/null 2>&1 || {
    oplog "sonuç: başarısız (up)"
    echo "Servisler başlatılamadı; ./deploy.sh doctor ile bakın." >&2
    exit 1
  }
  if wait_healthy 120; then
    reload_nginx_soft
    oplog "sonuç: başarılı"
    echo "Tamamlandı."
  else
    oplog "sonuç: sağlık kontrolü geçmedi"
    echo "Servisler açıldı ama sağlık kontrolü geçmedi; ./deploy.sh doctor ile bakın." >&2
    exit 1
  fi
}

# Rolu elle onarir: grant'leri yeniden uygular, parolayi yeniden verir ve
# .env'deki DSN'i tazeler. Bir majör PostgreSQL yükseltmesinden veya yarida
# kesilmis bir guncellemeden sonra rol parolasiz kalirsa tek gereken budur.
repair_app_role() {
  require_docker
  [ -f .env ] || { echo ".env yok; önce ./deploy.sh install çalıştırın." >&2; exit 1; }
  if ensure_app_role; then
    compose up -d api worker >/dev/null 2>&1 || true
    echo "  Servisler yeniden başlatıldı. Durum: ./deploy.sh status"
  else
    echo "  Rol onarılamadı. Sistemi ayakta tutmak için .env içindeki" >&2
    echo "  VARYAONE_APP_DATABASE_URL satırını boşaltıp servisleri yeniden başlatabilirsiniz." >&2
    exit 1
  fi
}

# --- işlem günlüğü ----------------------------------------------------------
# Her yıkıcı işlem kendi 0600 günlüğüne yazar. Bir hata sonrası sorulacak soru
# "hangi adım, neden bozuldu" olduğunda, terminale kayıp gitmiş bir stderr
# satırından başka bir yerde cevap bulunması gerekir.
#
# Parola ve token benzeri değerler redakte edilir: günlük dosyası .env kadar
# gizli tutulmaz ve yanlışlıkla paylaşılması muhtemeldir.
OPLOG=""

oplog_start() {
  mkdir -p "$project_dir/deploy/logs" 2>/dev/null || return 0
  chmod 700 "$project_dir/deploy/logs" 2>/dev/null || true
  OPLOG="$project_dir/deploy/logs/$1-$(date -u +%Y%m%dT%H%M%SZ)-$$.log"
  : > "$OPLOG" 2>/dev/null || { OPLOG=""; return 0; }
  chmod 600 "$OPLOG" 2>/dev/null || true
  oplog "işlem başladı: $1 ($*)"
  # Eski günlükleri buda.
  ls -1t "$project_dir/deploy/logs" 2>/dev/null | tail -n +51 | while read -r _old; do
    [ -n "$_old" ] && rm -f "$project_dir/deploy/logs/$_old"
  done
}

oplog() {
  [ -n "$OPLOG" ] || return 0
  printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$(redact "$*")" >> "$OPLOG" 2>/dev/null || true
}

# Parola/anahtar/token görünümlü değerleri maskele.
redact() {
  printf '%s' "$*" | sed -E \
    -e 's#(://[^:/@]+:)[^@]+@#\1***@#g' \
    -e 's/(PASSWORD|PASSWD|SECRET|TOKEN|KEY)=[^ ]*/\1=***/gi'
}

# --- tekil çalıştırma kilidi -------------------------------------------------
# Host tarafındaki işlemler (nginx yapılandırması, .env yazımı, Compose çağrıları)
# için kilit. Veritabanı ve depolama üzerindeki asıl koordinasyon konteyner
# içindeki işlem koordinatörüne aittir (varyaone system status); bu kilit onun
# yerine geçmez, host kaynaklarını korur.
#
# Kimlik yalnız PID değildir. PID yeniden kullanılır, `kill -0` başka kullanıcıya
# ait bir süreçte izin reddi verir (ölü sanılır) ve PID dosyası sahibinden sonra
# da yaşar. Kilit kimliği bu yüzden PID + sürecin başlangıç anı + boot kimliğidir:
# üçü birden eşleşmiyorsa o kilit başka bir sürece aittir ya da makine o kilitten
# beri yeniden başlamıştır.
LOCK_DIR=""
LOCK_HELD=0

# Bu makinenin açılış kimliği. Yoksa boş: o zaman devralma daha temkinli davranır.
_boot_id() {
  if [ -r /proc/sys/kernel/random/boot_id ]; then
    cat /proc/sys/kernel/random/boot_id 2>/dev/null && return 0
  fi
  # Linux dışı: açılış zamanını kimlik olarak kullan.
  if have sysctl; then
    sysctl -n kern.boottime 2>/dev/null && return 0
  fi
  echo ""
}

# <pid>'in başlangıç anı (jiffies / saniye). Boş = belirlenemedi.
_proc_start() {
  if [ -r "/proc/$1/stat" ]; then
    # 22. alan starttime. Komut adı boşluk içerebildiği için ')' sonrasından say.
    sed 's/.*) //' "/proc/$1/stat" 2>/dev/null | awk '{print $20}'
    return 0
  fi
  ps -o lstart= -p "$1" 2>/dev/null | tr -s ' ' || echo ""
}

_write_lock_owner() {
  {
    printf 'pid=%s\n' "$$"
    printf 'boot=%s\n' "$(_boot_id)"
    printf 'start=%s\n' "$(_proc_start $$)"
  } > "$1/owner" 2>/dev/null || true
}

_lock_field() { sed -n "s/^$2=//p" "$1/owner" 2>/dev/null | head -n1; }

# Kilidin sahibi gerçekten ölmüş mü? 0 = ölmüş (devralınabilir), 1 = belirsiz/yaşıyor.
_lock_owner_dead() {
  _d=$1
  _opid=$(_lock_field "$_d" pid)
  _oboot=$(_lock_field "$_d" boot)
  _ostart=$(_lock_field "$_d" start)
  [ -n "$_opid" ] || return 1          # kimliksiz kilit: devralma.
  _nboot=$(_boot_id)
  if [ -n "$_oboot" ] && [ -n "$_nboot" ] && [ "$_oboot" != "$_nboot" ]; then
    return 0                            # makine yeniden başlamış: sahip kesin ölü.
  fi
  if kill -0 "$_opid" 2>/dev/null; then
    # Süreç var. Aynı süreç mi, yoksa PID yeniden mi kullanılmış?
    _nstart=$(_proc_start "$_opid")
    if [ -n "$_ostart" ] && [ -n "$_nstart" ] && [ "$_ostart" != "$_nstart" ]; then
      return 0                          # PID yeniden kullanılmış: eski sahip ölü.
    fi
    return 1                            # yaşıyor.
  fi
  # kill -0 başarısız: ölü mü, yoksa izin mi reddedildi? İkisi aynı şey değil.
  if kill -0 "$_opid" 2>&1 | grep -qi 'permitted\|denied'; then
    return 1                            # başka kullanıcının süreci: yaşıyor say.
  fi
  return 0
}

acquire_lock() {
  _name=${1:-deploy}
  _dir="$project_dir/deploy/.lock-$_name"
  _waited=0
  _limit=${LOCK_WAIT_SECONDS:-0}
  _stolen=0
  while :; do
    if mkdir "$_dir" 2>/dev/null; then
      LOCK_DIR="$_dir"; LOCK_HELD=1
      _write_lock_owner "$_dir"
      # Çağıran, release_lock'u bir trap ile çağırmaktan sorumludur.
      return 0
    fi
    if _lock_owner_dead "$_dir"; then
      # Yalnız bir kez devral. İki temizleyici yarışırsa ikincisi beklesin;
      # aksi halde ikisi de silip ikisi de kilidi aldığını sanabilir.
      if [ "$_stolen" = 0 ]; then
        _stolen=1
        echo "  Uyarı: sahipsiz kilit kaldırılıyor ($(_lock_field "$_dir" pid))." >&2
        rm -rf "$_dir" 2>/dev/null || true
        continue
      fi
    fi
    if [ "$_waited" -ge "$_limit" ]; then
      echo "Başka bir işlem çalışıyor (kilit: $_dir, pid: $(_lock_field "$_dir" pid))." >&2
      echo "Bitmesini bekleyin; sürmüyorsa: rm -rf \"$_dir\"" >&2
      exit 1
    fi
    sleep 3; _waited=$((_waited + 3))
  done
}

release_lock() {
  [ -n "${VARYAONE_SELF_COPY:-}" ] && rm -f "$VARYAONE_SELF_COPY" 2>/dev/null || true
  [ "$LOCK_HELD" = 1 ] || return 0
  LOCK_HELD=0
  [ -n "$LOCK_DIR" ] && rm -rf "$LOCK_DIR" 2>/dev/null || true
}

published_port() {
  service=$1
  container_port=$2
  fallback=$3
  resolved=$(compose port "$service" "$container_port" 2>/dev/null | awk -F: 'NR == 1 { print $NF }')
  if [ -n "$resolved" ]; then
    echo "$resolved"
  else
    echo "$fallback"
  fi
}

generate_master_key() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -base64 32 | tr -d '\n'
  else
    od -An -N32 -tx1 /dev/urandom | tr -d ' \n' | xxd -r -p | base64 | tr -d '\n'
  fi
}

generate_hex_token() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 32
  else
    od -An -N32 -tx1 /dev/urandom | tr -d ' \n'
  fi
}

create_env() {
  umask 077
  if [ -f .env ]; then
    if ! grep -q '^VARYAONE_MASTER_KEY=' .env; then
      echo "VARYAONE_MASTER_KEY=$(generate_master_key)" >> .env
      echo ".env için şifreleme anahtarı oluşturuldu. Bu anahtarı güvenli biçimde yedekleyin."
    elif grep -q '^VARYAONE_MASTER_KEY=replace-' .env; then
      master_key=$(generate_master_key)
      sed -i "s|^VARYAONE_MASTER_KEY=.*|VARYAONE_MASTER_KEY=$master_key|" .env
      echo ".env içindeki örnek şifreleme anahtarı güvenli bir anahtarla değiştirildi."
    fi
    return 0
  fi
  if command -v openssl >/dev/null 2>&1; then
    password=$(openssl rand -hex 24)
  else
    password=$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')
  fi
  master_key=$(generate_master_key)
  sed "s|POSTGRES_PASSWORD=change-me|POSTGRES_PASSWORD=$password|; s|VARYAONE_ENV=development|VARYAONE_ENV=production|; s|VARYAONE_MASTER_KEY=replace-with-base64-encoded-32-byte-key|VARYAONE_MASTER_KEY=$master_key|" .env.example > .env
  echo ".env güvenli bir rastgele parola ve şifreleme anahtarı ile oluşturuldu."
}

# --- host nginx / certbot ---------------------------------------------------
#
# Domainli modda ters proxy sunucunun KENDİ nginx'idir (Docker'da nginx yok).
# deploy.sh bir server bloğu üretir, /etc/nginx altına bağlar, çakışma
# kontrolü yapar ve host certbot ile webroot üzerinden sertifika alır.

# nginx_conf_paths — dağıtıma göre yazılacak/etkinleştirilecek yolları belirler:
#   NGINX_SITE_FILE   : server bloğunun yazıldığı dosya
#   NGINX_SITE_LINK   : etkin dizindeki bağlantı (conf.d'de dosyanın kendisi)
NGINX_SITE_FILE=""; NGINX_SITE_LINK=""
nginx_conf_paths() {
  if [ -d /etc/nginx/sites-available ] && [ -d /etc/nginx/sites-enabled ]; then
    NGINX_SITE_FILE=/etc/nginx/sites-available/varyaone
    NGINX_SITE_LINK=/etc/nginx/sites-enabled/varyaone
  else
    NGINX_SITE_FILE=/etc/nginx/conf.d/varyaone.conf
    NGINX_SITE_LINK=$NGINX_SITE_FILE
  fi
}

# Host nginx'i doğrula ve yeniden yükle. `nginx -t` geçmezse reload etmez.
nginx_reload() {
  if ! run_root nginx -t >/dev/null 2>&1; then
    echo "  nginx yapılandırması geçersiz:" >&2
    run_root nginx -t >&2 || true
    return 1
  fi
  # reload, çalışmıyorsa start/restart — hangisi tutarsa.
  run_root systemctl reload nginx >/dev/null 2>&1 \
    || run_root systemctl restart nginx >/dev/null 2>&1 \
    || run_root nginx -s reload >/dev/null 2>&1 \
    || run_root nginx >/dev/null 2>&1 \
    || run_root rc-service nginx restart >/dev/null 2>&1 \
    || run_root service nginx restart >/dev/null 2>&1
}

# nginx -T çıktısında bir default_server tanımı var mı? (kendi bloğumuz
# configure_host_nginx tarafından bu kontrolden önce kaldırılır.)
nginx_has_foreign_default_server() {
  run_root nginx -T 2>/dev/null \
    | grep -Eq '[[:space:]]default_server([[:space:];]|$)'
}

# Bu alan adı BAŞKA bir server bloğunda mı tanımlı?
#
# Kendi bloğumuz hariç tutulur. Eskiden bu ayrım, kontrolden önce kendi bloğunu
# gerçekten silerek yapılıyordu — yani soruyu sormanın bedeli, cevap "evet" ise
# çalışan siteyi kaybetmekti. `nginx -T` çıktısında yapılandırma dosyaları
# "# configuration file <yol>:" satırlarıyla ayrılır; kendi dosyamıza ait blok
# bu satırlara bakılarak atlanır.
nginx_domain_already_served_elsewhere() {
  _d=$(printf '%s' "$1" | sed 's/[.]/\\./g')
  run_root nginx -T 2>/dev/null | awk -v ours="$NGINX_SITE_FILE" -v link="$NGINX_SITE_LINK" -v pat="$_d" '
    /^# configuration file / { file = $4; sub(/:$/, "", file); next }
    file == ours || file == link { next }
    $0 ~ ("server_name[^;]*[[:space:]]" pat "([[:space:];]|$)") { found = 1 }
    END { exit found ? 0 : 1 }
  '
}

# Ortak proxy gövdesi — tüm 443 blokları paylaşır.
#
# Yedekleme uçları ayrı bir location alır. Nedeni, tek bir ayar takımının iki
# farklı iş için doğru olamamasıdır:
#
#   - proxy_read_timeout ARDIŞIK OKUMALAR ARASINDAKİ sessizliği ölçer, işin
#     toplam süresini değil. Normal API için 600 saniye fazlasıyla yeterlidir;
#     saatler sürebilen bir geri yükleme içinse tek bir sessiz aralık bile
#     bağlantıyı koparmaya yeter. Bu yüzden yalnız o rotada kaldırılır.
#   - nginx varsayılan olarak istek gövdesini tamamen diske yazıp sonra
#     upstream'e gönderir. 8 GiB'lik bir yedek için bu, nginx'in geçici
#     diskinde ikinci bir tam kopya demektir; request buffering kapatılır ve
#     akış doğrudan geçer.
#   - Yedek yanıtı bütün kurulumun verisidir; hiçbir ara katman
#     önbelleklememelidir.
_nginx_proxy_body() {
  cat <<EOF
    add_header X-Robots-Tag "noindex, nofollow" always;

    # .varya tam sistem yedeği geri yükleme için büyük gövde sınırı.
    # API'nin kendi dosya sınırı 8 GiB; gövde sınırı multipart başlıkları için
    # biraz daha geniştir, yoksa tam 8 GiB'lik bir dosya bu katmanda reddedilir.
    client_max_body_size ${VARYAONE_NGINX_BODY_LIMIT:-9g};
    proxy_read_timeout 600s;
    proxy_send_timeout 600s;

    # SvelteKit/adapter-node yanıt başlıkları (CSP + Set-Cookie) nginx'in
    # varsayılan 4k/8k proxy buffer'ını aşabiliyor -> 502. Buffer'ları genişlet.
    proxy_buffer_size 16k;
    proxy_buffers 8 16k;
    proxy_busy_buffers_size 32k;

    location /api/v1/system/ {
        proxy_pass http://127.0.0.1:${WEB_PORT};
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;

        # Uzun ve sessiz olabilen işlemler: yedek hazırlanırken ilk bayt
        # dakikalar sonra gelebilir, geri yükleme saatler sürebilir.
        proxy_read_timeout 24h;
        proxy_send_timeout 24h;
        proxy_request_buffering off;
        proxy_buffering off;

        proxy_cache off;
        add_header Cache-Control "private, no-store, max-age=0" always;
    }

    location / {
        proxy_pass http://127.0.0.1:${WEB_PORT};
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
    }
EOF
}

# render_host_nginx_conf <domain> <bootstrap|full> <www:0|1> <catchall:0|1>
# WEB_PORT ortam değişkeni frontend'in 127.0.0.1 portunu verir.
# Bu alan adı için sertifikaların gerçekte durduğu dizin.
#
# --cert-name ile alınan sertifikalar lineage adı altında (varyaone-<domain>),
# bu değişiklikten önce alınanlar ise alan adının kendi adı altında durur.
# Hangisi varsa o kullanılır: eski kurulumlar yeniden sertifika almak zorunda
# kalmadan çalışmaya devam eder.
cert_live_dir() {
  _d=$1
  _lin=$(cert_lineage "$_d")
  if run_root test -s "/etc/letsencrypt/live/$_lin/fullchain.pem" 2>/dev/null; then
    printf '/etc/letsencrypt/live/%s' "$_lin"
  else
    printf '/etc/letsencrypt/live/%s' "$_d"
  fi
}

render_host_nginx_conf() {
  _domain=$1; _mode=$2; _www=${3:-0}; _catchall=${4:-0}
  CERT_DIR=$(cert_live_dir "$_domain")
  WEB_PORT=${WEB_PORT:-$(env_get VARYAONE_WEB_PORT)}; WEB_PORT=${WEB_PORT:-3000}
  mkdir -p "$NGINX_CONF_DIR"
  _staged="$NGINX_CONF_DIR/${_domain}.conf"
  _names=$_domain
  [ "$_www" = 1 ] && _names="$_domain www.$_domain"

  {
    echo "# Bu dosya deploy.sh tarafından üretildi; elle düzenlemeyin."
    echo "# Yeniden üretmek için: ./deploy.sh install (domainli mod)"
    echo

    # --- port 80: ACME + (full ise) HTTPS'e yönlendirme --------------------
    echo "server {"
    echo "    listen 80;"
    echo "    server_name $_names;"
    echo "    location /.well-known/acme-challenge/ { root $CERTBOT_WEBROOT; }"
    if [ "$_mode" = full ]; then
      echo "    location / { return 301 https://\$host\$request_uri; }"
    else
      echo "    location / { default_type text/plain; return 200 'varyaone: SSL sertifikasi hazirlaniyor'; }"
    fi
    echo "}"

    if [ "$_mode" = full ]; then
    echo

    # --- port 443: www -> apex (yalnız www DNS'i varsa) --------------------
    if [ "$_www" = 1 ]; then
      cat <<EOF
server {
    listen 443 ssl http2;
    server_name www.$_domain;
    ssl_certificate     $CERT_DIR/fullchain.pem;
    ssl_certificate_key $CERT_DIR/privkey.pem;
    return 301 https://$_domain\$request_uri;
}

EOF
    fi

    # --- port 443: asıl uygulama -----------------------------------------
    cat <<EOF
server {
    listen 443 ssl http2;
    server_name $_domain;

    ssl_certificate     $CERT_DIR/fullchain.pem;
    ssl_certificate_key $CERT_DIR/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_prefer_server_ciphers off;
    ssl_session_cache shared:SSL:10m;
    add_header Strict-Transport-Security "max-age=31536000" always;
$(_nginx_proxy_body)
}
EOF

    # --- IP veya bilinmeyen host ile gelen istekleri düşür ---------------
    # Yalnızca başka bir default_server yoksa; aksi halde nginx "duplicate
    # default server" ile başlamaz.
    if [ "$_catchall" = 1 ]; then
      cat <<EOF

server {
    listen 80 default_server;
    listen 443 ssl http2 default_server;
    server_name _;
    ssl_certificate     /etc/nginx/varyaone-dummy.crt;
    ssl_certificate_key /etc/nginx/varyaone-dummy.key;
    return 444;
}
EOF
    fi
    fi
  } > "$_staged"

  run_root mkdir -p "$(dirname "$NGINX_SITE_FILE")"
  run_root cp "$_staged" "$NGINX_SITE_FILE"
  if [ "$NGINX_SITE_LINK" != "$NGINX_SITE_FILE" ] && [ ! -e "$NGINX_SITE_LINK" ]; then
    run_root ln -s "$NGINX_SITE_FILE" "$NGINX_SITE_LINK"
  fi
}

# /etc/nginx/varyaone-dummy.{crt,key} — catch-all 443 default_server için.
ensure_dummy_cert() {
  run_root test -s /etc/nginx/varyaone-dummy.crt && return 0
  run_root openssl req -x509 -nodes -days 3650 -newkey rsa:2048 \
    -keyout /etc/nginx/varyaone-dummy.key \
    -out /etc/nginx/varyaone-dummy.crt -subj "/CN=invalid" >/dev/null 2>&1
}

wait_for_http() {
  url=$1
  i=0
  while [ "$i" -lt 30 ]; do
    if curl --silent --output /dev/null --max-time 3 "$url"; then
      return 0
    fi
    i=$((i + 1))
    sleep 2
  done
  return 1
}

# wait_for_ok <url> [tries] — nginx'ten geçen gerçek istek 2xx/3xx dönene kadar
# bekle. wait_for_http'nin aksine 502/504/000'i başarı saymaz; kurulum
# "hazır" demeden önce yığının gerçekten servis verdiğini doğrulamak için.
wait_for_ok() {
  _url=$1
  _tries=${2:-30}
  _i=0
  _code=000
  while [ "$_i" -lt "$_tries" ]; do
    _code=$(curl -k -s -o /dev/null -w '%{http_code}' --max-time 5 "$_url" 2>/dev/null || echo 000)
    case "$_code" in
      2?? | 3??) return 0 ;;
    esac
    _i=$((_i + 1))
    sleep 2
  done
  echo "$_code"
  return 1
}

# obtain_certificate <domain> <email> <staging:0|1> <www:0|1>
# Host certbot, webroot doğrulaması. Yenileme certbot'un kendi systemd
# timer'ı / cron'u ile olur; --deploy-hook sertifika değişince nginx'i yükler.
# Bu kurulumun sertifika soyu (lineage). --cert-name ile sabitlenir, böylece
# yenileme ve doğrulama yalnız bizim sertifikamıza dokunur: sunucuda başka
# siteler de olabilir ve onların sertifikaları bizim işimiz değildir.
cert_lineage() { printf 'varyaone-%s' "$1"; }

obtain_certificate() {
  _domain=$1; _email=$2; _staging=${3:-0}; _www=${4:-0}
  if ! have certbot; then
    echo "  certbot kuruluyor..."
    case "$PKG" in dnf | yum) pkg_install epel-release >/dev/null 2>&1 || true ;; esac
    pkg_install certbot || { echo "  certbot kurulamadı — elle kurun ve tekrar deneyin." >&2; return 1; }
  fi
  run_root mkdir -p "$CERTBOT_WEBROOT/.well-known/acme-challenge"
  _lineage=$(cert_lineage "$_domain")
  set -- certonly --webroot -w "$CERTBOT_WEBROOT" --cert-name "$_lineage" -d "$_domain"
  [ "$_www" = 1 ] && set -- "$@" -d "www.$_domain"
  set -- "$@" --email "$_email" --agree-tos --no-eff-email \
    --keep-until-expiring --non-interactive --deploy-hook "nginx -s reload"
  [ "$_staging" = 1 ] && set -- "$@" --staging
  echo "  Let's Encrypt sertifikası isteniyor ($_domain)..."
  run_root certbot "$@" || return 1
  env_set VARYAONE_CERT_LINEAGE "$_lineage"
  return 0
}

# Domainli modda konteynerler yeniden oluşturulduktan sonra host nginx'i
# sessizce yeniden yükle (proxy_pass 127.0.0.1 sabit; bu bir emniyet ağı).
reload_nginx_soft() {
  domain_mode || return 0
  have nginx || return 0
  elevate 2>/dev/null || true
  nginx_reload >/dev/null 2>&1 || true
}

# configure_host_nginx <domain> <email> <staging:0|1>
# WEB_PORT ortam değişkeni frontend'in 127.0.0.1 portunu verir.
# --- nginx yapılandırma işlemi ----------------------------------------------
# Kural: çalışan bir yapılandırma, yerine geçecek olan doğrulanana kadar
# kaldırılmaz.
#
# Eski akış tersini yapıyordu: önce kendi bloğunu siler, reload eder, sonra
# sertifika alıp yeni bloğu yazardı. Aradaki her hata — certbot'un başarısız
# olması, yeni config'in `nginx -t`'yi geçmemesi, reload'ın tutmaması —
# çalışan HTTPS'i geride bırakmadan siteyi indiriyordu. Yeniden kurulum,
# çalışan bir siteyi bozabilen bir işlemdi.
#
# Yeni akış: mevcut config yedeklenir, aday ayrı bir dosyada hazırlanır,
# `nginx -t` ile gerçek include ağacında sınanır, ancak geçerse yerine konur,
# reload sonrası gerçekten yanıt verdiği ölçülür ve ölçüm başarısızsa eski
# dosya geri konup yeniden reload edilir.

NGINX_BACKUP=""

# Mevcut server bloğunu kenara al (silme). 0 = yedek alındı ya da dosya yoktu.
nginx_stash_current() {
  NGINX_BACKUP=""
  [ -e "$NGINX_SITE_FILE" ] || return 0
  NGINX_BACKUP="${NGINX_SITE_FILE}.varyaone-backup.$$"
  run_root cp -p "$NGINX_SITE_FILE" "$NGINX_BACKUP" || {
    echo "  Mevcut nginx yapılandırması yedeklenemedi; değişiklik yapılmıyor." >&2
    return 1
  }
  return 0
}

# Kenara alınan yapılandırmayı geri koy ve reload et.
nginx_restore_stashed() {
  [ -n "$NGINX_BACKUP" ] || return 0
  run_root cp -p "$NGINX_BACKUP" "$NGINX_SITE_FILE" 2>/dev/null || return 1
  [ "$NGINX_SITE_LINK" != "$NGINX_SITE_FILE" ] && run_root ln -sf "$NGINX_SITE_FILE" "$NGINX_SITE_LINK"
  nginx_reload >/dev/null 2>&1 || return 1
  return 0
}

nginx_drop_stash() {
  [ -n "$NGINX_BACKUP" ] && run_root rm -f "$NGINX_BACKUP" 2>/dev/null
  NGINX_BACKUP=""
}

# Adayı yerine koy, sınır, reload et, gerçekten yanıt verdiğini ölç; olmazsa
# eskiye dön. <aday dosya> <sağlık probu için host> <mod>
nginx_commit_candidate() {
  _cand=$1; _probe_host=$2; _mode=$3
  run_root cp "$_cand" "$NGINX_SITE_FILE" || return 1
  [ "$NGINX_SITE_LINK" != "$NGINX_SITE_FILE" ] && run_root ln -sf "$NGINX_SITE_FILE" "$NGINX_SITE_LINK"
  if ! nginx_reload; then
    echo "  Yeni nginx yapılandırması yüklenemedi; eskisine dönülüyor." >&2
    nginx_restore_stashed || echo "  UYARI: eski nginx yapılandırması geri konamadı." >&2
    return 1
  fi
  # Reload'ın hata vermemesi, sitenin yanıt verdiğini kanıtlamaz.
  if ! nginx_probe "$_probe_host" "$_mode"; then
    echo "  nginx yüklendi ama site yanıt vermiyor; eskisine dönülüyor." >&2
    nginx_restore_stashed || echo "  UYARI: eski nginx yapılandırması geri konamadı." >&2
    return 1
  fi
  return 0
}

# Site loopback üzerinden yanıt veriyor mu? DNS ve dış erişimden bağımsız.
nginx_probe() {
  _host=$1; _mode=$2
  have curl || return 0
  if [ "$_mode" = full ]; then
    # Sertifika zinciri henüz bu makinede güvenilir olmayabilir (staging,
    # yeni CA); ölçülen şey TLS güveni değil, sunucunun yanıt vermesi.
    _code=$(curl -sk -o /dev/null -w '%{http_code}' --max-time 8 \
      -H "Host: $_host" "https://127.0.0.1/" 2>/dev/null)
  else
    _code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 \
      -H "Host: $_host" "http://127.0.0.1/" 2>/dev/null)
  fi
  case "${_code:-000}" in
    000|5*) return 1 ;;
    *) return 0 ;;
  esac
}

configure_host_nginx() {
  _domain=$1; _email=$2; _staging=${3:-0}

  detect_os
  elevate || { echo "  nginx yapılandırması için root/sudo gerekli." >&2; exit 1; }

  if ! have nginx; then
    echo "  nginx kuruluyor..."
    pkg_install nginx || { echo "  nginx kurulamadı — elle kurup tekrar deneyin." >&2; exit 1; }
    run_root systemctl enable --now nginx >/dev/null 2>&1 \
      || run_root rc-service nginx start >/dev/null 2>&1 || true
  fi

  nginx_conf_paths
  nginx_stash_current || exit 1

  # Çakışma kontrolleri --------------------------------------------------
  # Kendi bloğumuz hâlâ yerinde; "zaten tanımlı mı" sorusunu sorarken onu
  # saymamak için sahiplik işaretine bakılır. Eskiden bunun için blok gerçekten
  # siliniyordu ve kontroller başarısız olursa site silinmiş kalıyordu.
  _catchall=1
  if nginx_has_foreign_default_server; then
    _catchall=0
    echo "  ! Sistemde zaten bir 'default_server' var — IP'yi gizleyen catch-all"
    echo "    bloğu EKLENMEYECEK (nginx 'duplicate default server' verirdi)."
  fi
  if nginx_domain_already_served_elsewhere "$_domain"; then
    echo "  ! '$_domain' zaten başka bir nginx server bloğunda tanımlı." >&2
    echo "    Çakışmayı önlemek için o bloğu kaldırın, sonra ./deploy.sh install." >&2
    nginx_drop_stash
    exit 1
  fi

  # www alt alanı yalnızca DNS'i bu sunucuya çözülüyorsa dahil edilir.
  _www=0
  if resolves_here "www.$_domain"; then
    _www=1
    echo "  ✓ www.$_domain de bu sunucuya işaret ediyor — sertifikaya dahil edilecek."
  fi

  # SELinux: nginx'in 127.0.0.1'e proxy yapabilmesi için.
  if have getenforce && [ "$(getenforce 2>/dev/null)" = Enforcing ]; then
    run_root setsebool -P httpd_can_network_connect 1 >/dev/null 2>&1 || true
  fi

  [ "$_catchall" = 1 ] && ensure_dummy_cert

  open_firewall_ports 80 443

  # Webroot, ACME probundan ÖNCE var olmalı. Eskiden ilk kez certbot
  # fonksiyonunda oluşturuluyordu, dolayısıyla prob her taze kurulumda
  # başarısız oluyor ve uyarı "yine de denenecek" diyerek geçiştiriliyordu.
  run_root mkdir -p "$CERTBOT_WEBROOT/.well-known/acme-challenge"
  run_root chmod 755 "$CERTBOT_WEBROOT" "$CERTBOT_WEBROOT/.well-known" \
    "$CERTBOT_WEBROOT/.well-known/acme-challenge" 2>/dev/null || true

  # 1) HTTP-only aday — ACME doğrulaması yapılabilsin.
  echo "  nginx: HTTP yapılandırması yazılıyor..."
  render_host_nginx_conf "$_domain" bootstrap "$_www" "$_catchall"
  if ! nginx_commit_candidate "$NGINX_CONF_DIR/${_domain}.conf" "$_domain" bootstrap; then
    echo "  HTTP yapılandırması uygulanamadı; mevcut site korundu." >&2
    exit 1
  fi

  # ACME dizini doğru server bloğundan servis ediliyor mu? Gerçek token yolu ve
  # gerçek içerikle ölç: `root $CERTBOT_WEBROOT` tanımı altında
  # /.well-known/acme-challenge/X isteği $CERTBOT_WEBROOT/.well-known/acme-challenge/X
  # dosyasını arar — eski prob dosyayı webroot köküne yazdığı için asla bulunamazdı.
  _probe="varyaone-acme-$(date +%s)-$$"
  _probe_path="$CERTBOT_WEBROOT/.well-known/acme-challenge/$_probe"
  printf 'varyaone-acme-ok' | run_root tee "$_probe_path" >/dev/null 2>&1 || true
  run_root chmod 644 "$_probe_path" 2>/dev/null || true
  _got=$(curl -s --max-time 5 -H "Host: $_domain" \
    "http://127.0.0.1/.well-known/acme-challenge/$_probe" 2>/dev/null)
  run_root rm -f "$_probe_path" 2>/dev/null || true
  if [ "$_got" != "varyaone-acme-ok" ]; then
    echo "  ! nginx 80 portunda ACME dizinini beklendiği gibi sunmuyor" >&2
    echo "    (beklenen içerik gelmedi). Sertifika adımı yine de denenecek." >&2
  fi

  # 2) Sertifika. Başarısız olursa HTTP bloğu ayakta kalır: site HTTP üzerinden
  #    çalışmaya devam eder, hiçbir şey silinmez.
  if ! obtain_certificate "$_domain" "$_email" "$_staging" "$_www"; then
    echo >&2
    echo "  Sertifika alınamadı. Site şimdilik yalnız HTTP: http://$_domain" >&2
    echo "  DNS ve 80/443'ü düzeltip tekrar deneyin: ./deploy.sh install" >&2
    nginx_drop_stash
    exit 1
  fi

  # 3) Tam yapılandırma (443 proxy + yönlendirmeler).
  echo "  nginx: HTTPS yapılandırması yazılıyor..."
  render_host_nginx_conf "$_domain" full "$_www" "$_catchall"
  if ! nginx_commit_candidate "$NGINX_CONF_DIR/${_domain}.conf" "$_domain" full; then
    echo "  HTTPS yapılandırması uygulanamadı; önceki yapılandırmaya dönüldü." >&2
    exit 1
  fi
  nginx_drop_stash
}

# Üretilen host nginx yapılandırmasını kaldır (uninstall).
remove_host_nginx() {
  have nginx || return 0
  elevate 2>/dev/null || return 0
  nginx_conf_paths
  [ "$NGINX_SITE_LINK" != "$NGINX_SITE_FILE" ] && run_root rm -f "$NGINX_SITE_LINK"
  run_root rm -f "$NGINX_SITE_FILE" /etc/nginx/varyaone-dummy.crt /etc/nginx/varyaone-dummy.key
  nginx_reload >/dev/null 2>&1 || true
}

# --- komutlar ---------------------------------------------------------------

# Eski (Docker içi nginx + certbot) domainli kurulumdan kalan konteynerleri
# temizle. Yeni domainli mod host nginx kullanır; bunlar artıktır.
# --- eski (Docker içi) ters proxy'den host nginx'e geçiş --------------------
#
# İki ayrı iş, ve sırası önemli:
#
#   stop_legacy_proxy  — eski konteynerleri DURDURUR. 80/443'ü serbest bırakmak
#                        için gerekli, geri alınabilir, veri silmez.
#   purge_legacy_proxy — eski volume'leri (letsencrypt sertifikaları dahil)
#                        SİLER. Geri alınamaz ve yalnız yeni kurulum sağlık
#                        kontrolünü geçtikten sonra yapılmalıdır.
#
# Eskiden ikisi tek fonksiyondu ve yeni sertifika alınmadan ÖNCE çağrılıyordu:
# certbot başarısız olursa geri dönülecek sertifikalar da gitmiş oluyordu.

stop_legacy_proxy() {
  for _c in nginx certbot; do
    $DK stop "varyaone-${_c}-1" >/dev/null 2>&1 || true
    $DK rm -f "varyaone-${_c}-1" >/dev/null 2>&1 || true
  done
}

# Eski sertifikaları host'un /etc/letsencrypt'ine kopyala. Kopyalanabilirse yeni
# kurulum certbot'a hiç gitmeden çalışmaya başlayabilir; kopyalanamazsa da
# volume silinmeden önce en azından denenmiş olur.
rescue_legacy_certificates() {
  $DK volume inspect varyaone_letsencrypt >/dev/null 2>&1 || return 0
  echo "  Eski sertifikalar kurtarılmaya çalışılıyor..."
  elevate || return 1
  run_root mkdir -p /etc/letsencrypt
  # Volume'ü salt okunur bağlayan geçici bir konteynerle kopyala.
  $DK run --rm -v varyaone_letsencrypt:/src:ro -v /etc/letsencrypt:/dst \
    alpine:3.24 sh -c 'cp -an /src/. /dst/ 2>/dev/null || true' >/dev/null 2>&1 \
    && echo "  Eski sertifikalar /etc/letsencrypt altına kopyalandı." \
    || echo "  Eski sertifikalar kopyalanamadı (yeni sertifika alınacak)." >&2
  return 0
}

purge_legacy_proxy() {
  for _v in varyaone_letsencrypt varyaone_certbot-webroot; do
    $DK volume rm "$_v" >/dev/null 2>&1 || true
  done
}

# Bu makineye ağdan erişilecek adres. Sırasıyla: elle verilmiş değer, birincil
# ağ arayüzünün IP'si, son çare localhost.
detect_public_host() {
  _h=${VARYAONE_PUBLIC_HOST:-}
  [ -n "$_h" ] && { printf '%s' "$_h"; return 0; }
  if have ip; then
    _h=$(ip -4 route get 1.1.1.1 2>/dev/null | sed -n 's/.*src \([0-9.]*\).*/\1/p' | head -n1)
  fi
  [ -z "$_h" ] && have hostname && _h=$(hostname -I 2>/dev/null | awk '{print $1}')
  [ -n "$_h" ] && { printf '%s' "$_h"; return 0; }
  printf 'localhost'
}

# --- giriş doğrulama --------------------------------------------------------
# Aynı doğrulayıcılar hem sihirbazda hem etkileşimsiz yolda çalışır. Eskiden
# alan adı yalnız sihirbazda doğrulanıyordu; .env'e doğrudan yazılan bir değer
# hiçbir kontrolden geçmeden nginx yapılandırmasına giriyordu.

valid_port() {
  case "${1:-}" in
    ''|*[!0-9]*) return 1 ;;
  esac
  [ "$1" -ge 1 ] && [ "$1" -le 65535 ]
}

# Port kullanımda mı? 0 = boş/bilinmiyor, 1 = dolu.
port_in_use() {
  _p=$1
  if have ss; then
    ss -ltn 2>/dev/null | awk '{print $4}' | grep -Eq "[:.]${_p}\$" && return 1
  elif have netstat; then
    netstat -ltn 2>/dev/null | awk '{print $4}' | grep -Eq "[:.]${_p}\$" && return 1
  fi
  return 0
}

# Alan adı etiket kurallarına uyuyor mu? Satır sonu/boşluk içeren bir değer
# nginx yapılandırmasına enjeksiyon olarak girebilir.
valid_domain() {
  _d=${1:-}
  [ -n "$_d" ] || return 1
  [ "${#_d}" -le 253 ] || return 1
  case "$_d" in
    *[!a-zA-Z0-9.-]*) return 1 ;;
    .*|*.|*..*|-*|*-) return 1 ;;
  esac
  printf '%s' "$_d" | grep -q '\.' || return 1
  # Her etiket 1-63 karakter ve tire ile başlamaz/bitmez.
  printf '%s' "$_d" | tr '.' '\n' | while read -r _label; do
    [ -n "$_label" ] && [ "${#_label}" -le 63 ] || exit 1
    case "$_label" in -*|*-) exit 1 ;; esac
  done
}

# Portları doğrula ve çakışmaya bak. Kullanım: validate_ports <web> <api>
validate_ports() {
  _wp=${1:-}; _ap=${2:-}
  for _p in "$_wp" "$_ap"; do
    [ -n "$_p" ] || continue
    valid_port "$_p" || { echo "Geçersiz port: $_p (1-65535 olmalı)" >&2; return 1; }
  done
  if [ -n "$_wp" ] && [ -n "$_ap" ] && [ "$_wp" = "$_ap" ]; then
    echo "Web ve API portları aynı olamaz: $_wp" >&2
    return 1
  fi
  return 0
}

install_domainless() {
  _wp=${1:-}
  _ap=${2:-}
  # Portları serbest bırak, ama sertifika volume'lerine dokunma: yeni kurulum
  # sağlık kontrolünü geçene kadar onlar hâlâ geri dönüş malzemesidir.
  stop_legacy_proxy
  [ -n "$(env_get VARYAONE_DOMAIN)" ] && remove_host_nginx
  validate_ports "$_wp" "$_ap" || exit 2
  [ -n "$_wp" ] && env_set VARYAONE_WEB_PORT "$_wp"
  [ -n "$_ap" ] && env_set VARYAONE_API_PORT "$_ap"
  env_set VARYAONE_WEB_BIND 0.0.0.0
  env_set VARYAONE_API_BIND 0.0.0.0
  env_set VARYAONE_DOMAIN ""
  web_port=$(env_get VARYAONE_WEB_PORT); web_port=${web_port:-3000}

  # Origin, tarayıcının gerçekte kullandığı adres olmalıdır.
  #
  # Domainsiz mod 0.0.0.0'a bağlanır ve "ağdaki başka bir makineden de
  # açabilirsiniz" der; origin'i localhost'a sabitlemek ise SvelteKit'in
  # form/multipart POST origin kontrolünü o makineler için kırar. Belirti,
  # yerelde çalışıp uzaktan çalışmayan bir giriş ekranıdır.
  #
  # Zaten ayarlanmış, localhost olmayan bir origin yeniden kurulumda EZİLMEZ:
  # operatörün elle yazdığı doğru değer, betiğin varsayılanından daha iyi bilgi.
  _origin=$(env_get VARYAONE_WEB_ORIGIN)
  case "$_origin" in
    ""|http://localhost:*|https://localhost:*)
      _host=$(detect_public_host)
      env_set VARYAONE_WEB_ORIGIN "http://${_host}:$web_port"
      [ "$_host" != "localhost" ] && \
        echo "  Erişim adresi: http://${_host}:$web_port (değiştirmek için .env içinde VARYAONE_WEB_ORIGIN)"
      ;;
    *) echo "  Mevcut erişim adresi korunuyor: $_origin" ;;
  esac

  # Düz HTTP üzerinde Secure cerez tarayıcı tarafından gönderilmez; oturum
  # hiç kurulamaz. Bu, güvenliği gevşetmek değil, HTTP modunda doğru olan
  # ayardır — HTTPS moduna geçildiğinde install_domain bunu geri açar.
  env_set VARYAONE_SECURE_COOKIES false

  compose config --quiet
  bring_up_stack --build || exit 1
  compose ps
  resolved=$(published_port frontend 3000 "$web_port")
  # Yeni kurulum ayakta ve sağlıklı: ancak şimdi eski proxy volume'leri silinir.
  if wait_healthy 180; then
    purge_legacy_proxy
  else
    echo "  UYARI: sağlık kontrolü geçmedi; eski proxy volume'leri korundu." >&2
  fi
  echo
  echo "Varya One hazır (domainsiz, SSL yok): http://localhost:$resolved"
}

# install_domain <domain> <email> <staging:0|1> <webport>
# Ters proxy sunucunun kendi nginx'idir; Docker'da nginx/certbot çalışmaz.
install_domain() {
  domain=$1
  email=$2
  staging=${3:-0}
  _wp=${4:-}

  [ -n "$domain" ] || { echo "Alan adı gerekli." >&2; exit 2; }
  [ -n "$email" ] || { echo "Let's Encrypt e-posta adresi gerekli." >&2; exit 2; }

  # Eski sertifikaları önce kurtar, sonra konteynerleri durdur. Volume'ler
  # yeni kurulum çalıştığı doğrulanana kadar durur.
  rescue_legacy_certificates
  stop_legacy_proxy

  [ -n "$_wp" ] && env_set VARYAONE_WEB_PORT "$_wp"
  WEB_PORT=$(env_get VARYAONE_WEB_PORT); WEB_PORT=${WEB_PORT:-3000}

  # frontend/api yalnızca 127.0.0.1; dışarıya bakan tek şey host nginx.
  valid_domain "$domain" || { echo "Geçersiz alan adı: $domain" >&2; exit 2; }
  validate_ports "$_wp" "" || exit 2
  env_set VARYAONE_DOMAIN "$domain"
  env_set VARYAONE_ACME_EMAIL "$email"
  env_set VARYAONE_WEB_ORIGIN "https://$domain"
  # HTTPS modunda üretim varsayılanına dön.
  env_set VARYAONE_SECURE_COOKIES true
  env_set VARYAONE_WEB_BIND 127.0.0.1
  env_set VARYAONE_API_BIND 127.0.0.1

  echo "1/4 yapılandırma doğrulanıyor..."
  compose config --quiet

  echo "2/4 servisler başlatılıyor (postgres, migration, rol, api, worker, frontend)..."
  bring_up_stack --build || exit 1

  echo "3/4 frontend bekleniyor (127.0.0.1:$WEB_PORT)..."
  _fp=$(published_port frontend 3000 "$WEB_PORT")
  wait_for_ok "http://127.0.0.1:$_fp" 45 >/dev/null || \
    echo "  ! frontend henüz yanıt vermiyor; nginx yine de kurulacak."

  echo "4/4 host nginx + Let's Encrypt yapılandırılıyor..."
  configure_host_nginx "$domain" "$email" "$staging"

  echo
  compose ps
  echo
  echo "Site doğrulanıyor (https://$domain)..."
  if _code=$(wait_for_ok "https://$domain" 45); then
    # Yeni kurulum dışarıdan yanıt veriyor: eski proxy volume'leri artık
    # kurtarma malzemesi değil.
    purge_legacy_proxy
    echo "Varya One hazır: https://$domain"
  else
    echo >&2
    echo "UYARI: sertifika ve nginx tamam ama site https://$domain üzerinden" >&2
    echo "yanıt vermiyor (son HTTP kodu: ${_code:-000}). Genelde frontend" >&2
    echo "konteyneri kalkmamıştır. Şununla bakın:" >&2
    echo "  docker compose logs --tail=60 frontend api" >&2
    exit 1
  fi
}

# valid_domain tek bir yerde tanımlıdır (yukarıda, giriş doğrulama bölümünde).
# Burada ikinci ve daha gevşek bir tanım vardı; shell'de sonraki tanım öncekini
# ezdiği için sıkı kontrol hiçbir yerde çalışmıyordu.

valid_email() {
  case "$1" in
    ?*@?*.?*) return 0 ;;
    *) return 1 ;;
  esac
}

server_ip() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsS --max-time 5 https://api.ipify.org 2>/dev/null && return 0
  fi
  return 1
}

resolves_here() {
  _d=$1
  _ips=""
  if command -v getent >/dev/null 2>&1; then
    _ips=$(getent ahosts "$_d" 2>/dev/null | awk '{print $1}' | sort -u)
  elif command -v dig >/dev/null 2>&1; then
    _ips=$(dig +short "$_d" A "$_d" AAAA 2>/dev/null)
  else
    return 2
  fi
  [ -n "$_ips" ] || return 1
  _self=$(server_ip || true)
  [ -n "$_self" ] || return 2
  printf '%s\n' "$_ips" | grep -qx "$_self"
}

# Adım adım interaktif sihirbaz. Şu değişkenleri doldurur:
#   WZ_MODE (domain|domainless) WZ_DOMAIN WZ_EMAIL WZ_STAGING
#   WZ_WEBPORT WZ_APIPORT
wizard() {
  banner "Kurulum Sihirbazı"
  printf '  Bu sunucuya Varya One nasıl kurulsun?\n\n'
  printf '    1) Domainli   — otomatik HTTPS (sunucunun kendi nginx'\''i + Let'\''s Encrypt)   [önerilen]\n'
  printf '    2) Domainsiz  — yalnızca HTTP, sunucu IP'\''si : port ile erişim\n\n'

  _choice=$(ask "Seçiminiz" "1")
  case "$_choice" in
    2 | domainsiz | Domainsiz)
      WZ_MODE=domainless
      printf '\n  --- Domainsiz kurulum ---\n\n'
      WZ_WEBPORT=$(ask "Web arayüzü portu" "$(env_get VARYAONE_WEB_PORT | grep . || echo 3000)")
      WZ_APIPORT=$(ask "API portu (isteğe bağlı, dışarı açık)" "$(env_get VARYAONE_API_PORT | grep . || echo 8080)")
      printf '\n  Özet:\n'
      printf '    Mod           : Domainsiz (SSL yok)\n'
      printf '    Web portu     : %s\n' "$WZ_WEBPORT"
      printf '    API portu     : %s\n' "$WZ_APIPORT"
      printf '    PostgreSQL    : dışarı kapalı\n\n'
      ask_yesno "Kuruluma başlansın mı?" e || { echo "  İptal edildi." >&2; exit 1; }
      ;;
    *)
      WZ_MODE=domain
      printf '\n  --- Domainli kurulum (otomatik SSL) ---\n\n'
      while :; do
        WZ_DOMAIN=$(ask "Alan adı (örn. erp.firmaniz.com)" "$(env_get VARYAONE_DOMAIN)")
        valid_domain "$WZ_DOMAIN" && break
        printf '  Geçerli bir alan adı girin.\n' >&2
      done
      while :; do
        WZ_EMAIL=$(ask "Let's Encrypt e-posta (sertifika/yenileme uyarıları)" "$(env_get VARYAONE_ACME_EMAIL)")
        valid_email "$WZ_EMAIL" && break
        printf '  Geçerli bir e-posta girin.\n' >&2
      done
      WZ_WEBPORT=$(ask "frontend'in yerel portu (nginx buraya proxy yapar)" "$(env_get VARYAONE_WEB_PORT | grep . || echo 3000)")
      # Her zaman gerçek Let's Encrypt sertifikası; staging tarayıcıda
      # ERR_CERT_AUTHORITY_INVALID verdiği için sihirbazda sunulmuyor.
      WZ_STAGING=0

      printf '\n  DNS kontrol ediliyor (%s)...\n' "$WZ_DOMAIN"
      if resolves_here "$WZ_DOMAIN"; then
        printf '    ✓ %s bu sunucuya işaret ediyor.\n' "$WZ_DOMAIN"
      else
        _rc=$?
        if [ "$_rc" = 2 ]; then
          printf '    ? DNS otomatik doğrulanamadı (araç yok). Elle kontrol edin.\n'
        else
          printf '    ! UYARI: %s bu sunucunun IP'\''sine çözümlenmiyor.\n' "$WZ_DOMAIN"
          printf '      Sertifika alımı bu haliyle BAŞARISIZ olur.\n'
          ask_yesno "Yine de devam edilsin mi?" h || { echo "  İptal edildi." >&2; exit 1; }
        fi
      fi

      printf '\n  Özet:\n'
      printf '    Mod           : Domainli + otomatik HTTPS (host nginx)\n'
      printf '    Alan adı      : %s  ->  https://%s\n' "$WZ_DOMAIN" "$WZ_DOMAIN"
      printf '    E-posta       : %s\n' "$WZ_EMAIL"
      printf '    Ters proxy    : sunucunun kendi nginx'\''i (80/443)\n'
      printf '    frontend      : 127.0.0.1:%s (yalnız nginx erişir)\n' "$WZ_WEBPORT"
      printf '    Sertifika     : Let'\''s Encrypt (webroot) — otomatik yenilenir\n'
      printf '    frontend/api  : yalnızca 127.0.0.1 (dışarı kapalı)\n'
      printf '    PostgreSQL    : dışarı kapalı\n\n'
      ask_yesno "Kuruluma başlansın mı?" e || { echo "  İptal edildi." >&2; exit 1; }
      ;;
  esac
}

install_stack() {
  [ $# -eq 0 ] || { echo "install ek seçenek almaz; ayarlar sihirbazda sorulur." >&2; usage; }

  ensure_prereqs
  create_env

  if interactive; then
    # Terminalde: adım adım sihirbaz.
    wizard
    if [ "$WZ_MODE" = "domain" ]; then
      install_domain "$WZ_DOMAIN" "$WZ_EMAIL" "$WZ_STAGING" "$WZ_WEBPORT"
    else
      install_domainless "$WZ_WEBPORT" "$WZ_APIPORT"
    fi
    return
  fi

  # İnteraktif değil (CI / pipe): .env'e göre karar ver.
  domain=$(env_get VARYAONE_DOMAIN)
  if [ -n "$domain" ]; then
    email=$(env_get VARYAONE_ACME_EMAIL)
    [ -n "$email" ] || { echo ".env içinde VARYAONE_ACME_EMAIL gerekli (interaktif olmayan domainli kurulum)." >&2; exit 2; }
    banner
    install_domain "$domain" "$email" 0 ""
  else
    install_domainless "" ""
  fi
}

# Sertifikayı yenile.
#
# Yalnız BU kurulumun soyunu yeniler. Eskiden `certbot renew --force-renewal`
# çağrılıyordu: bu, sunucudaki her sertifikayı — Varya One ile ilgisi olmayan
# siteler dahil — zorla yeniler ve Let's Encrypt oran sınırlarını başkasının
# adına tüketir. Zorlama da varsayılan değil, açık bir seçenektir: normalde
# yenileme yalnız vadesi geldiğinde yapılır.
renew_cert() {
  _force=0
  case "${1:-}" in
    --force) _force=1 ;;
    "") : ;;
    *) echo "Bilinmeyen seçenek: $1" >&2; exit 2 ;;
  esac
  domain_mode || { echo "Domainli kurulum yok (.env içinde VARYAONE_DOMAIN boş)." >&2; exit 1; }
  have certbot || { echo "certbot bulunamadı." >&2; exit 1; }
  elevate || { echo "Sertifika yenilemek için root/sudo gerekli." >&2; exit 1; }

  _domain=$(env_get VARYAONE_DOMAIN)
  _lineage=$(env_get VARYAONE_CERT_LINEAGE)
  [ -n "$_lineage" ] || _lineage=$(cert_lineage "$_domain")
  if ! run_root test -d "/etc/letsencrypt/renewal" || \
     ! run_root test -f "/etc/letsencrypt/renewal/${_lineage}.conf"; then
    # Bu kurulumdan önce alınmış, adı bizim şemamıza uymayan bir sertifika
    # olabilir. Onu zorla yeniden adlandırmak yerine adıyla hedefle.
    _lineage=$_domain
  fi

  set -- renew --cert-name "$_lineage" --non-interactive
  [ "$_force" = 1 ] && set -- "$@" --force-renewal
  run_root certbot "$@" || { echo "Yenileme başarısız." >&2; exit 1; }

  nginx_reload || { echo "nginx yeniden yüklenemedi." >&2; exit 1; }
  # Yenilendi demeden önce TLS'in gerçekten yeni sertifikayla çalıştığını ölç.
  if cert_days_left "$_domain" >/dev/null 2>&1; then
    echo "Sertifika yenilendi ($(cert_days_left "$_domain") gün kaldı)."
  else
    echo "Sertifika yenilendi, ancak TLS doğrulanamadı; ./deploy.sh doctor ile bakın." >&2
    exit 1
  fi
}

# Sunucunun sunduğu sertifikanın kalan gün sayısı. Dosyanın varlığı değil,
# gerçekten sunulan zincir ölçülür: süresi geçmiş ya da yanlış alan adına ait
# bir dosya da "var".
cert_days_left() {
  _d=$1
  have openssl || return 1
  _end=$(printf '' | openssl s_client -servername "$_d" -connect 127.0.0.1:443 2>/dev/null \
    | openssl x509 -noout -enddate 2>/dev/null | sed 's/notAfter=//')
  [ -n "$_end" ] || return 1
  _endsec=$(date -d "$_end" +%s 2>/dev/null) || return 1
  _now=$(date +%s)
  echo $(( (_endsec - _now) / 86400 ))
}

show_status() {
  require_docker
  compose ps
  if command -v curl >/dev/null 2>&1; then
    api_port=$(published_port api 8080 "$(env_get VARYAONE_API_PORT)")
    api_port=${api_port:-8080}
    curl --fail --silent --show-error "http://localhost:$api_port/health/ready" 2>/dev/null || \
      compose exec -T api wget -q -O - http://127.0.0.1:8080/health/ready || exit 1
    echo
  fi
  if domain_mode; then
    echo "Genel adres: https://$(env_get VARYAONE_DOMAIN)"
  fi
}


# --- sürüm kimliği ve geri dönüş -------------------------------------------
# Bir deploy'un geri alınabilmesi için, geri dönülecek şeyin ne olduğunun
# yazılı olması gerekir. "latest etiketini yeniden derleriz" bir geri dönüş
# planı değildir: aynı kaynaktan aynı image'ın çıkacağını varsayar ve bu,
# taban image'lar ve paket depoları değiştiği anda yanlış olur.
#
# Bu yüzden mevcut image'ların digest'leri, .env ve compose dosyaları deploy
# başlamadan önce release dizinine kopyalanır. Geri dönüş, o digest'lere
# yeniden etiketlemektir — yeniden derlemek değil.
RELEASE_DIR="$project_dir/deploy/releases"

_image_id() { $DK image inspect --format '{{.Id}}' "$1" 2>/dev/null; }

# Mevcut durumun sürüm kaydını al. Yazdığı dizini stdout'a basar.
#
# Image'lar yalnız digest ile kaydedilmez, AYRICA kendi etiketleriyle sabitlenir.
# Bir derleme `varyaone-api:latest` etiketini yeni image'a taşır; eskisi
# etiketsiz kalır ve Docker onu istediği zaman toplayabilir — nitekim toplar.
# Geri dönülecek image'ı bir etiketle tutmak, onu "geri dönebiliriz" varsayımı
# olmaktan çıkarıp gerçekten var olan bir şeye dönüştürür.
capture_release() {
  _rid=$(date -u +%Y%m%dT%H%M%SZ)-$$
  _rdir="$RELEASE_DIR/$_rid"
  mkdir -p "$_rdir" || return 1
  chmod 700 "$RELEASE_DIR" "$_rdir" 2>/dev/null || true
  printf '%s\n' "$_rid" > "$_rdir/id"
  for _svc in api worker frontend migrate; do
    _id=$(_image_id "varyaone-${_svc}:latest")
    [ -n "$_id" ] || continue
    if $DK tag "$_id" "varyaone-${_svc}:rollback-${_rid}" >/dev/null 2>&1; then
      printf '%s=%s\n' "$_svc" "varyaone-${_svc}:rollback-${_rid}" >> "$_rdir/images"
    else
      printf '%s=%s\n' "$_svc" "$_id" >> "$_rdir/images"
    fi
  done
  # Zorunlu bileşenler. Bunlardan biri kopyalanamıyorsa kayıt eksiktir ve eksik
  # bir kayıtla geri dönmek, geri dönüldüğünü sanmaktır: sessizce geçilmez.
  #
  # .env şifreleme anahtarını içerir: 0600, ve yalnız kök dizini 0700 olan
  # release dizininde.
  if [ -f "$project_dir/.env" ]; then
    cp "$project_dir/.env" "$_rdir/env" || { echo "  .env kopyalanamadı." >&2; return 1; }
    chmod 600 "$_rdir/env"
  fi
  # compose.yaml da sürümün parçası: yeni compose ile eski image çalıştırmak
  # eski sürüme dönmek değildir. Servis adları, volume'ler, sağlık kontrolleri
  # ve ortam değişkenleri sürümle birlikte değişir.
  cp "$project_dir/compose.yaml" "$_rdir/compose.yaml" \
    || { echo "  compose.yaml kopyalanamadı." >&2; return 1; }
  # Şema sürümü: geri dönüşte eski ikilinin bu şemayı anlayıp anlamadığını
  # sormak için gerekir; sorulamıyorsa "bilinmiyor" diye kaydedilir, boş değil.
  if _rs=$(maintenance migrate status 2>/dev/null) && [ -n "$_rs" ]; then
    printf '%s\n' "$_rs" > "$_rdir/schema"
  else
    printf 'unknown\n' > "$_rdir/schema"
  fi
  git -C "$project_dir" rev-parse HEAD > "$_rdir/commit" 2>/dev/null || true
  # Çalışma ağacındaki değişiklikler de sürümün parçası: commit tek başına
  # neyin derlendiğini söylemez.
  git -C "$project_dir" status --porcelain > "$_rdir/worktree" 2>/dev/null || true
  printf '%s\n' "$_rdir"
}

# Sürüm kaydına, alındıktan sonra bulunan deploy öncesi yedeği bağla. Kayıt ile
# yedek ayrı yerlerde durur; hangi yedeğin hangi sürüme ait olduğunu bilmeden
# "tam geri dönüş" diye bir şey yoktur.
record_release_safety() {
  _rdir=$1; _file=$2
  [ -d "$_rdir" ] && [ -f "$_file" ] || return 0
  printf '%s\n' "$_file" > "$_rdir/safety"
  if [ -f "$_file.sha256" ]; then
    cp "$_file.sha256" "$_rdir/safety.sha256" 2>/dev/null || true
  fi
}

# Kaydedilmiş sürüme dön: image'ları digest'lerinden yeniden etiketle ve
# servisleri o etiketlerle ayağa kaldır.
restore_release() {
  _rdir=$1
  [ -f "$_rdir/images" ] || { echo "  Sürüm kaydı eksik: $_rdir" >&2; return 1; }
  _ok=1
  while IFS='=' read -r _svc _ref; do
    [ -n "$_svc" ] && [ -n "$_ref" ] || continue
    if $DK image inspect "$_ref" >/dev/null 2>&1; then
      $DK tag "$_ref" "varyaone-${_svc}:latest" >/dev/null 2>&1 || _ok=0
    else
      echo "  Eski image bulunamadı: $_svc ($_ref)" >&2
      _ok=0
    fi
  done < "$_rdir/images"
  [ "$_ok" = 1 ] || return 1
  if [ -f "$_rdir/env" ]; then
    cp "$_rdir/env" "$project_dir/.env" || return 1
    chmod 600 "$project_dir/.env"
  fi
  # Kaydedilen compose.yaml da geri yüklenir. Aksi halde eski image'lar yeni
  # sürümün compose tanımıyla — yeni servis adları, yeni volume'ler, yeni
  # ortam değişkenleriyle — çalıştırılır; bu bir geri dönüş değil, üçüncü bir
  # sürümdür. Mevcut dosya, ileri geri dönmek gerekirse diye saklanır.
  if [ -f "$_rdir/compose.yaml" ] \
     && ! cmp -s "$_rdir/compose.yaml" "$project_dir/compose.yaml"; then
    cp "$project_dir/compose.yaml" "$project_dir/compose.yaml.rollback-backup" 2>/dev/null || true
    cp "$_rdir/compose.yaml" "$project_dir/compose.yaml" || return 1
    echo "  compose.yaml sürüm kaydından geri yüklendi." >&2
  fi
  compose up -d --force-recreate >/dev/null 2>&1 || return 1
  return 0
}

# Sürüm kayıtlarını buda: son N tanesini tut. Geri dönüş malzemesi olduğu için
# cömert davran; birkaç KB'lik dosyalar, karşılığında bir deploy geri alınabilir.
prune_releases() {
  _keep=${1:-10}
  [ -d "$RELEASE_DIR" ] || return 0
  ls -1t "$RELEASE_DIR" 2>/dev/null | tail -n +$((_keep + 1)) | while read -r _old; do
    [ -n "$_old" ] || continue
    # Etiketleri de bırak, yoksa her deploy bir image kümesini kalıcı olarak
    # diskte tutar.
    if [ -f "$RELEASE_DIR/$_old/images" ]; then
      while IFS='=' read -r _svc _ref; do
        case "$_ref" in
          *:rollback-*) $DK image rm "$_ref" >/dev/null 2>&1 || true ;;
        esac
      done < "$RELEASE_DIR/$_old/images"
    fi
    rm -rf "$RELEASE_DIR/$_old"
  done
}

rebuild() {
  require_docker
  oplog_start rebuild "$@"
  banner "Yeniden Derleme"
  no_cache=""
  skip_backup=0
  _op=""
  for _arg in "$@"; do
    case "$_arg" in
      --no-cache) no_cache="--no-cache" ;;
      # Yedek almadan deploy: yalnız yedeklemenin mümkün olmadığı ya da
      # bilinçli olarak istenmediği durumlar için, ve açıkça istenmeli.
      --skip-backup) skip_backup=1 ;;
      "") : ;;
      *) echo "Bilinmeyen seçenek: $_arg" >&2; usage ;;
    esac
  done
  [ -f .env ] || { echo ".env yok; önce ./deploy.sh install çalıştırın." >&2; exit 1; }

  # Aynı kilit: iki deploy, deploy ile yedek, deploy ile geri yükleme çakışmasın.
  acquire_lock update
  # INT/TERM yalnız kilidi bırakıp komutlara devam etmez. Ctrl-C'den sonra
  # migration'ın çalışmaya devam ettiği bir deploy, tam olarak kaydı olmayan
  # bir geçiştir; burada kontrollü çıkılır ve açık işlem varsa kurulum bakımda
  # bırakılır. SIGKILL'de bu çalışmaz — orada kalıcı işlem kaydı devralır.
  trap '_deploy_interrupted' INT TERM
  trap 'release_lock' EXIT
  umask 077

  # 1) Yarıda kalmış bir işlem varsa yeni bir deploy başlatma.
  if compose ps --status running postgres >/dev/null 2>&1 && ensure_postgres_up; then
    _ss=0; system_serviceable || _ss=$?
    case "$_ss" in
      1)
        echo "  Yarıda kalmış bir sistem işlemi var; deploy başlatılmıyor." >&2
        echo "  ${SYSTEM_STATUS_REASON:-}" >&2
        echo "  ./deploy.sh system-status" >&2
        exit 1
        ;;
      2)
        # Çalışan sürüm bu soruyu cevaplayamıyor (koordinatörden önceki bir
        # ikili). Deploy'u bu yüzden durdurmak, tam da koordinatörü kuracak
        # olan yükseltmeyi engellerdi.
        echo "  Not: çalışan sürüm işlem durumunu bildiremiyor; deploy sürüyor." >&2
        ;;
    esac
  fi

  # 2) Mevcut sürümü kaydet. Geri dönülecek şey budur.
  _release=$(capture_release) || { echo "  Sürüm kaydı alınamadı." >&2; exit 1; }
  echo "  Mevcut sürüm kaydedildi: $(basename "$_release")"

  # 3) Deploy öncesi doğrulanmış yedek. Migration veriyi değiştirebilir ve
  #    image'ı geri almak veriyi geri almaz.
  _predeploy=""
  if [ "$skip_backup" = 1 ]; then
    echo "  UYARI: --skip-backup verildi; migration veriyi değiştirirse geri dönüş yok." >&2
  elif system_installed_quiet; then
    ensure_backup_dir || { echo "  backups dizini oluşturulamadı." >&2; exit 1; }
    _pd_final="$project_dir/backups/pre-deploy-$(date -u +%Y%m%dT%H%M%SZ).varya"
    _pd_tmp=$(mktemp "$project_dir/backups/.pre-deploy.XXXXXX") || _pd_tmp=""
    echo "  Deploy öncesi yedek alınıyor: $(basename "$_pd_final")"
    if [ -n "$_pd_tmp" ] && backup_create_complete "$_pd_tmp" \
       && publish_backup "$_pd_tmp" "$_pd_final"; then
      _predeploy=$PUBLISHED_BACKUP
      record_release_safety "$_release" "$_predeploy"
    else
      rm -f "$_pd_tmp"
      echo "  Deploy öncesi yedek alınamadı; deploy durduruldu." >&2
      echo "  Yedek almadan devam etmek için: ./deploy.sh rebuild --skip-backup" >&2
      exit 1
    fi
  fi

  # 4) Derleme. Buradaki hata hiçbir şeyi değiştirmez: eski konteynerler çalışır.
  oplog "aşama: derleme"
  echo "  Görüntüler derleniyor${no_cache:+ (önbelleksiz)}..."
  if ! compose build $no_cache; then
    echo "  Derleme başarısız; çalışan sürüm değişmedi." >&2
    exit 1
  fi

  # 5) Deploy buradan sonra canlı veriye dokunabilir. Önce bunun kalıcı kaydını
  #    yaz: yazılamıyorsa migration da servis geçişi de başlamasın. Kayıt
  #    tutulamayan bir deploy, yarıda kaldığında hiçbir şey söylemez.
  #
  #    Aynı kayıt sırada bekleyen başka bir işlem (API yedeği, CLI geri
  #    yükleme) için de "bu kurulum meşgul" demektir; shell kilidi yalnız bu
  #    betiğin kopyalarını durdurur.
  # `set -e` altında çıplak bir çağrının sıfırdan farklı dönüşü betiği sessizce
  # bitirir; bekleyen migration (dönüş 1) tam da deploy'un var olma sebebidir.
  _pending=0; pending_migrations || _pending=$?
  case "$_pending" in
    0) echo "  Bekleyen migration yok." ;;
    1) echo "  Bekleyen migration var." ;;
    *) echo "  Migration durumu sorulamadı; deploy veriyi değiştirebilir sayılıyor." ;;
  esac
  _op=$(maintenance system begin deploy "rebuild" 2>/dev/null | tr -d '\r' | tail -n1)
  case "$_op" in
    2???????T??????Z-*) : ;;
    *)
      echo "  İşlem kaydı açılamadı; deploy başlatılmıyor." >&2
      echo "  Çalışan sürüm değişmedi. Durum: ./deploy.sh system-status" >&2
      exit 1
      ;;
  esac
  oplog "işlem: $_op"
  echo "  İşlem kaydı: $_op"

  # 6) Migration'ı açıkça çalıştır. `up -d`'nin bağımlılık sırasına güvenmek,
  #    migration'ın hangi noktada ve hangi sonuçla çalıştığını görmemek demektir.
  #
  #    Niyet önce yazılır: çakılma anında "migration başlamıştı" ile "hiç
  #    başlamamıştı" arasındaki fark, eski image'ların açılıp açılamayacağıdır.
  if ! maintenance system phase "$_op" SWITCHING "migration başlıyor" >/dev/null 2>&1; then
    echo "  Migration niyeti kaydedilemedi; migration çalıştırılmıyor." >&2
    _hold_deploy "$_op" "işlem kaydı"
    exit 1
  fi
  echo "  Migration uygulanıyor..."
  # Servisin kendi komutu zaten `migrate up`. Buraya ayrıca `up` yazmak,
  # entrypoint'e `varyaone up` geçirir — yani hiçbir migration çalışmaz.
  #
  # VARYAONE_OPERATION_ID, migration konteynerinin kendi işleminin kapısında
  # takılmamasını sağlar; bu kimliği bilmeyen başka hiçbir süreç geçemez.
  if ! compose run --rm --no-deps -e VARYAONE_OPERATION_ID="$_op" migrate; then
    echo "  Migration başarısız." >&2
    _hold_deploy "$_op" "migration"
    exit 1
  fi
  if ! maintenance system phase "$_op" COMMITTED "migration tamam" >/dev/null 2>&1; then
    echo "  Migration başarılı ama sonucu kaydedilemedi." >&2
    _hold_deploy "$_op" "işlem kaydı"
    exit 1
  fi

  # Migration'dan sonraki hatalar için geri dönüş kararı. Yalnız bekleyen
  # migration olmadığı KANITLANDIYSA eski image'lara dönmek güvenlidir; aksi
  # halde eski ikili yeni şemayı bulur ve "sağlıklı" görünerek tutarsız veri
  # yazar. Kanıt yoksa sistem bakımda kalır.
  if [ "$_pending" = 0 ]; then
    _on_fail=_rollback_deploy
  else
    _on_fail=_hold_deploy
  fi

  # 7) Servisleri yeni image'larla ayağa kaldır.
  echo "  Servisler yeniden başlatılıyor..."
  if ! compose up -d --force-recreate; then
    echo "  Servisler başlatılamadı." >&2
    $_on_fail "$_op" "servis başlatma"
    exit 1
  fi

  # 8) Sağlık kapısı. "Konteyner ayağa kalktı" ile "uygulama çalışıyor" aynı
  #    şey değildir; trafiği açmadan önce ikincisini ölç.
  oplog "aşama: sağlık kontrolü"
  echo "  Sağlık kontrolü..."
  if ! wait_healthy 180; then
    echo "  Yeni sürüm sağlık kontrolünü geçmedi." >&2
    $_on_fail "$_op" "sağlık kontrolü"
    exit 1
  fi

  reload_nginx_soft
  prune_releases 10
  echo
  compose ps
  echo
  oplog "sonuç: başarılı"
  echo "  Yeniden derleme tamamlandı."
  [ -n "$_predeploy" ] && echo "  Deploy öncesi yedek: $_predeploy"
  echo "  Geri dönüş kaydı: $_release"
}

# Dışarıdan bakılınca kurulum gerçekten kapalı mı? Yayımlanan portta hâlâ
# başarılı bir yanıt varsa "bakımda" iddiası yanlıştır: birileri yazmaya devam
# edebilir. 0 = kapalı (2xx/3xx yok), 1 = hâlâ servis veriyor.
_traffic_closed() {
  _port=$(published_port frontend 3000 "$(env_get VARYAONE_HTTP_PORT)")
  [ -n "$_port" ] || _port=80
  _i=0
  while [ "$_i" -lt 10 ]; do
    _code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 \
      "http://127.0.0.1:${_port}/" 2>/dev/null || echo 000)
    case "$_code" in
      2?? | 3??) ;;
      *) return 0 ;;
    esac
    _i=$((_i + 1))
    sleep 2
  done
  return 1
}

# Deploy'u bakımda bırak.
#
# Migration başladıktan sonraki her hata için tek doğru cevap budur. Eski
# image'lar ayağa kalkıp sağlık ucuna cevap verebilir — bu, eski tutarlı
# durumun geri geldiğini değil, yalnız eski ikilinin çalıştığını gösterir; veri
# o sırada yeni şemada olabilir. Bu yüzden burada hiçbir şey otomatik açılmaz:
# yazıcılar durdurulur, bakım kaydı yeniden başlatmaların da açmaması için
# kalıcılaştırılır ve dışarıdan gerçekten kapalı olduğu doğrulanır.
_hold_deploy() {
  _op=$1; _stage=$2
  _reason="deploy $_stage aşamasında başarısız; canlı veri değişmiş olabilir"
  oplog "sonuç: başarısız ($_stage); kurulum bakımda"
  echo >&2
  echo "  Başarısız aşama: $_stage" >&2
  echo "  Canlı veri değişmiş olabilir; eski sürüm otomatik açılmıyor." >&2

  # 1) Yazıcıları durdur. Mesaj basmakla yazmayı durdurmak aynı şey değildir.
  compose stop api worker frontend >/dev/null 2>&1 || true

  # 2) Bakım kaydını kalıcılaştır. Bu yazılmazsa bir sonraki başlatma kurulumu
  #    hiçbir şey olmamış gibi açar; o yüzden yazılamaması sessiz geçilemez.
  # `hold` açık kaydı da kapanmış kaydı da aynı sonuca getirir: kurulum
  # hizmete alınamaz. Hangi aşamada başarısız olunduğunu bilmesi gerekmez.
  if maintenance system hold "$_reason" >/dev/null 2>&1; then
    _held=1
  else
    _held=0
  fi
  if [ "$_held" = 0 ]; then
    echo >&2
    echo "  BAKIM KAYDI YAZILAMADI." >&2
    echo "  Kurulumun kendiliğinden açılmasını engelleyemiyoruz; yığın indiriliyor." >&2
    if ! compose down >/dev/null 2>&1; then
      echo "  YIĞIN DA İNDİRİLEMEDİ. Servisleri elle durdurun:" >&2
      echo "    $DK compose down" >&2
    fi
  fi

  # 3) İddiayı doğrula: dışarıdan bakınca gerçekten kapalı mı?
  if _traffic_closed; then
    echo "  Dış erişim kapalı." >&2
  else
    echo "  UYARI: yayımlanan port hâlâ başarılı yanıt veriyor; erişimi elle kapatın." >&2
  fi

  echo >&2
  echo "  SİSTEM BAKIMDA. Kurtarma malzemesi (hiçbiri silinmedi):" >&2
  echo "    sürüm kaydı:          $_release" >&2
  [ -n "$_predeploy" ] && echo "    deploy öncesi yedek:  $_predeploy" >&2
  [ -n "$_op" ] && echo "    işlem günlüğü:        ./deploy.sh system-history $_op" >&2
  echo "    durum:                ./deploy.sh system-status" >&2
  echo >&2
  echo "  Veriyi deploy öncesine döndürmek için:" >&2
  [ -n "$_predeploy" ] && echo "    ./deploy.sh restore \"$_predeploy\" --confirm" >&2
  echo "  İnceleyip düzelttikten sonra:" >&2
  echo "    ./deploy.sh system-resolve \"ne yapıldı\"" >&2
  return 1
}

# Deploy elle kesildi. Ne kadar ilerlendiğine göre davran: işlem kaydı hiç
# açılmadıysa canlı sisteme dokunulmamıştır ve çıkmak yeterlidir; açıldıysa
# migration başlamış olabilir ve kurulum bakımda bırakılır.
_deploy_interrupted() {
  echo >&2
  echo "  Kesildi." >&2
  if [ -n "${_op:-}" ]; then
    _hold_deploy "$_op" "kesinti (INT/TERM)"
  else
    echo "  Canlı sisteme dokunulmamıştı; çalışan sürüm değişmedi." >&2
  fi
  release_lock
  exit 130
}

# Geri dönülen kurulum gerçekten çalışıyor mu?
#
# wait_healthy "süreç ayakta ve bir uca cevap veriyor" der. Geri dönüşten sonra
# sorulması gereken bundan fazlasıdır: eski ikili bu şemayı anlıyor mu, uygulama
# rolü hâlâ bağlanabiliyor mu, dosyalara erişilebiliyor mu. Üçünden biri hayırsa
# "geri dönüldü" demek yanlıştır. 0 = doğrulandı.
_verify_restored() {
  _vok=0

  # 1) Şema uyumu. `migrate status` bekleyen ya da tanınmayan migration varsa
  #    sıfırdan farklı döner; tanınmayan migration, eski ikilinin daha yeni bir
  #    şemaya baktığı anlamına gelir ve tam da önlenmek istenen durumdur.
  if maintenance migrate status >/dev/null 2>&1; then
    echo "  ✓ şema eski sürümle uyumlu" >&2
  else
    echo "  ✗ şema eski sürümle uyumlu DEĞİL" >&2
    _vok=1
  fi

  # 2) Uygulama rolüyle gerçek bağlantı. Superuser ile çalışan bir kurulum
  #    "sağlıklı" görünür ama firma izolasyonu veritabanı seviyesinde kalkmıştır.
  if [ -n "$(env_get VARYAONE_APP_DATABASE_URL)" ]; then
    if compose exec -T postgres \
         env PGPASSWORD="$(env_get VARYAONE_APP_DB_PASSWORD)" \
         psql -h 127.0.0.1 -U varyaone_app -d "$(env_get POSTGRES_DB || echo varyaone)" \
         -c 'SELECT 1' >/dev/null 2>&1; then
      echo "  ✓ varyaone_app bağlanabiliyor" >&2
    else
      echo "  ✗ varyaone_app BAĞLANAMIYOR" >&2
      _vok=1
    fi
  fi

  # 3) Dosya deposu. Belgeleri okunamayan bir kurulum çalışır görünür ve her
  #    ek/PDF isteğinde hata verir.
  if compose exec -T api sh -c 'test -w /var/lib/varyaone/storage' >/dev/null 2>&1; then
    echo "  ✓ dosya deposu yazılabilir" >&2
  else
    echo "  ✗ dosya deposuna ERİŞİLEMİYOR" >&2
    _vok=1
  fi

  return $_vok
}

# Deploy geri alma. Yalnız canlı veriye dokunulmadığı KANITLANDIĞINDA çağrılır:
# bekleyen migration yoktu, dolayısıyla eski image'lar eski şemayı bulur.
_rollback_deploy() {
  _rb_op=$1; _rb_stage=$2
  oplog "sonuç: başarısız ($_rb_stage); geri alınıyor"
  echo >&2
  echo "  Geri alınıyor (başarısız aşama: $_rb_stage)..." >&2
  echo "  Bekleyen migration yoktu; veri deploy öncesiyle aynı." >&2
  if restore_release "$_release"; then
    if wait_healthy 120 && _verify_restored; then
      [ -n "$_rb_op" ] && maintenance system phase "$_rb_op" ROLLED_BACK \
        "eski sürüme dönüldü; şema değişmemişti" >/dev/null 2>&1
      echo "  Önceki sürüme dönüldü, doğrulandı ve sağlıklı." >&2
      return 0
    fi
    echo "  Önceki sürüme dönüldü ama doğrulama geçmedi." >&2
  else
    echo "  Önceki sürüme DÖNÜLEMEDİ." >&2
  fi
  # Geri dönüş de başarısız: artık hangi image'ın hangi durumda olduğu belirsiz.
  # Buradan sonrası bakım işidir, tahmin değil.
  _hold_deploy "$_rb_op" "$_rb_stage (geri dönüş de başarısız)"
  return 1
}

# Deploy öncesi yedek almaya değer bir kurulum var mı?
#
# "Belirlenemedi" burada "var" sayılır: yedek almanın maliyeti birkaç saniye,
# almamanın maliyeti geri dönüşü olmayan bir migration.
system_installed_quiet() {
  _is=0; installation_state || _is=$?
  [ "$_is" != 1 ]
}

current_release() {
  git describe --tags --always --dirty 2>/dev/null || git rev-parse --short HEAD 2>/dev/null || echo "unknown"
}


# 127.0.0.1:port/health/ready true olana kadar bekle (<saniye> zaman asimi)
# frontend konteynerinde HTTP dinleyicisi ayakta mi? (api'den compose agi
# uzerinden yoklanir — api'de wget kesin var). wget cikis 8 = sunucu bir hata
# yaniti verdi (3xx/4xx/5xx) ama AYAKTA; cikis 4 = baglanti yok = kapali.
# Frontend gerçekten yanıt veriyor mu? wget'in 8. çıkış kodu "sunucu hata
# yanıtı döndü" demektir — yani 500 de dahil. Bunu sağlıklı saymak, çöken bir
# frontend'i sağlıklı göstermekti; yalnız başarılı yanıt (0) sağlıklıdır.
# wget yönlendirmeleri kendiliğinden izler, dolayısıyla 3xx de 0 döner.
_frontend_healthy() {
  compose exec -T api wget -q -O /dev/null -T 5 http://frontend:3000/ >/dev/null 2>&1
}

wait_healthy() {
  _limit=${1:-180}
  _waited=0
  while [ "$_waited" -lt "$_limit" ]; do
    if compose exec -T api wget -q -O - http://127.0.0.1:8080/health/ready >/dev/null 2>&1 \
       && _frontend_healthy; then
      return 0
    fi
    sleep 5
    _waited=$((_waited + 5))
  done
  return 1
}

# En az <kb> boş alanı olan bir yol var mı? df başarısızsa (yol yok) "sınırsız" say.
_avail_kb_for() {
  df -Pk "$1" 2>/dev/null | awk 'NR==2 {print $4}'
}


# Uygulamadan bağımsız bakım çalıştırıcısı: api image'ından tek seferlik bir
# konteyner. --no-deps migration'ı ve bağımlı servisleri başlatmaz; konteyner
# aynı storage volume'ünü ve aynı DB ağını kullanır. Doğrulama ve geri yükleme
# çalışan bir api konteynerine bağlı olmamalıdır — api bozukken de aynı sonucu
# vermeleri gerekir.
maintenance() {
  compose run --rm --no-deps -T api "$@"
}

# Bakım çalıştırıcısının çalışabilmesi için postgres ayakta olmalı (--no-deps
# onu başlatmaz). 1 = başlatılamadı.
ensure_postgres_up() {
  compose ps --status running postgres 2>/dev/null | grep -q postgres && return 0
  compose up -d postgres >/dev/null 2>&1 || return 1
  _w=0
  while [ "$_w" -lt 60 ]; do
    compose ps --status running postgres 2>/dev/null | grep -q postgres && return 0
    sleep 2; _w=$((_w + 2))
  done
  return 1
}

# İşlem koordinatörünün durumu. 0 = kurulum hizmete alınabilir,
# 1 = alınamaz (yarıda kalmış işlem), 2 = sorulamadı.
#
# Servisleri açmadan önce sorulur. Yarıda kalmış bir geri yüklemeden sonra
# trafiği açmak, hangi yedeğe ait olduğu bilinmeyen bir veriyle çalışmaya
# başlamaktır; her yeni yazma da kurtarmayı zorlaştırır.
system_serviceable() {
  ensure_postgres_up || return 2
  _ssrc=0; _out=$(maintenance system status 2>&1) || _ssrc=$?
  case "$_ssrc" in
    0) return 0 ;;
    *) case "$_out" in
         *'"serviceable"'*) SYSTEM_STATUS_REASON=$(printf '%s' "$_out" | sed -n 's/.*"reason": *"\([^"]*\)".*/\1/p'); return 1 ;;
         *) SYSTEM_STATUS_REASON="işlem durumu sorulamadı"; return 2 ;;
       esac ;;
  esac
}

# Servisleri açmadan önceki son kapı. Koordinatör "hayır" diyorsa açma.
gate_or_stay_down() {
  _ss=0; system_serviceable || _ss=$?
  case "$_ss" in
    0) return 0 ;;
    1)
      echo >&2
      echo "SİSTEM HİZMETE ALINAMAZ — servisler KAPALI bırakıldı." >&2
      echo "  ${SYSTEM_STATUS_REASON:-yarıda kalmış bir sistem işlemi var}" >&2
      echo "  Ayrıntı:   ./deploy.sh system-status" >&2
      echo "  Geçmiş:    ./deploy.sh system-history" >&2
      echo "  İncelenip düzeltildiyse:  ./deploy.sh system-resolve \"ne yapıldı\"" >&2
      return 1
      ;;
    *)
      echo "  UYARI: işlem durumu sorulamadı; servisler açılmıyor." >&2
      return 1
      ;;
  esac
}

# Kurulumda geri yüklemenin yok edebileceği veri var mı? Üç durumlu:
#   0 = var (veya olabilir)   1 = kesin boş   2 = belirlenemedi
# "Belirlenemedi" asla "boş" sayılmaz: bu ayrım güvenlik yedeğinin atlanıp
# atlanmayacağını belirler ve yanlış tarafa düşerse geri yükleme geri alınamaz
# hale gelir. Eski sürüm bunu `migrate status` çıkışına bakarak iki durumlu
# ölçüyordu; api kapalıyken dolu bir kurulum "boş" görünüyordu.
installation_state() {
  ensure_postgres_up || return 2
  _st=$(maintenance migrate status 2>/dev/null)
  case "$_st" in
    "current=0 "*) return 1 ;;
    "current="*)   return 0 ;;
    *)             return 2 ;;
  esac
}


# Uygulanmayı bekleyen migration var mı? Üç durumlu:
#   0 = yok (kanıtlandı)   1 = var   2 = sorulamadı
#
# Bu ayrım deploy'un geri dönüşünü belirler. Bekleyen migration yoksa deploy
# şemaya ve veriye dokunmaz; sonraki bir hata eski image'larla güvenle geri
# alınabilir. Diğer iki durumda alınamaz: eski ikili yeni şemayı anlamayabilir.
# "Sorulamadı" asla "yok" sayılmaz.
pending_migrations() {
  ensure_postgres_up || return 2
  _ms=$(maintenance migrate status 2>/dev/null)
  case "$_ms" in
    *" pending=0 "*) return 0 ;;
    *" pending="*)   return 1 ;;
    *)               return 2 ;;
  esac
}

# .varya dosyasının bütünlüğünü doğrula: var + boş değil + geçerli arşiv +
# motorun kendi sağlama doğrulaması. 0 = sağlam.
#
# Motor doğrulaması koşullu değildir. Daha önce yalnız api konteyneri
# çalışıyorsa yapılıyordu, yani api kapalıyken "ilk tar girdisi manifest.json"
# kontrolünü geçen her dosya doğrulanmış sayılıyordu. Doğrulama aracı
# çalıştırılamıyorsa sonuç "doğrulandı" değil, "doğrulanamadı"dır.
verify_backup_file() {
  _f=$1
  [ -s "$_f" ] || { echo "yedek dosyası yok veya boş: $_f" >&2; return 1; }
  _sz=$(wc -c < "$_f" 2>/dev/null || echo 0)
  [ "${_sz:-0}" -ge 1024 ] || { echo "yedek dosyası şüpheli derecede küçük (${_sz} bayt)" >&2; return 1; }
  _first=$(tar -tf "$_f" 2>/dev/null | head -n1)
  [ "$_first" = "manifest.json" ] || { echo "geçersiz .varya arşivi (ilk giriş: ${_first:-yok})" >&2; return 1; }
  ensure_postgres_up || {
    echo "yedek doğrulanamadı: veritabanı servisi başlatılamadı" >&2; return 1
  }
  if maintenance backup verify - < "$_f" >/dev/null; then
    return 0
  fi
  echo "yedek sağlama doğrulaması başarısız: $_f" >&2
  return 1
}


# check <etiket> ; sonra: pass "detay"  |  fail "neden"  |  warn "not"
# (Türkçe çok baytlı karakterler için hizalama awk length() ile yapılır.)
_ck_label=""
# UTF-8 karakter sayısı: devam baytlarını (0x80-0xBF) atıp kalan baytları say.
_len() { printf %s "$1" | LC_ALL=C tr -d '\200-\277' | wc -c | tr -d ' '; }
_pad() {
  printf '%s' "$1"
  _n=$(( ${2:-30} - $(_len "$1") ))
  while [ "$_n" -gt 0 ]; do printf ' '; _n=$((_n - 1)); done
}
check() { _ck_label=$1; }
_row()  { printf '  %b  ' "$1"; _pad "$_ck_label" 30; printf '  %s\n' "${2:-}"; }
pass()  { _row '\033[32m✓\033[0m' "${1:-}"; }
warn()  { _row '\033[33m!\033[0m' "${1:-}"; DOCTOR_WARNED=1; }
fail()  { _row '\033[31m✗\033[0m' "${1:-}"; DOCTOR_FAILED=1; }

doctor() {
  banner "Ortam Denetimi"
  DOCTOR_FAILED=0
  DOCTOR_WARNED=0

  check "Docker"
  if $DK info >/dev/null 2>&1; then
    pass "$($DK version --format '{{.Server.Version}}' 2>/dev/null)"
  elif [ "$(id -u)" != 0 ] && have sudo && sudo docker info >/dev/null 2>&1; then
    DK="sudo docker"
    pass "$($DK version --format '{{.Server.Version}}' 2>/dev/null) (sudo)"
  else
    fail "çalışmıyor veya erişilemiyor — ./deploy.sh bootstrap"
  fi

  check "Docker Compose"
  if $DK compose version >/dev/null 2>&1; then
    pass "$($DK compose version --short 2>/dev/null)"
  else
    fail "bulunamadı — ./deploy.sh bootstrap"
  fi

  check ".env"
  if [ -f .env ]; then
    if grep -q '^VARYAONE_MASTER_KEY=' .env && ! grep -q '^VARYAONE_MASTER_KEY=replace-' .env; then
      pass "şifreleme anahtarı ayarlı"
    else
      fail "VARYAONE_MASTER_KEY eksik/örnek — ./deploy.sh install"
    fi
  else
    fail "yok — ./deploy.sh install"
  fi

  check "Compose yapılandırması"
  if compose config --quiet 2>/dev/null; then
    pass "geçerli"
  else
    fail "compose config hataları var"
  fi

  check "Disk alanı"
  available_kb=$(df -Pk "$project_dir" | awk 'NR==2 {print $4}')
  available_gb=$(awk "BEGIN{printf \"%.1f\", $available_kb/1048576}")
  if [ "$available_kb" -ge 1048576 ]; then
    pass "${available_gb} GiB boş"
  else
    fail "yalnızca ${available_gb} GiB boş (en az 1 GiB)"
  fi

  check "Kurulum modu"
  if domain_mode; then
    pass "domainli — https://$(env_get VARYAONE_DOMAIN)"
  elif [ -f .env ]; then
    wp=$(env_get VARYAONE_WEB_PORT); pass "domainsiz — http://localhost:${wp:-3000}"
  else
    warn "henüz kurulmadı"
  fi

  if domain_mode; then
    _d=$(env_get VARYAONE_DOMAIN)
    check "DNS ($_d)"
    if resolves_here "$_d"; then
      pass "bu sunucuya işaret ediyor"
    elif [ $? = 2 ]; then
      warn "otomatik doğrulanamadı"
    else
      fail "bu sunucunun IP'sine çözümlenmiyor"
    fi

    # Dosyanın var olması sertifikanın geçerli olduğunu söylemez: süresi
    # geçmiş, başka bir alan adına ait ya da eksik zincirli bir dosya da
    # vardır. Ölçülen şey, sunucunun 443'te gerçekten sunduğu zincirdir.
    check "SSL sertifikası"
    elevate 2>/dev/null || true
    _certdir=$(cert_live_dir "$_d")
    if ! run_root test -s "$_certdir/fullchain.pem" 2>/dev/null; then
      warn "henüz alınmadı — ./deploy.sh install"
    elif ! have openssl; then
      warn "openssl yok — sunulan sertifika doğrulanamadı"
    else
      _served=$(printf '' | openssl s_client -servername "$_d" -connect 127.0.0.1:443 2>/dev/null)
      if [ -z "$_served" ]; then
        fail "443 portunda TLS yanıtı yok"
      elif ! printf '%s' "$_served" | openssl x509 -noout -checkend 0 >/dev/null 2>&1; then
        fail "sunulan sertifikanın süresi dolmuş — ./deploy.sh renew-cert --force"
      elif ! printf '%s' "$_served" | openssl x509 -noout -checkhost "$_d" >/dev/null 2>&1; then
        fail "sunulan sertifika $_d için değil"
      else
        _left=$(cert_days_left "$_d" 2>/dev/null)
        if [ -n "$_left" ] && [ "$_left" -lt 10 ]; then
          warn "${_left} gün kaldı — ./deploy.sh renew-cert"
        else
          pass "geçerli${_left:+ (${_left} gün kaldı)}"
        fi
      fi
    fi

    check "Sertifika yenileme zamanlayıcısı"
    if run_root systemctl is-enabled certbot.timer >/dev/null 2>&1 \
       || run_root systemctl is-enabled certbot-renew.timer >/dev/null 2>&1; then
      pass "systemd timer etkin"
    elif run_root crontab -l 2>/dev/null | grep -q certbot; then
      pass "cron kaydı var"
    elif [ -f /etc/cron.d/certbot ]; then
      pass "/etc/cron.d/certbot"
    else
      # Bir sertifikanın otomatik yenilenmemesi, 90 gün sonra sitenin
      # kapanması demektir — ve o ana kadar hiçbir belirti vermez.
      fail "otomatik yenileme kurulu değil; sertifika 90 günde sona erer"
    fi

    check "nginx"
    if have nginx && run_root nginx -t >/dev/null 2>&1; then
      pass "kurulu, yapılandırma geçerli"
    elif have nginx; then
      fail "yapılandırma geçersiz — sudo nginx -t"
    else
      fail "kurulu değil — ./deploy.sh install"
    fi
  fi

  # Uygulama rolu, api'nin gercekten kullandigi baglantidir. Parolasi eksikse
  # (ornegin majör yükseltme rolu parolasiz yeniden yaratmissa) api hicbir
  # zaman hazir olmaz, ama belirti "api yanit vermiyor" gibi alakasiz gorunur.
  # Once bunu soyle ki operatoru dogru yere gondersin.
  check "Uygulama veritabanı rolü"
  _appurl=$(env_get VARYAONE_APP_DATABASE_URL)
  if [ -z "$_appurl" ]; then
    pass "kullanılmıyor — superuser bağlantısı (izolasyon uygulama yükünde)"
  elif ! compose exec -T postgres pg_isready >/dev/null 2>&1; then
    warn "postgres çalışmıyor — kontrol edilemedi"
  elif compose exec -T postgres \
      env PGPASSWORD="$(env_get VARYAONE_APP_DB_PASSWORD)" \
      psql -h 127.0.0.1 -U varyaone_app -d "$(env_get POSTGRES_DB || echo varyaone)" \
      -c 'SELECT 1' >/dev/null 2>&1; then
    pass "varyaone_app giriş yapabiliyor"
  else
    fail "varyaone_app giriş YAPAMIYOR — api bu yüzden hazır olmaz. Düzelt: ./deploy.sh repair-app-role"
  fi

  check "api sağlığı"
  if compose exec -T api wget -q -O /dev/null -T 10 http://127.0.0.1:8080/health/ready >/dev/null 2>&1; then
    pass "/health/ready → ok"
  else
    fail "api hazır değil — docker compose logs --tail=60 api"
  fi

  # wget'in 8. çıkış kodu "sunucu hata yanıtı döndü" demektir; bunu sağlıklı
  # saymak çöken bir frontend'i sağlıklı göstermekti.
  check "frontend sağlığı"
  if _frontend_healthy; then
    pass "yanıt veriyor"
  else
    fail "frontend yanıt vermiyor — docker compose logs --tail=60 frontend"
  fi

  check "worker"
  if compose ps --status running worker 2>/dev/null | grep -q worker; then
    _restarts=$($DK inspect --format '{{.RestartCount}}' varyaone-worker-1 2>/dev/null)
    if [ -n "$_restarts" ] && [ "$_restarts" -gt 5 ]; then
      fail "sürekli yeniden başlıyor (${_restarts} kez) — docker compose logs worker"
    else
      pass "çalışıyor"
    fi
  else
    fail "çalışmıyor — docker compose logs --tail=60 worker"
  fi

  # Yarıda kalmış bir işlem, sağlıklı görünen bir kurulumda bile trafiğin
  # açılmaması gereken tek nedendir.
  check "Sistem işlem durumu"
  _ss=0; system_serviceable || _ss=$?
  case "$_ss" in
    0) pass "bekleyen işlem yok" ;;
    1) fail "${SYSTEM_STATUS_REASON:-yarıda kalmış işlem} — ./deploy.sh system-status" ;;
    *) warn "sorulamadı (servisler kapalı olabilir)" ;;
  esac

  check "Depolama"
  if compose exec -T api sh -c 'test -w /var/lib/varyaone/storage' >/dev/null 2>&1; then
    pass "yazılabilir"
  elif compose ps --status running api 2>/dev/null | grep -q api; then
    fail "depolama yazılamıyor — yüklenen dosyalar kaydedilemez"
  else
    warn "api çalışmıyor — kontrol edilemedi"
  fi

  check "Çalışan sürüm"
  pass "$(current_release)"

  check "Yedek dizini"
  if [ -d "$project_dir/backups" ]; then
    _bcount=$(find "$project_dir/backups" -maxdepth 1 -name '*.varya' 2>/dev/null | wc -l | tr -d ' ')
    _bsize=$(du -sh "$project_dir/backups" 2>/dev/null | awk '{print $1}')
    _bfree_kb=$(_avail_kb_for "$project_dir/backups")
    if [ -n "$_bfree_kb" ] && [ "$_bfree_kb" -lt 2097152 ]; then
      warn "${_bcount} yedek, ${_bsize:-?} — disk az kaldı; eski .varya dosyalarını silin"
    else
      pass "${_bcount} yedek, ${_bsize:-?}"
    fi
  else
    warn "henüz yedek alınmadı — ./deploy.sh backup"
  fi

  echo
  # Çıkış kodu sözleşmesi: 0 = her şey yolunda, 1 = başarısız kontrol var,
  # 2 = yalnız uyarı. Bir izleme betiği bunlara güvenebilmelidir; "uyarı var
  # ama sonunda tüm kontroller başarılı" diyen bir çıktı buna izin vermezdi.
  if [ "$DOCTOR_FAILED" = 1 ]; then
    echo "  BAŞARISIZ — yukarıdaki ✗ satırlarını giderin." >&2
    exit 1
  fi
  if [ "${DOCTOR_WARNED:-0}" = 1 ]; then
    echo "  Uyarılarla tamam — yukarıdaki ! satırlarına bakın."
    exit 2
  fi
  echo "  Tüm kontroller başarılı."
}

# Tam (eksiksiz) bir yedek al ve <dosya>'ya yaz.
#
# Yükseltme sırası gereği bu betik, HENÜZ DERLENMEMİŞ sürümün değil, o an
# çalışan sürümün ikilisini çağırır. Yeni eklenen bir seçeneği koşulsuz
# kullanmak, her yükseltmeyi ilk adımda kırardı. Bu yüzden seçenek denenir ve
# tanınmıyorsa onsuz devam edilir — ama sessizce değil: eski ikili eksiksizlik
# güvencesini veremiyorsa bu söylenir.
# Kullanım: backup_create_complete <hedef dosya>
backup_create_complete() {
  _target=$1
  _err="${_target}.err"
  if maintenance backup create - --require-complete > "$_target" 2>"$_err"; then
    rm -f "$_err"
    return 0
  fi
  if grep -q 'bilinmeyen seçenek: --require-complete' "$_err" 2>/dev/null; then
    echo "  Not: çalışan sürüm eksiksizlik denetimini desteklemiyor; yedek alınıyor." >&2
    rm -f "$_err"
    maintenance backup create - > "$_target" || return 1
    return 0
  fi
  cat "$_err" >&2
  rm -f "$_err"
  return 1
}

# Yedeği güvenli biçimde yayımla: benzersiz geçici ad -> doğrulama -> nihai ad.
# Nihai ad ancak dosya tam yazılıp doğrulandıktan sonra ortaya çıkar; yarıda
# kalan bir yedek hiçbir zaman gerçek bir yedek gibi görünmez.
# Kullanım: publish_backup <geçici dosya> <nihai dosya>
publish_backup() {
  _tmp=$1; _final=$2
  if ! verify_backup_file "$_tmp"; then
    rm -f "$_tmp"
    return 1
  fi
  # Aynı saniyedeki ikinci bir işlem mevcut yedeği ezmesin.
  if [ -e "$_final" ]; then
    _n=2
    while [ -e "${_final%.varya}-$_n.varya" ]; do _n=$((_n + 1)); done
    _final="${_final%.varya}-$_n.varya"
  fi
  mv "$_tmp" "$_final" || { rm -f "$_tmp"; return 1; }
  chmod 600 "$_final" 2>/dev/null || true
  # Sağlama yan dosyası yalnız dosya adını içersin: tam kaynak yolu yazılırsa
  # yedek başka bir sunucuya taşındığında doğrulama var olmayan yolu arar.
  if ( cd "$(dirname "$_final")" && sha256sum "$(basename "$_final")" > ".$(basename "$_final").sha256.tmp" ) 2>/dev/null; then
    mv "$(dirname "$_final")/.$(basename "$_final").sha256.tmp" "$_final.sha256"
    chmod 600 "$_final.sha256" 2>/dev/null || true
  else
    rm -f "$(dirname "$_final")/.$(basename "$_final").sha256.tmp"
    echo "  UYARI: sha256sum yok — sağlama yan dosyası yazılamadı." >&2
  fi
  PUBLISHED_BACKUP=$_final
  return 0
}

# Yedek dizinini hazırla. 0700: yedek bütün kurulumun verisini içerir, host
# umask'ı 022 olduğunda dizin herkese okunur hale gelirdi.
ensure_backup_dir() {
  mkdir -p "$project_dir/backups" || return 1
  chmod 700 "$project_dir/backups" 2>/dev/null || true
  return 0
}

backup() {
  require_docker
  oplog_start backup "$@"
  [ -f .env ] || { echo ".env bulunamadı." >&2; exit 1; }
  acquire_lock update
  trap 'release_lock' EXIT INT TERM
  # Yedek dosyaları yalnız sahibine okunabilir olmalı; host umask'ına güvenme.
  umask 077
  ensure_backup_dir || { echo "backups dizini oluşturulamadı." >&2; exit 1; }
  ensure_postgres_up || { echo "veritabanı servisi başlatılamadı." >&2; exit 1; }

  timestamp=$(date -u +%Y%m%dT%H%M%SZ)
  out="$project_dir/backups/varyaone-$timestamp.varya"
  tmp=$(mktemp "$project_dir/backups/.varyaone-$timestamp.XXXXXX") || {
    echo "geçici yedek dosyası oluşturulamadı." >&2; exit 1
  }
  echo "Tam sistem yedeği alınıyor (veritabanı + dosyalar)..."
  # stdout yalnız arşivdir; motorun günlükleri stderr'e gider.
  if ! maintenance backup create - > "$tmp"; then
    rm -f "$tmp"
    echo "Yedek başarısız." >&2
    exit 1
  fi
  publish_backup "$tmp" "$out" || { echo "Yedek doğrulanamadı; dosya silindi." >&2; exit 1; }
  size=$(du -h "$PUBLISHED_BACKUP" 2>/dev/null | awk '{print $1}')
  echo "Yedek oluşturuldu ve doğrulandı: $PUBLISHED_BACKUP${size:+ ($size)}"
}

restore() {
  require_docker
  file=${1:-}
  shift 2>/dev/null || true
  confirm=""
  force=""
  skip_safety=0
  for arg in "$@"; do
    case "$arg" in
      --confirm) confirm=1 ;;
      --force) force="--force" ;;
      # Açık riskli acil kurtarma yolu: yalnız mevcut kurulum zaten
      # kurtarılamayacak kadar bozuksa ve güvenlik yedeği alınamıyorsa.
      # --confirm ve --force bu istisnayı kapsamaz; ayrı ve isimli olması
      # gerekir, çünkü sonucu geri alınamaz.
      --skip-safety-backup) skip_safety=1 ;;
      *) echo "Bilinmeyen seçenek: $arg" >&2; exit 2 ;;
    esac
  done
  [ -n "$file" ] && [ -f "$file" ] || { echo "Geçerli bir .varya dosyası verin." >&2; exit 1; }
  [ "$confirm" = "1" ] || { echo "Geri yükleme mevcut veriyi SİLER. Onaylayın:" >&2
    echo "  ./deploy.sh restore \"$file\" --confirm" >&2; exit 1; }

  oplog_start restore "$file"
  acquire_lock update
  trap 'release_lock' EXIT INT TERM
  umask 077

  if [ -f "$file.sha256" ]; then
    echo "Sağlama doğrulanıyor..."
    (cd "$(dirname "$file")" && sha256sum -c "$(basename "$file").sha256") \
      || { echo "Sağlama doğrulaması başarısız — dosya bozuk." >&2; exit 1; }
  else
    echo "  UYARI: $file.sha256 yok — yalnız arşivin iç bütünlüğü doğrulanacak." >&2
  fi
  # Arşivin kendi manifest sağlamaları. Bu adım koşullu değildir: doğrulanamayan
  # bir arşivle geri yükleme başlatılmaz.
  oplog "aşama: arşiv doğrulaması"
  echo "Arşiv bütünlüğü doğrulanıyor..."
  verify_backup_file "$file" || { echo "Arşiv doğrulaması başarısız — geri yükleme durduruldu." >&2; exit 1; }

  ensure_backup_dir || { echo "backups dizini oluşturulamadı — geri yükleme durduruldu." >&2; exit 1; }

  # Geri yükleme yıkıcı: önce mevcut durumun doğrulanmış güvenlik yedeğini al.
  # Bu bir uyarı değil, bir kapıdır — başarısız bir güvenlik yedeğinden sonra
  # devam etmek, geri dönüşü olmayan bir işlemi geri dönüş noktası olmadan
  # yapmak demektir.
  _safety=""
  _state=0; installation_state || _state=$?
  case "$_state" in
    1)
      echo "Kurulumda veri yok; güvenlik yedeği gerekmiyor."
      ;;
    0|2)
      if [ "$_state" = 2 ]; then
        echo "  Mevcut kurulumun durumu belirlenemedi; veri varmış gibi davranılıyor." >&2
      fi
      if [ "$skip_safety" = 1 ]; then
        echo "  UYARI: --skip-safety-backup verildi; geri dönüş noktası OLMADAN devam ediliyor." >&2
      else
        _safety_final="$project_dir/backups/pre-restore-$(date -u +%Y%m%dT%H%M%SZ).varya"
        echo "Güvenlik yedeği alınıyor: $(basename "$_safety_final")"
        _safety_tmp=$(mktemp "$project_dir/backups/.pre-restore.XXXXXX") || _safety_tmp=""
        # Bu yedek geri dönüş noktasıdır: eksik bir yedeğin eksik olduğu,
        # ancak ona ihtiyaç duyulduğu anda anlaşılır.
        if [ -n "$_safety_tmp" ] && backup_create_complete "$_safety_tmp" \
           && publish_backup "$_safety_tmp" "$_safety_final"; then
          _safety=$PUBLISHED_BACKUP
        else
          rm -f "$_safety_tmp"
          echo "Güvenlik yedeği alınamadı — geri yükleme durduruldu; sistem değişmedi." >&2
          echo "  Mevcut kurulum zaten kurtarılamaz durumdaysa riski kabul ederek:" >&2
          echo "  ./deploy.sh restore \"$file\" --confirm --skip-safety-backup" >&2
          exit 1
        fi
      fi
      ;;
  esac

  # Bütün yazıcılar dursun — api dahil. Eskiden yalnız worker/frontend
  # durduruluyordu; domainsiz kurulumda api dışarıdan erişilebilir kalıyor ve
  # geri yükleme sürerken yazmaya devam edebiliyordu.
  oplog "aşama: servisler durduruluyor"
  echo "Geri yükleniyor (tüm servisler durduruluyor)..."
  if ! compose stop api worker frontend >/dev/null 2>&1; then
    echo "Servisler durdurulamadı — geri yükleme durduruldu; sistem değişmedi." >&2
    exit 1
  fi
  ensure_postgres_up || { echo "veritabanı servisi başlatılamadı." >&2; exit 1; }

  # Çıkış kodu aşağıdaki dallarda ayrıştırılıyor; `set -e` altında çıplak
  # çağrı başarısız olduğunda betik o dallara hiç ulaşmadan biterdi.
  _rc=0; maintenance backup restore - $force < "$file" || _rc=$?
  case "$_rc" in
    0)
      # Motor "tamam" dedi; koordinatörün kaydı da tamam demeli. İkisi
      # ayrışıyorsa açma — kayıt, motorun kendi raporundan daha uzun yaşar.
      gate_or_stay_down || exit 3
      compose up -d --force-recreate api >/dev/null 2>&1 \
        || { echo "Geri yükleme tamamlandı ancak api başlatılamadı." >&2; exit 1; }
      compose up -d worker frontend >/dev/null 2>&1 \
        || { echo "Geri yükleme tamamlandı ancak worker/frontend başlatılamadı." >&2; exit 1; }
      reload_nginx_soft
      oplog "sonuç: başarılı"
      echo "Geri yükleme tamamlandı. ./deploy.sh doctor ile doğrulayın."
      [ -n "$_safety" ] && echo "  Önceki durumun yedeği: $_safety"
      ;;
    3)
      # varyaone çıkış kodu 3: veritabanı değişti, geçiş tamamlanamadı. Servisleri
      # açmak, hangi yedeğe ait olduğu bilinmeyen bir veriyle trafiğe çıkmaktır.
      echo >&2
      oplog "sonuç: SİSTEM TUTARSIZ; servisler kapalı"
      echo "SİSTEM TUTARSIZ DURUMDA — servisler KAPALI bırakıldı." >&2
      echo "  Veritabanı değişti ancak geri yükleme tamamlanamadı." >&2
      echo "  Servisleri elle açmayın; önce durumu inceleyin." >&2
      [ -n "$_safety" ] && echo "  Geri dönüş için: ./deploy.sh restore \"$_safety\" --confirm" >&2
      exit 3
      ;;
    *)
      # Motor veritabanı işlemine başlamadan durmuş olmalı — ama bunu motorun
      # çıkış kodundan değil, kalıcı kayıttan doğrula. Çıkış kodu, süreç
      # öldüğünde hiç üretilmemiş olabilir; kayıt her koşulda diskte kalır.
      if ! gate_or_stay_down; then
        echo "Geri yükleme başarısız ve sistem durumu doğrulanamadı." >&2
        [ -n "$_safety" ] && echo "  Geri dönüş için: ./deploy.sh restore \"$_safety\" --confirm" >&2
        exit 3
      fi
      compose up -d api worker frontend >/dev/null 2>&1 || true
      reload_nginx_soft
      echo "Geri yükleme başarısız; sistem değişmedi (farklı MASTER_KEY veya daha yeni sürüm ise --force gerekebilir)." >&2
      [ -n "$_safety" ] && echo "  Önceki durumun yedeği: $_safety" >&2
      exit 1
      ;;
  esac
}

# Yarıda kalmış bir işlemi çözülmüş olarak işaretle ve servisleri aç.
# Not zorunludur: elle kurtarılmış bir kurulumun ne yapıldığını kaydetmesi,
# aynı sorun tekrar ettiğinde elde kalan tek bilgidir.
system_resolve() {
  [ -n "${1:-}" ] || {
    echo "Kullanım: ./deploy.sh system-resolve \"ne yapıldığını anlatan not\"" >&2
    exit 2
  }
  acquire_lock update
  trap 'release_lock' EXIT INT TERM
  ensure_postgres_up || { echo "veritabanı servisi başlatılamadı." >&2; exit 1; }
  maintenance system resolve "$@" || exit 1
  echo "Servisler açılıyor..."
  compose up -d api worker frontend >/dev/null 2>&1 || true
  # `up -d` yeniden başlatma döngüsündeki bir konteyner için hata dönebilir;
  # asıl ölçü sağlık kontrolüdür, komutun anlık çıkış kodu değil.
  if wait_healthy 120; then
    reload_nginx_soft
    echo "Tamamlandı. ./deploy.sh doctor ile doğrulayın."
  else
    echo "Servisler açıldı ama sağlık kontrolü geçmedi; ./deploy.sh doctor ile bakın." >&2
    exit 1
  fi
}

# Saklanan eski bir veritabanını canlıya al (geri yükleme sonrası geri dönüş).
#
# Trafiğe açıldıktan sonra yapılan bir geri dönüş, o andan beri yazılmış her
# şeyi kaybettirir. Bu yüzden önce mevcut durumun yedeği alınır ve işlem açıkça
# onaylanır: kaybedilecek şey, kaybedilmeden önce bir dosyaya yazılmış olur.
system_promote() {
  _db=${1:-}
  _confirm=${2:-}
  [ -n "$_db" ] || {
    echo "Kullanım: ./deploy.sh system-promote <veritabanı> --confirm" >&2
    echo "Saklananları görmek için: ./deploy.sh system-retained" >&2
    exit 2
  }
  [ "$_confirm" = "--confirm" ] || {
    echo "Bu işlem mevcut veritabanını kenara alıp $_db'yi canlıya alır." >&2
    echo "Geri yüklemeden sonra yazılan veriler canlı olmaktan çıkar. Onaylayın:" >&2
    echo "  ./deploy.sh system-promote \"$_db\" --confirm" >&2
    exit 1
  }
  oplog_start promote "$_db"
  acquire_lock update
  trap 'release_lock' EXIT INT TERM
  umask 077
  ensure_postgres_up || { echo "veritabanı servisi başlatılamadı." >&2; exit 1; }
  ensure_backup_dir || exit 1

  _pp_final="$project_dir/backups/pre-promote-$(date -u +%Y%m%dT%H%M%SZ).varya"
  _pp_tmp=$(mktemp "$project_dir/backups/.pre-promote.XXXXXX") || _pp_tmp=""
  echo "Mevcut durumun yedeği alınıyor..."
  if [ -n "$_pp_tmp" ] && backup_create_complete "$_pp_tmp" && publish_backup "$_pp_tmp" "$_pp_final"; then
    echo "  Yedek: $PUBLISHED_BACKUP"
  else
    rm -f "$_pp_tmp"
    echo "Mevcut durumun yedeği alınamadı; işlem durduruldu." >&2
    exit 1
  fi

  echo "Servisler durduruluyor..."
  compose stop api worker frontend >/dev/null 2>&1 || true
  if maintenance system promote "$_db"; then
    oplog "sonuç: başarılı ($_db)"
    gate_or_stay_down || exit 3
    compose up -d api worker frontend >/dev/null 2>&1 || true
    wait_healthy 120 && echo "Tamamlandı. ./deploy.sh doctor ile doğrulayın."
  else
    oplog "sonuç: başarısız"
    echo "Geri dönüş başarısız; servisler kapalı bırakıldı." >&2
    echo "  ./deploy.sh system-status" >&2
    exit 3
  fi
}

# --- yedek dosyası saklama --------------------------------------------------
# backups/ tek diskte büyür ve dolduğunda yeni yedek alınamaz — yani yedekleme,
# tam da ihtiyaç duyulacağı dönemde sessizce durur.
#
# Budama kuralları, yedeklerin var olma nedenini bozmayacak biçimde:
#   - Her zaman en yeni N tam yedek korunur.
#   - Koruma penceresinden yeni hiçbir şey silinmez.
#   - pre-restore-* ve pre-deploy-* dosyaları, kendilerinden sonra gelen bir
#     işlem varsa geri dönüş noktasıdır; ayrı ve daha uzun süre korunurlar.
#   - Doğrulanamayan bir dosya silinmez: bozuk olabilir, ama elde kalan tek
#     kopya da olabilir ve kararı operatör verir.
BACKUP_KEEP=${VARYAONE_BACKUP_KEEP:-7}
BACKUP_KEEP_DAYS=${VARYAONE_BACKUP_KEEP_DAYS:-30}
BACKUP_KEEP_SAFETY_DAYS=${VARYAONE_BACKUP_KEEP_SAFETY_DAYS:-90}

prune_backups() {
  _dry=0
  [ "${1:-}" = "--dry-run" ] && _dry=1
  [ -d "$project_dir/backups" ] || { echo "backups dizini yok."; return 0; }

  # Çözülmemiş bir işlem varsa hiçbir şey silinmez. Kurtarmayı bekleyen bir
  # kurulumda silinebilecek en eski dosya, tam da geri dönülecek olan deploy
  # öncesi yedek olabilir. Retention, kurtarma malzemesinin önüne geçmez.
  _keep_safety=0
  if [ "$_dry" = 0 ]; then
    _ss=0; system_serviceable || _ss=$?
    case "$_ss" in
      1)
        echo "Yarıda kalmış bir sistem işlemi var; hiçbir yedek silinmiyor." >&2
        echo "  ${SYSTEM_STATUS_REASON:-}" >&2
        echo "  ./deploy.sh system-status" >&2
        return 1
        ;;
      2)
        # Durum sorulamadı. Rutin budamayı engellemeye gerek yok, ama geri
        # dönüş noktalarına dokunmak için "muhtemelen iyidir" yeterli değil.
        echo "  Not: işlem durumu sorulamadı; geri dönüş yedekleri korunuyor." >&2
        _keep_safety=1
        ;;
    esac
  fi

  _now=$(date +%s)
  _kept=0
  # En yeniden eskiye doğru: sayaç korumasını doğru uygulamak için sıra önemli.
  ls -1t "$project_dir/backups"/*.varya 2>/dev/null | while read -r _f; do
    [ -f "$_f" ] || continue
    _base=$(basename "$_f")
    _age_days=0
    _mtime=$(stat -c %Y "$_f" 2>/dev/null || stat -f %m "$_f" 2>/dev/null || echo "$_now")
    _age_days=$(( (_now - _mtime) / 86400 ))

    case "$_base" in
      pre-restore-*|pre-deploy-*|pre-promote-*)
        # Geri dönüş noktaları: yalnız uzun pencere geçtiyse.
        [ "$_keep_safety" = 1 ] && continue
        if [ "$_age_days" -gt "$BACKUP_KEEP_SAFETY_DAYS" ]; then
          if [ "$_dry" = 1 ]; then echo "silinecek (geri dönüş, ${_age_days}g): $_base"
          else rm -f "$_f" "$_f.sha256" && echo "silindi: $_base"; fi
        fi
        continue
        ;;
    esac

    _kept=$((_kept + 1))
    [ "$_kept" -le "$BACKUP_KEEP" ] && continue
    [ "$_age_days" -le "$BACKUP_KEEP_DAYS" ] && continue
    if [ "$_dry" = 1 ]; then echo "silinecek (${_age_days}g): $_base"
    else rm -f "$_f" "$_f.sha256" && echo "silindi: $_base"; fi
  done

  _left=$(find "$project_dir/backups" -maxdepth 1 -name '*.varya' 2>/dev/null | wc -l | tr -d ' ')
  echo "kalan yedek: $_left"
}

# --- kurtarma paketi --------------------------------------------------------
# Yeni bir sunucuda sıfırdan kurtarmak için gereken her şey, tek yerde.
#
# Yalnız .varya dosyası yeterli değildir: şifreli alanlar VARYAONE_MASTER_KEY
# olmadan çözülemez, ve anahtar yedeğin içinde değildir (olsaydı yedeği çalan
# herkes veriyi de okurdu). Paket bu yüzden iki parçadır ve gizli olan parça
# ayrı tutulur — ayrı tutulması, birlikte kaybedilmemeleri içindir.
recovery_bundle() {
  _out=${1:-}
  [ -n "$_out" ] || {
    echo "Kullanım: ./deploy.sh recovery-bundle <hedef dizin>" >&2
    exit 2
  }
  require_docker
  [ -f .env ] || { echo ".env bulunamadı." >&2; exit 1; }
  umask 077
  mkdir -p "$_out" || exit 1
  chmod 700 "$_out" 2>/dev/null || true

  _stamp=$(date -u +%Y%m%dT%H%M%SZ)
  _dir="$_out/varyaone-recovery-$_stamp"
  mkdir -p "$_dir" || exit 1
  chmod 700 "$_dir" 2>/dev/null || true

  echo "Kurtarma paketi hazırlanıyor: $_dir"

  # 1) Doğrulanmış tam yedek.
  _tmp=$(mktemp "$_dir/.backup.XXXXXX") || exit 1
  echo "  Yedek alınıyor..."
  if ! backup_create_complete "$_tmp"; then
    rm -f "$_tmp"; echo "  Yedek alınamadı." >&2; exit 1
  fi
  if ! publish_backup "$_tmp" "$_dir/varyaone-$_stamp.varya"; then
    echo "  Yedek doğrulanamadı." >&2; exit 1
  fi

  # 2) Gizli olmayan kurtarma bilgisi. Bu dosya paylaşılabilir.
  {
    echo "# Varya One kurtarma bilgisi — $_stamp"
    echo "# Gizli değer İÇERMEZ; gizli olanlar secrets.env dosyasındadır."
    echo "release=$(current_release)"
    echo "commit=$(git -C "$project_dir" rev-parse HEAD 2>/dev/null || echo bilinmiyor)"
    echo "domain=$(env_get VARYAONE_DOMAIN)"
    echo "web_port=$(env_get VARYAONE_WEB_PORT)"
    echo "api_port=$(env_get VARYAONE_API_PORT)"
    echo "web_origin=$(env_get VARYAONE_WEB_ORIGIN)"
    echo "storage_provider=$(env_get VARYAONE_STORAGE_PROVIDER)"
    echo "postgres_db=$(env_get POSTGRES_DB)"
    echo "postgres_user=$(env_get POSTGRES_USER)"
    for _svc in api worker frontend migrate; do
      echo "image_${_svc}=$(_image_id "varyaone-${_svc}:latest")"
    done
  } > "$_dir/recovery.txt"
  chmod 600 "$_dir/recovery.txt" 2>/dev/null || true

  # 3) Gizli değerler. AYRI dosya, ayrı saklanmalı.
  {
    echo "# GİZLİ — bu dosyayı yedekle aynı yerde saklamayın."
    echo "# VARYAONE_MASTER_KEY olmadan yedekteki şifreli alanlar çözülemez."
    echo "# Anahtar kaybolursa parmak izinden yeniden üretilemez."
    grep -E '^(VARYAONE_MASTER_KEY|POSTGRES_PASSWORD|VARYAONE_APP_DB_PASSWORD)=' "$project_dir/.env" 2>/dev/null
  } > "$_dir/secrets.env"
  chmod 600 "$_dir/secrets.env" 2>/dev/null || true

  cp "$project_dir/compose.yaml" "$_dir/compose.yaml" 2>/dev/null || true
  if domain_mode && [ -f "$NGINX_CONF_DIR/$(env_get VARYAONE_DOMAIN).conf" ]; then
    cp "$NGINX_CONF_DIR/$(env_get VARYAONE_DOMAIN).conf" "$_dir/nginx.conf" 2>/dev/null || true
  fi

  cat > "$_dir/BENİOKU.txt" <<'EOF'
Varya One — yeni bir sunucuda kurtarma

1. Depoyu klonlayın ve bu paketteki recovery.txt içindeki commit'e geçin.
2. ./deploy.sh install ile kurulumu yapın (aynı alan adı/port ile).
3. Kurulum bittikten sonra .env içindeki şu değerleri secrets.env'dekilerle
   DEĞİŞTİRİN — özellikle VARYAONE_MASTER_KEY:
     VARYAONE_MASTER_KEY, POSTGRES_PASSWORD
   Anahtar farklı olursa şifreli alanlar okunamaz; yedek "geri yüklendi"
   görünür ama içindeki bazı veriler açılamaz.
4. ./deploy.sh rebuild
5. ./deploy.sh restore <bu paketteki .varya dosyası> --confirm
6. ./deploy.sh doctor

Provası yapılmamış bir kurtarma planı, plan değildir: bu adımları gerçek
veriyle en az bir kez deneyin.
EOF

  echo
  echo "Kurtarma paketi hazır: $_dir"
  echo "  • varyaone-$_stamp.varya   — doğrulanmış tam yedek"
  echo "  • recovery.txt             — gizli olmayan kurulum bilgisi"
  echo "  • secrets.env              — GİZLİ: ana anahtar ve parolalar"
  echo "  • BENİOKU.txt              — adım adım kurtarma"
  echo
  echo "  secrets.env dosyasını yedekle AYNI yerde saklamayın. İkisini birlikte"
  echo "  kaybetmek, verinin kurtarılamaz olması demektir."
}

uninstall() {
  keep_backups=0
  purge=0
  confirm=0
  assume_yes=0
  for arg in "$@"; do
    case "$arg" in
      --confirm) confirm=1 ;;
      --keep-backups) keep_backups=1 ;;
      --purge) purge=1 ;;
      --yes|-y) assume_yes=1 ;;
      *) echo "Bilinmeyen seçenek: $arg" >&2; usage ;;
    esac
  done

  banner "Kaldırma"

  # --keep-backups + --purge birbiriyle çelişir: backups/ proje dizininin
  # içindedir, dolayısıyla purge korunması istenen yedekleri de siler. Bunu
  # sessizce yapmak yerine başlamadan reddet.
  if [ "$keep_backups" = 1 ] && [ "$purge" = 1 ]; then
    echo "  --keep-backups ile --purge birlikte kullanılamaz: backups/ proje" >&2
    echo "  dizininin içindedir ve purge onu da siler." >&2
    echo "  Yedekleri önce dışarı taşıyın:" >&2
    echo "    mv \"$project_dir/backups\" /başka/bir/yer/" >&2
    exit 2
  fi

  # Silinecek kök gerçekten bir Varya One kurulumu mu? VARYAONE_PROJECT_DIR ile
  # yanlış (veya boş) bir yol verildiğinde rm -rf'in nereye ineceği buna bağlı.
  _root=$(CDPATH= cd -- "$project_dir" 2>/dev/null && pwd -P) || _root=""
  case "$_root" in
    ""|"/"|"$HOME") echo "  Güvenli olmayan proje dizini: ${_root:-<çözümlenemedi>}" >&2; exit 2 ;;
  esac
  [ -f "$_root/compose.yaml" ] || {
    echo "  $_root bir Varya One kurulumu gibi görünmüyor (compose.yaml yok)." >&2
    exit 2
  }
  project_dir=$_root

  echo "  Bu işlem KALICI olarak şunları siler:"
  echo "    • Tüm Varya One konteynerleri ve ağları"
  echo "    • Docker volume'leri — PostgreSQL veritabanı ve yüklenen dosyalar DAHİL"
  echo "    • Derlenen image'lar (varyaone-*) + çekilen postgres image'ı"
  echo "    • Üretilen .env (+ geçici kopyaları), host nginx server bloğu, kilitler"
  [ "$keep_backups" = 1 ] || echo "    • backups/ dizini (tüm .varya yedekleri)"
  [ "$purge" = 1 ] && echo "    • proje dizininin kendisi: $project_dir"
  echo

  if [ "$confirm" != 1 ]; then
    echo "  Onay gerekli:  ./deploy.sh uninstall --confirm" >&2
    exit 1
  fi
  if [ "$assume_yes" != 1 ]; then
    interactive || { echo "  Etkileşimsiz çalıştırma için --yes gerekli." >&2; exit 2; }
    ask_yesno "  Gerçekten her şeyi kaldırmak istiyor musunuz?" h || { echo "  İptal edildi." >&2; exit 1; }
    if [ "$purge" = 1 ]; then
      ask_yesno "  Proje dizini de ($project_dir) silinecek — emin misiniz?" h || { echo "  İptal edildi." >&2; exit 1; }
    fi
  fi

  # 1) Konteynerler + volume'ler + ağlar + yerel image'lar (varsa compose).
  if $DK compose version >/dev/null 2>&1 || { [ "$(id -u)" != 0 ] && have sudo && sudo docker compose version >/dev/null 2>&1 && DK="sudo docker"; }; then
    echo "  Konteynerler, volume'ler ve ağlar kaldırılıyor..."
    # Eski (Docker içi nginx/certbot) kurulumdan kalmış olabilecek konteynerler.
    $DK rm -f varyaone-nginx-1 varyaone-certbot-1 >/dev/null 2>&1 || true
    if ! $DK compose -f compose.yaml down --volumes --remove-orphans --rmi local >/dev/null 2>&1; then
      echo "  UYARI: compose down başarısız; kalan kaynaklar tek tek denenecek." >&2
    fi
    # Compose 'down -v' yalnızca anonim/harici olmayan volume'leri siler; kalanları temizle.
    for _v in varyaone_postgres-data varyaone_storage-data varyaone_letsencrypt varyaone_certbot-webroot; do
      $DK volume rm "$_v" >/dev/null 2>&1 || true
    done
    for _svc in api worker frontend migrate backend-tests; do
      $DK image rm "varyaone-${_svc}:latest" "varyaone-${_svc}:preupdate" >/dev/null 2>&1 || true
      # Deploy geri dönüş etiketleri.
      $DK image ls -q "varyaone-${_svc}" --filter 'reference=*:rollback-*' 2>/dev/null \
        | while read -r _img; do [ -n "$_img" ] && $DK image rm -f "$_img" >/dev/null 2>&1 || true; done
    done
    # compose dosyalarının çektiği sabitlenmiş üçüncü taraf image'lar (nginx,
    # certbot, postgres). Yalnızca bu yığın kullandığı için kaldırılıyor;
    # başka bir şey referans veriyorsa `image rm` zaten sessizce başarısız olur.
    for _img in nginx:1.27-alpine certbot/certbot:latest postgres:18.4-alpine; do
      $DK image rm "$_img" >/dev/null 2>&1 || true
    done
    # Gerçekten temizlendi mi? "Komutlar çalıştı" ile "veri gitti" aynı şey
    # değil: kullanımdaki bir volume silinmez ve `volume rm` sessizce başarısız
    # olur. Anahtarı silip silmemek buna bağlı olduğu için sonucu ölç.
    DOCKER_CLEANUP_OK=1
    _left=$($DK volume ls -q 2>/dev/null | grep -c '^varyaone_' || true)
    if [ "${_left:-0}" -gt 0 ]; then
      DOCKER_CLEANUP_OK=0
      echo "  UYARI: $_left Varya One volume'ü kaldırılamadı (kullanımda olabilir)." >&2
    fi
    _left=$($DK ps -aq --filter 'name=varyaone' 2>/dev/null | grep -c . || true)
    if [ "${_left:-0}" -gt 0 ]; then
      DOCKER_CLEANUP_OK=0
      echo "  UYARI: $_left Varya One konteyneri kaldırılamadı." >&2
    fi
  else
    DOCKER_CLEANUP_OK=0
    echo "  Docker erişilemiyor — konteyner/volume temizliği yapılamadı." >&2
  fi

  # 2) Üretilen dosyalar.
  echo "  Üretilen dosyalar siliniyor..."
  rm -f "$NGINX_CONF_DIR"/*.conf
  # Host nginx'e yazılan server bloğu + dummy sertifika (sertifikaların kendisi
  # /etc/letsencrypt'te bırakılır — başka bir şey kullanıyor olabilir).
  remove_host_nginx
  rm -rf "$project_dir"/deploy/.lock-*
  rm -rf "$project_dir/deploy/releases"
  [ "$keep_backups" = 1 ] || rm -rf "$project_dir/backups"

  # 3) .env ve anahtarlar EN SON. Bunlar kurtarmanın son malzemesi:
  # VARYAONE_MASTER_KEY olmadan elde kalan bir .varya yedeğinin şifreli alanları
  # çözülemez. Docker temizliği başarısız olduysa volume'ler hâlâ duruyor ve
  # anahtar hâlâ gerekli olabilir — bu durumda anahtarı silme, durumu bildir.
  if [ "${DOCKER_CLEANUP_OK:-0}" = 1 ]; then
    # env_set `.env.tmp.XXXXXX` üretir; bunlar da anahtarı içerebilir.
    rm -f "$project_dir/.env" "$project_dir/.env.tmp" "$project_dir/.env.partial"
    rm -f "$project_dir"/.env.tmp.*
  else
    echo >&2
    echo "  UYARI: Docker kaynakları kaldırılamadı; veriler ve .env yerinde bırakıldı." >&2
    echo "  .env içindeki VARYAONE_MASTER_KEY kalan .varya yedeklerinin şifreli" >&2
    echo "  alanlarını çözmek için gereklidir; elle silmeden önce saklayın." >&2
    echo "  Kaldırma TAMAMLANMADI." >&2
    exit 1
  fi

  # 4) Proje dizininin kendisi (isteğe bağlı, hiçbir iz bırakmaz).
  if [ "$purge" = 1 ]; then
    echo "  Proje dizini siliniyor: $project_dir"
    echo
    echo "  Varya One tamamen kaldırıldı."
    # Betik hâlâ bu dizinden çalışıyor; dizini biz çıktıktan sonra sil.
    # Yol, heredoc içine gömülmek yerine argüman olarak geçirilir: gömüldüğünde
    # içindeki boşluk veya tırnak, silinecek yolu sessizce değiştirebiliyordu.
    _self=$(mktemp /tmp/varyaone-uninstall.XXXXXX) || _self=/tmp/varyaone-uninstall.$$
    cat > "$_self" <<'EOF'
#!/bin/sh
sleep 1
rm -rf -- "$1"
rm -f -- "$0"
EOF
    chmod +x "$_self"
    cd /
    if have setsid; then
      setsid "$_self" "$project_dir" >/dev/null 2>&1 &
    elif have nohup; then
      nohup "$_self" "$project_dir" >/dev/null 2>&1 &
    else
      "$_self" "$project_dir" >/dev/null 2>&1 &
    fi
    exit 0
  fi

  echo
  echo "  Varya One kaldırıldı. Proje dosyaları (git deposu) yerinde bırakıldı."
  echo "  Tümüyle silmek için:  rm -rf \"$project_dir\""
}

# Argüman almayan komutlar fazladan argümanı sessizce yok saymaz: yok sayılan
# bir argüman, operatörün istediği şeyin yapılmadığı anlamına gelir ve bunu
# ancak sonuç yanlış çıktığında fark eder.
no_args() {
  [ $# -eq 0 ] && return 0
  echo "Bu komut argüman almaz; verilen: $*" >&2
  exit 2
}

case "${1:-}" in
  install) shift; install_stack "$@" ;;
  bootstrap) shift; no_args "$@"; bootstrap ;;
  rebuild) shift; rebuild "$@" ;;
  status) shift; no_args "$@"; show_status ;;
  restart) shift; no_args "$@"; restart_services ;;
  doctor) shift; no_args "$@"; doctor ;;
  repair-app-role) shift; no_args "$@"; repair_app_role ;;
  renew-cert) shift; renew_cert "$@" ;;
  backup) shift; no_args "$@"; backup ;;
  restore) shift; restore "$@" ;;
  system-status) require_docker; maintenance system status ;;
  system-retained) shift; no_args "$@"; require_docker; ensure_postgres_up && maintenance system retained ;;
  system-prune) shift; require_docker; ensure_postgres_up && maintenance system prune "$@" ;;
  system-promote) shift; require_docker; system_promote "$@" ;;
  prune-backups) shift; require_docker; prune_backups "$@" ;;
  recovery-bundle) shift; recovery_bundle "$@" ;;
  system-history) shift; require_docker; maintenance system history "$@" ;;
  system-resolve) shift; require_docker; system_resolve "$@" ;;
  uninstall) shift; uninstall "$@" ;;
  *) usage ;;
esac
