#!/bin/sh
set -eu

# VARYAONE_PROJECT_DIR: `run_update` re-exec's the script from a throwaway copy
# (so `git checkout` can freely replace deploy.sh on disk mid-run); the copy is
# not next to the repo, so it passes the real project dir through this env var.
project_dir=${VARYAONE_PROJECT_DIR:-$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)}
cd "$project_dir"

DOMAIN_OVERRIDE="compose.domain.yaml"
NGINX_CONF_DIR="$project_dir/deploy/nginx/conf.d"

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
  if have curl; then curl -fsSL "$1"
  elif have wget; then wget -qO- "$1"
  else return 1
  fi
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
      if [ "$_pkg_refreshed" = 0 ]; then run_root pacman -Sy --noconfirm || true; _pkg_refreshed=1; fi
      run_root pacman -S --noconfirm --needed "$@" ;;
    apk)    run_root apk add --no-cache "$@" ;;
    *)      return 1 ;;
  esac
}

ensure_basic_tools() {
  _need=""
  { have curl || have wget; } || _need="$_need curl"
  have openssl || _need="$_need openssl"
  { [ -e /etc/ssl/certs/ca-certificates.crt ] || [ -e /etc/pki/tls/certs/ca-bundle.crt ] || [ -e /etc/ssl/cert.pem ]; } \
    || _need="$_need ca-certificates"
  if [ -n "$_need" ]; then
    echo "  Temel araçlar kuruluyor:$_need"
    pkg_install $_need || echo "  Uyarı: bazıları kurulamadı ($_need) — devam ediliyor." >&2
  fi
}

install_docker_engine() {
  echo "  Docker Engine kuruluyor (${OS_NAME:-?} / ${ARCH})..."
  case "$OS_FAMILY" in
    debian | rhel | suse)
      fetch https://get.docker.com | run_root sh ;;
    arch)
      pkg_install docker docker-compose ;;
    alpine)
      pkg_install docker docker-cli-compose ;;
    *)
      if have curl || have wget; then
        echo "  Bilinmeyen dağıtım — resmi Docker betiği deneniyor."
        fetch https://get.docker.com | run_root sh
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

  ensure_basic_tools

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
                          alan adı, e-posta, portlar — hepsi tek tek sorulur.
                          Domainli modda nginx + Let's Encrypt ile otomatik HTTPS.
  bootstrap               Sunucuyu hazırla: OS'u algıla, Docker + Docker Compose
                          kur/başlat, kullanıcıyı docker grubuna ekle.
                          (install bunu zaten otomatik çağırır.)
  rebuild [--no-cache]    Görüntüleri yeniden derleyip servisleri yeniden başlatır.
  update [--target vX.Y.Z] [--yes]
                          Bulletproof güncelleme: ön kontrol → tam yedek →
                          GitHub'dan çek → derle → (gerekirse PostgreSQL majör
                          yükseltmesi: dök & geri yükle) → migrate → yeniden
                          başlat → sağlık kontrolü. Herhangi bir aşamada hata
                          olursa sistem otomatik olarak önceki sürüme geri alınır.
  recover                 Yarıda kalmış bir güncellemeyi (elektrik/kill) güvenle
                          geri al. Sonraki 'update' bunu otomatik de yapar.
  status                  Servis durumu + sağlık kontrolü.
  doctor                  Ön koşul ve ortam denetimi.
  repair-app-role         varyaone_app rolünün parolasını ve yetkilerini yeniden uygular
                          (majör PostgreSQL yükseltmesi sonrası gerekebilir).
  renew-cert              SSL sertifikasını hemen yenilemeyi dene (domainli).
  backup                  Tam sistem yedeği al: backups/ altına tek .varya dosyası
                          (veritabanı + yüklenen dosyalar).
  restore <dosya.varya> --confirm [--force]
                          .varya yedeğinden tam sistemi geri yükle.
  uninstall --confirm [--keep-backups] [--purge] [--yes]
                          Her şeyi kaldır: konteynerler, volume'ler (VERİTABANI
                          DAHİL), derlenen image'lar, ağlar, üretilen .env ve
                          nginx yapılandırması, systemd güncelleme aracı.
                          --keep-backups verilmezse backups/ de silinir.
                          --purge ayrıca proje dizininin kendisini de siler
                          (hiçbir iz bırakmaz).
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

env_get() {
  [ -f .env ] || return 0
  grep "^$1=" .env 2>/dev/null | tail -n1 | cut -d= -f2-
}

env_set() {
  key=$1
  value=$2
  touch .env
  chmod 600 .env 2>/dev/null || true
  # Aynı dizinde geçici dosya + atomik rename: yarıda kesilse bile .env asla
  # yarım/bozuk kalmaz (bozuk .env tüm dağıtımı kırar).
  tmp=$(mktemp "$project_dir/.env.tmp.XXXXXX") || { echo "env_set: geçici dosya oluşturulamadı" >&2; return 1; }
  chmod 600 "$tmp" 2>/dev/null || true
  if [ -f .env ]; then
    grep -v "^${key}=" .env > "$tmp" 2>/dev/null || true
  fi
  printf '%s=%s\n' "$key" "$value" >> "$tmp"
  mv -f "$tmp" .env
}

# Domainli mod .env içindeki VARYAONE_DOMAIN dolu ise etkindir.
domain_mode() {
  [ -n "$(env_get VARYAONE_DOMAIN)" ]
}

# İlgili compose dosyalarıyla `docker compose` çağır.
compose() {
  if domain_mode; then
    $DK compose -f compose.yaml -f "$DOMAIN_OVERRIDE" "$@"
  else
    $DK compose "$@"
  fi
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

  _psql="compose exec -T postgres psql -v ON_ERROR_STOP=1 -U $_pguser -d $_pgdb"
  if ! $_psql -f - < "$project_dir/internal/platform/migrations/app_role.sql" >/dev/null 2>&1; then
    echo "  ! varyaone_app grant'leri uygulanamadı. Superuser bağlantısıyla devam ediliyor." >&2
    return 1
  fi
  # Parolayı ayrı ver (script LOGIN/parola içermez). Parametreli değil ama
  # _apppw yalnızca hex karakter içerir.
  if ! $_psql -c "ALTER ROLE varyaone_app LOGIN PASSWORD '$_apppw'" >/dev/null 2>&1; then
    echo "  ! varyaone_app rolüne parola verilemedi. Superuser bağlantısıyla devam ediliyor." >&2
    return 1
  fi

  env_set VARYAONE_APP_DB_PASSWORD "$_apppw"
  env_set VARYAONE_APP_DATABASE_URL "postgres://varyaone_app:$_apppw@postgres:5432/$_pgdb?sslmode=disable"
  echo "  ✓ varyaone_app rolü etkin — firma izolasyonu veritabanı seviyesinde zorlanıyor."
  return 0
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

# --- tekil çalıştırma kilidi -------------------------------------------------
# Aynı anda iki güncelleme/yedek/geri-yükleme (ör. host agent + elle çalıştırma)
# .update-rollback dosyasını, :preupdate etiketlerini ve git ağacını bozar.
# mkdir atomiktir ve her POSIX sh'de vardır; sahibi ölmüşse kilit devralınır.
LOCK_DIR=""
LOCK_HELD=0

acquire_lock() {
  _name=${1:-deploy}
  _dir="$project_dir/deploy/.lock-$_name"
  _waited=0
  _limit=${LOCK_WAIT_SECONDS:-0}
  while :; do
    if mkdir "$_dir" 2>/dev/null; then
      LOCK_DIR="$_dir"; LOCK_HELD=1
      printf '%s\n' "$$" > "$_dir/pid" 2>/dev/null || true
      # Çağıran, release_lock'u bir trap ile çağırmaktan sorumludur.
      return 0
    fi
    _owner=$(cat "$_dir/pid" 2>/dev/null || echo "")
    if [ -n "$_owner" ] && ! kill -0 "$_owner" 2>/dev/null; then
      echo "  Uyarı: sahipsiz kilit ($_owner) kaldırılıyor." >&2
      rm -rf "$_dir" 2>/dev/null || true
      continue
    fi
    if [ "$_waited" -ge "$_limit" ]; then
      echo "Başka bir işlem çalışıyor (kilit: $_dir, pid: ${_owner:-?}). Bitmesini bekleyin." >&2
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

# Otomatik guncelleme icin host agent belirteci (.env'de yoksa uretilir).
ensure_update_token() {
  [ -f .env ] || return 0
  if ! grep -q '^VARYAONE_UPDATE_AGENT_TOKEN=.\+' .env 2>/dev/null; then
    grep -v '^VARYAONE_UPDATE_AGENT_TOKEN=' .env > .env.tmp 2>/dev/null || true
    printf 'VARYAONE_UPDATE_AGENT_TOKEN=%s\n' "$(generate_hex_token)" >> .env.tmp
    cat .env.tmp > .env && rm -f .env.tmp
    echo ".env için otomatik güncelleme belirteci oluşturuldu."
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
    ensure_update_token
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
  ensure_update_token
}

# --- nginx / certbot ---------------------------------------------------------

# render_nginx_conf <bootstrap|full> <domain>
render_nginx_conf() {
  mode=$1
  domain=$2
  mkdir -p "$NGINX_CONF_DIR"
  target="$NGINX_CONF_DIR/default.conf"

  {
    echo "# Bu dosya deploy.sh tarafından üretildi; elle düzenlemeyin."
    echo "server {"
    echo "    listen 80;"
    echo "    server_name $domain;"
    echo "    location /.well-known/acme-challenge/ { root /var/www/certbot; }"
    if [ "$mode" = "full" ]; then
      echo "    location / { return 301 https://\$host\$request_uri; }"
    else
      echo "    location / { default_type text/plain; return 200 'varyaone: SSL sertifikasi hazirlaniyor'; }"
    fi
    echo "}"

    if [ "$mode" = "full" ]; then
      cat <<EOF
server {
    listen 443 ssl;
    http2 on;
    server_name $domain;

    ssl_certificate     /etc/letsencrypt/live/$domain/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/$domain/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_prefer_server_ciphers off;
    ssl_session_cache shared:SSL:10m;
    add_header Strict-Transport-Security "max-age=31536000" always;

    # .varya tam sistem yedeği geri yükleme için büyük gövde sınırı.
    client_max_body_size 8g;
    proxy_read_timeout 600s;
    proxy_send_timeout 600s;

    # SvelteKit/adapter-node yanit basliklari (CSP + Set-Cookie) nginx'in
    # varsayilan 4k/8k proxy buffer'ini asabiliyor -> "upstream sent too big
    # header" -> 502. Buffer'lari genislet.
    proxy_buffer_size 16k;
    proxy_buffers 8 16k;
    proxy_busy_buffers_size 32k;

    # Docker'in gomulu DNS'i. proxy_pass'te degisken kullanildigi icin nginx
    # `frontend` adini her istekte yeniden cozer; boylece frontend konteyneri
    # yeniden olusturulup yeni bir IP alsa bile 502 vermez.
    resolver 127.0.0.11 valid=10s ipv6=off;

    location / {
        set \$varyaone_upstream frontend:3000;
        proxy_pass http://\$varyaone_upstream;
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
EOF
    fi
  } > "$target"
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

obtain_certificate() {
  domain=$1
  email=$2
  staging=$3
  set -- certonly --webroot -w /var/www/certbot \
    -d "$domain" --email "$email" --agree-tos --no-eff-email \
    --keep-until-expiring --non-interactive
  [ "$staging" = "1" ] && set -- "$@" --staging
  echo "Let's Encrypt sertifikası isteniyor ($domain)..."
  compose run --rm --entrypoint certbot certbot "$@"
}

# Domainli modda, frontend/api konteynerleri yeniden olusturulduktan sonra
# nginx'i sessizce reload et. Degiskenli proxy_pass zaten her istekte yeniden
# cozer; bu ekstra bir emniyet agi ve nginx ayakta degilse gurultu cikarmaz.
reload_nginx_soft() {
  domain_mode || return 0
  # nginx ayakta degilse `exec` sessizce basarisiz olur; sorun degil.
  compose exec -T nginx nginx -s reload >/dev/null 2>&1 || true
}

reload_nginx() {
  compose exec -T nginx nginx -t >/dev/null 2>&1 || {
    echo "nginx yapılandırması geçersiz." >&2
    compose exec -T nginx nginx -t || true
    return 1
  }
  compose exec -T nginx nginx -s reload
}

# --- komutlar ---------------------------------------------------------------

install_domainless() {
  _wp=${1:-}
  _ap=${2:-}
  # Varsa daha önceki domainli kurulumun nginx/certbot katmanını kaldır.
  if [ -n "$(env_get VARYAONE_DOMAIN)" ]; then
    $DK compose -f compose.yaml -f "$DOMAIN_OVERRIDE" rm -sf nginx certbot >/dev/null 2>&1 || true
  fi
  [ -n "$_wp" ] && env_set VARYAONE_WEB_PORT "$_wp"
  [ -n "$_ap" ] && env_set VARYAONE_API_PORT "$_ap"
  env_set VARYAONE_WEB_BIND 0.0.0.0
  env_set VARYAONE_API_BIND 0.0.0.0
  env_set VARYAONE_DOMAIN ""
  web_port=$(env_get VARYAONE_WEB_PORT); web_port=${web_port:-3000}
  env_set VARYAONE_WEB_ORIGIN "http://localhost:$web_port"

  compose config --quiet
  compose up --build -d
  # migrate servisi (api'nin bağımlılığı) bu noktada tamamlandı; rolü kurup
  # api/worker'ı yeni bağlantıyla yeniden oluştur.
  if ensure_app_role; then
    compose up -d
  fi
  compose ps
  resolved=$(published_port frontend 3000 "$web_port")
  echo
  echo "Varya One hazır (domainsiz, SSL yok): http://localhost:$resolved"
}

install_domain() {
  domain=$1
  email=$2
  staging=$3
  _hp=${4:-}
  _sp=${5:-}

  [ -n "$domain" ] || { echo "Alan adı gerekli." >&2; exit 2; }
  [ -n "$email" ] || { echo "Let's Encrypt e-posta adresi gerekli." >&2; exit 2; }
  [ -n "$_hp" ] && env_set VARYAONE_HTTP_PORT "$_hp"
  [ -n "$_sp" ] && env_set VARYAONE_HTTPS_PORT "$_sp"

  # frontend/api yalnızca localhost; dışarıya sadece nginx bakar.
  env_set VARYAONE_DOMAIN "$domain"
  env_set VARYAONE_ACME_EMAIL "$email"
  env_set VARYAONE_WEB_ORIGIN "https://$domain"
  env_set VARYAONE_WEB_BIND 127.0.0.1
  env_set VARYAONE_API_BIND 127.0.0.1

  http_port=$(env_get VARYAONE_HTTP_PORT); http_port=${http_port:-80}
  https_port=$(env_get VARYAONE_HTTPS_PORT); https_port=${https_port:-443}

  open_firewall_ports "$http_port" "$https_port"

  echo "1/5 nginx bootstrap yapılandırması yazılıyor..."
  render_nginx_conf bootstrap "$domain"

  echo "2/5 yapılandırma doğrulanıyor..."
  compose config --quiet

  echo "3/5 servisler başlatılıyor (postgres, api, worker, frontend, nginx, certbot)..."
  compose up --build -d
  if ensure_app_role; then
    compose up -d
  fi

  echo "4/5 nginx'in ACME isteklerini karşılaması bekleniyor..."
  if ! wait_for_http "http://localhost:$http_port/.well-known/acme-challenge/ping"; then
    echo "Uyarı: nginx ${http_port} portunda yanıt vermedi. 80/443'ün açık ve" >&2
    echo "$domain -> bu sunucu DNS kaydının doğru olduğundan emin olun." >&2
  fi

  echo "5/5 SSL sertifikası alınıyor ve HTTPS'e geçiliyor..."
  if obtain_certificate "$domain" "$email" "$staging"; then
    render_nginx_conf full "$domain"
    if reload_nginx; then
      echo
      compose ps
      echo
      echo "Site doğrulanıyor (https://$domain)..."
      if _code=$(wait_for_ok "https://$domain" 45); then
        echo "Varya One hazır: https://$domain"
      else
        echo >&2
        echo "UYARI: sertifika ve nginx tamam ama site https://$domain üzerinden" >&2
        echo "yanıt vermiyor (son HTTP kodu: ${_code:-000}). Genelde frontend" >&2
        echo "konteyneri kalkmamıştır. Şununla bakın:" >&2
        echo "  docker compose -f compose.yaml -f $DOMAIN_OVERRIDE logs --tail=60 frontend api" >&2
        exit 1
      fi
    else
      echo "Sertifika alındı ama nginx yeniden yüklenemedi; 'docker compose ... logs nginx' ile bakın." >&2
      exit 1
    fi
  else
    echo >&2
    echo "Sertifika alınamadı. Sık nedenler:" >&2
    echo "  - $domain A/AAAA kaydı bu sunucuya işaret etmiyor" >&2
    echo "  - 80/443 portları güvenlik duvarı/başka servis tarafından kapalı" >&2
    echo "  - Let's Encrypt oran sınırı (aynı alan adı için haftada 5 sertifika)" >&2
    echo "Site şimdilik HTTP üzerinden çalışıyor: http://$domain" >&2
    exit 1
  fi
}

valid_domain() {
  case "$1" in
    *[!a-zA-Z0-9.-]* | "" | .* | *. | *..*) return 1 ;;
    *.*) return 0 ;;
    *) return 1 ;;
  esac
}

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
#   WZ_MODE (domain|domainless) WZ_DOMAIN WZ_EMAIL WZ_STAGING WZ_HTTP WZ_HTTPS
#   WZ_WEBPORT WZ_APIPORT
wizard() {
  banner "Kurulum Sihirbazı"
  printf '  Bu sunucuya Varya One nasıl kurulsun?\n\n'
  printf '    1) Domainli   — otomatik HTTPS (Let'\''s Encrypt + nginx)   [önerilen]\n'
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
      WZ_HTTP=$(ask "HTTP portu" "$(env_get VARYAONE_HTTP_PORT | grep . || echo 80)")
      WZ_HTTPS=$(ask "HTTPS portu" "$(env_get VARYAONE_HTTPS_PORT | grep . || echo 443)")
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
      printf '    Mod           : Domainli + otomatik HTTPS\n'
      printf '    Alan adı      : %s  ->  https://%s\n' "$WZ_DOMAIN" "$WZ_DOMAIN"
      printf '    E-posta       : %s\n' "$WZ_EMAIL"
      printf '    Portlar       : HTTP %s / HTTPS %s\n' "$WZ_HTTP" "$WZ_HTTPS"
      printf '    Sertifika     : Let'\''s Encrypt (gerçek)\n'
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
  sync_release_env

  if interactive; then
    # Terminalde: adım adım sihirbaz.
    wizard
    if [ "$WZ_MODE" = "domain" ]; then
      install_domain "$WZ_DOMAIN" "$WZ_EMAIL" "$WZ_STAGING" "$WZ_HTTP" "$WZ_HTTPS"
    else
      install_domainless "$WZ_WEBPORT" "$WZ_APIPORT"
    fi
    post_install_agent
    return
  fi

  # İnteraktif değil (CI / pipe): .env'e göre karar ver.
  domain=$(env_get VARYAONE_DOMAIN)
  if [ -n "$domain" ]; then
    email=$(env_get VARYAONE_ACME_EMAIL)
    [ -n "$email" ] || { echo ".env içinde VARYAONE_ACME_EMAIL gerekli (interaktif olmayan domainli kurulum)." >&2; exit 2; }
    banner
    install_domain "$domain" "$email" 0 "" ""
  else
    install_domainless "" ""
  fi
  post_install_agent
}

renew_cert() {
  require_docker
  domain_mode || { echo "Domainli kurulum yok (.env içinde VARYAONE_DOMAIN boş)." >&2; exit 1; }
  compose run --rm --entrypoint certbot certbot renew --webroot -w /var/www/certbot --force-renewal
  reload_nginx && echo "Sertifika yenilendi."
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

# Calisan surumu git etiketinden turetip .env'e yazar.
#
# Bunu yapmazsak VARYAONE_RELEASE bos kalir ve sunucu kendini "dev" olarak
# raporlar. Surum karsilastirmasi ayristirilamayan bir surumu EN ESKI sayar,
# yani git clone ile kurulmus ve en yeni etiketin ONUNDE olan bir kurulum
# kendisine surekli daha eski bir surume "guncelleme" teklif eder.
#
# `git describe` ciktisi (ornek: v0.1.7-alpha-1-gf106e8a) surum ayristiricisi
# tarafindan 0.1.7 olarak okunur, dolayisiyla karsilastirma dogru calisir:
# etiketle ayni noktadaysak guncelleme teklif edilmez, yeni etiket cikinca
# edilir.
sync_release_env() {
  _rel=$(current_release)
  case "$_rel" in
    ""|unknown) return 0 ;;
  esac
  env_set VARYAONE_RELEASE "$_rel"
}

rebuild() {
  require_docker
  banner "Yeniden Derleme"
  no_cache=""
  case "${1:-}" in
    --no-cache) no_cache="--no-cache" ;;
    "") : ;;
    *) echo "Bilinmeyen seçenek: $1" >&2; usage ;;
  esac
  [ -f .env ] || { echo ".env yok; önce ./deploy.sh install çalıştırın." >&2; exit 1; }
  sync_release_env
  echo "  Görüntüler derleniyor${no_cache:+ (önbelleksiz)}..."
  compose build $no_cache
  echo "  Servisler yeniden başlatılıyor..."
  compose up -d
  reload_nginx_soft
  echo
  compose ps
  echo
  echo "  Yeniden derleme tamamlandı."
}

# ===========================================================================
#  Guncelleme (bulletproof): on kontrol -> yedek -> derle -> migrate ->
#  yeniden baslat -> saglik; herhangi bir asamada hata -> otomatik geri alma.
#  Manuel: ./deploy.sh update
#  Agent (host systemd): ./deploy.sh update --yes --report [--target vX.Y.Z]
# ===========================================================================

UPD_LOG=""
UPD_REPORT=0
UPD_SELF_URL=""
UPD_TOKEN=""
UPD_PREV_COMMIT=""
UPD_PREV_RELEASE=""
UPD_NEW_RELEASE=""
UPD_TARGET_VERSION=""
UPD_PRE_BACKUP=""
UPD_MIGRATED=0
UPD_FROM_VERSION=""
UPD_PRE_MIGRATE_VERSION=""
UPD_ROLLING_BACK=0
UPD_PG_UPGRADED=0
UPD_PG_FROM_MAJOR=""

current_release() {
  git describe --tags --always --dirty 2>/dev/null || git rev-parse --short HEAD 2>/dev/null || echo "unknown"
}

# report_phase <phase> <mesaj>
report_phase() {
  _ts=$(date -u +%H:%M:%S)
  printf '  [%s] %-12s %s\n' "$_ts" "$1" "${2:-}"
  [ -n "$UPD_LOG" ] && printf '[%s] %s %s\n' "$_ts" "$1" "${2:-}" >> "$UPD_LOG" 2>/dev/null || true
  if [ "$UPD_REPORT" = 1 ] && [ -n "$UPD_SELF_URL" ] && [ -n "$UPD_TOKEN" ]; then
    _msg=$(printf '%s' "${2:-}" | sed 's/\\/\\\\/g; s/"/\\"/g')
    curl -fsS -m 10 -X POST "$UPD_SELF_URL/internal/update/progress" \
      -H "authorization: Bearer $UPD_TOKEN" -H 'content-type: application/json' \
      -d "{\"phase\":\"$1\",\"message\":\"$_msg\"}" >/dev/null 2>&1 || true
  fi
}

# report_result <ok|fail> <hata-mesaji> <rolled_back:0|1>
report_result() {
  _ok=false; [ "$1" = "ok" ] && _ok=true
  _rb=false; [ "${3:-0}" = "1" ] && _rb=true
  _tail=""
  [ -n "$UPD_LOG" ] && [ -f "$UPD_LOG" ] && _tail=$(tail -n 120 "$UPD_LOG" 2>/dev/null | sed 's/\\/\\\\/g; s/"/\\"/g; s/\t/ /g' | awk '{printf "%s\\n", $0}')
  _err=$(printf '%s' "${2:-}" | sed 's/\\/\\\\/g; s/"/\\"/g')
  if [ "$UPD_REPORT" = 1 ] && [ -n "$UPD_SELF_URL" ] && [ -n "$UPD_TOKEN" ]; then
    curl -fsS -m 15 -X POST "$UPD_SELF_URL/internal/update/result" \
      -H "authorization: Bearer $UPD_TOKEN" -H 'content-type: application/json' \
      -d "{\"ok\":$_ok,\"error\":\"$_err\",\"rolled_back\":$_rb,\"from_version\":\"$UPD_FROM_VERSION\",\"to_version\":\"${UPD_NEW_RELEASE:-${UPD_TARGET_VERSION:-$UPD_FROM_VERSION}}\",\"log_tail\":\"$_tail\"}" \
      >/dev/null 2>&1 || true
  fi
}

# 127.0.0.1:port/health/ready true olana kadar bekle (<saniye> zaman asimi)
# frontend konteynerinde HTTP dinleyicisi ayakta mi? (api'den compose agi
# uzerinden yoklanir — api'de wget kesin var). wget cikis 8 = sunucu bir hata
# yaniti verdi (3xx/4xx/5xx) ama AYAKTA; cikis 4 = baglanti yok = kapali.
_frontend_healthy() {
  compose exec -T api wget -q -O /dev/null -T 5 http://frontend:3000/ >/dev/null 2>&1 && return 0
  [ "$?" = 8 ]
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

# api konteynerinden uygulanmış şema sürümünü oku (çalışmıyorsa boş döner).
db_migration_version() {
  compose exec -T api varyaone migrate status 2>/dev/null \
    | sed -n 's/.*current=\([0-9][0-9]*\).*/\1/p' | head -n1
}

# api daha önce kurulmuş mu? (postgres volume + şema tablosu var mı)
system_installed() {
  compose exec -T api varyaone migrate status >/dev/null 2>&1
}

# --- PostgreSQL majör sürüm yükseltmesi -------------------------------------
# Çalışan postgres konteynerinin majör sürümü (örn. 18). Kapalıysa boş.
pg_server_major() {
  _u=$(env_get POSTGRES_USER); _u=${_u:-varyaone}
  _d=$(env_get POSTGRES_DB); _d=${_d:-varyaone}
  _n=$(compose exec -T postgres psql -U "$_u" -d "$_d" -tAc 'SHOW server_version_num' 2>/dev/null | tr -cd '0-9')
  [ -n "$_n" ] && echo $(( _n / 10000 ))
}

# Çalışma ağacındaki compose.yaml'ın istediği postgres majör sürümü (örn. 18).
compose_pg_major() {
  grep -oE 'postgres:[0-9]+' "$project_dir/compose.yaml" 2>/dev/null | grep -oE '[0-9]+' | head -n1
}

# Başarılı bir majör yükseltmeden sonra eski majörün artık kullanılmayan veri
# dizinini volume'den siler (disk iadesi). `postgres` imajı sürümlü PGDATA
# kullanır: /var/lib/postgresql/<majör>/docker.
pg_drop_old_major_dir() {
  _m=$1
  [ -n "$_m" ] || return 0
  compose exec -T postgres sh -c "rm -rf /var/lib/postgresql/$_m" >>"${UPD_LOG:-/dev/null}" 2>&1 || true
}

# .varya dosyasının bütünlüğünü doğrula: var + boş değil + geçerli arşiv +
# motorun kendi sağlama doğrulaması. 0 = sağlam.
verify_backup_file() {
  _f=$1
  [ -s "$_f" ] || { echo "yedek dosyası yok veya boş: $_f" >&2; return 1; }
  _sz=$(wc -c < "$_f" 2>/dev/null || echo 0)
  [ "${_sz:-0}" -ge 1024 ] || { echo "yedek dosyası şüpheli derecede küçük (${_sz} bayt)" >&2; return 1; }
  _first=$(tar -tf "$_f" 2>/dev/null | head -n1)
  [ "$_first" = "manifest.json" ] || { echo "geçersiz .varya arşivi (ilk giriş: ${_first:-yok})" >&2; return 1; }
  if compose ps --status running api 2>/dev/null | grep -q api; then
    compose exec -T api varyaone backup verify - < "$_f" >>"${UPD_LOG:-/dev/null}" 2>&1 \
      || { echo "yedek sağlama doğrulaması başarısız: $_f" >&2; return 1; }
  fi
  return 0
}

update_preflight() {
  # Disk: hem proje dizini hem docker veri kökü için bol alan gerekli.
  _min_kb=5242880
  _proj_kb=$(_avail_kb_for "$project_dir")
  _proj_gb=$(awk "BEGIN{printf \"%.1f\", ${_proj_kb:-0}/1048576}")
  if [ "${_proj_kb:-0}" -lt "$_min_kb" ]; then
    report_phase preflight "proje diski yetersiz: ${_proj_gb} GiB boş, en az 5 GiB gerekli"
    return 1
  fi
  _docker_root=$($DK info --format '{{.DockerRootDir}}' 2>/dev/null || echo "")
  if [ -n "$_docker_root" ] && [ -d "$_docker_root" ]; then
    _dk_kb=$(_avail_kb_for "$_docker_root")
    if [ -n "$_dk_kb" ] && [ "$_dk_kb" -lt "$_min_kb" ]; then
      _dk_gb=$(awk "BEGIN{printf \"%.1f\", ${_dk_kb}/1048576}")
      report_phase preflight "docker diski ($_docker_root) yetersiz: ${_dk_gb} GiB boş, en az 5 GiB gerekli"
      return 1
    fi
  fi
  # Docker calisir durumda mi
  if ! $DK info >/dev/null 2>&1; then
    report_phase preflight "docker daemon yanıt vermiyor"
    return 1
  fi
  # Calisma agaci temiz mi (yerel degisiklikler pull'u bozar)
  if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
    report_phase preflight "çalışma ağacı kirli — sunucuda 'git status' ile temizleyin"
    return 1
  fi
  # GitHub'a erisim
  if ! git ls-remote --exit-code origin HEAD >/dev/null 2>&1; then
    report_phase preflight "git uzak sunucusuna (origin) erişilemiyor"
    return 1
  fi
  # Mevcut sistem saglikli mi (calisiyorsa)
  if compose ps --status running api 2>/dev/null | grep -q api; then
    if ! compose exec -T api wget -q -O - http://127.0.0.1:8080/health/ready >/dev/null 2>&1; then
      report_phase preflight "mevcut sistem sağlıksız; önce onu düzeltin"
      return 1
    fi
  fi
  report_phase preflight "tamam — ${_proj_gb} GiB boş"
  return 0
}

# Kirilan asamadan sonra guvenli geri donus. Tekrar girise karsi korumali;
# icinde `set +e` — geri alma adimlarindan biri patlasa bile devam eder.
rollback_update() {
  [ "${UPD_ROLLING_BACK:-0}" = 1 ] && return 0
  UPD_ROLLING_BACK=1
  trap - EXIT INT TERM
  set +e

  _phase=$1
  _restore_db=${2:-0}
  report_phase rollback "'$_phase' başarısız — önceki sürüme dönülüyor"

  # 1) Kaynak agaci geri al — KRITIK. Sessizce yutma.
  if ! git checkout --quiet --force --detach "$UPD_PREV_COMMIT" 2>>"${UPD_LOG:-/dev/null}"; then
    if ! git reset --hard "$UPD_PREV_COMMIT" >>"${UPD_LOG:-/dev/null}" 2>&1; then
      report_phase rollback "KRİTİK: kaynak ağacı $UPD_PREV_COMMIT sürümüne döndürülemedi — elle müdahale gerekli"
      report_result fail "Geri alma başarısız: git ağacı geri alınamadı ($UPD_PREV_COMMIT). Sistemi elle onarın." 0
      release_lock
      exit 1
    fi
  fi
  [ -n "$UPD_PREV_RELEASE" ] && env_set VARYAONE_RELEASE "$UPD_PREV_RELEASE"

  # 1b) Majör PG yükseltmesi denendiyse: eski majör dizini (örn. .../18/docker)
  # volume'de bozulmadan duruyor; git checkout eski `postgres:<majör>` imajını
  # geri getirdi, `compose up` onu otomatik devreye alır. Yeni majörün yarım
  # dizini volume'de kalır — sonraki deneme onu `backup restore --force` ile
  # temizler. Yıkıcı .varya geri yüklemesi GEREKMEZ.
  if [ "${UPD_PG_UPGRADED:-0}" = 1 ]; then
    report_phase rollback "PostgreSQL yükseltmesi geri alınıyor — eski majör (${UPD_PG_FROM_MAJOR:-?}) devreye giriyor"
  fi

  # 2) Onceki image'lari geri getir: once :preupdate etiketlerinden, olmadi yeniden derle.
  _restored_images=1
  for _svc in api worker frontend migrate; do
    if $DK image inspect "varyaone-${_svc}:preupdate" >/dev/null 2>&1; then
      $DK image tag "varyaone-${_svc}:preupdate" "varyaone-${_svc}:latest" 2>/dev/null || _restored_images=0
    else
      _restored_images=0
    fi
  done
  [ "$_restored_images" = 1 ] || compose build >>"${UPD_LOG:-/dev/null}" 2>&1

  # 3) Akilli DB geri yukleme: yalnizca sema surumu gercekten ilerlediyse.
  #    (Migrasyonlar tek transaction — basarisiz migrasyon DB'yi degistirmez.)
  if [ "$_restore_db" = 1 ] && [ -n "$UPD_PRE_BACKUP" ] && [ -f "$UPD_PRE_BACKUP" ]; then
    compose up -d postgres api >>"${UPD_LOG:-/dev/null}" 2>&1
    _now_ver=""; _tries=0
    while [ "$_tries" -lt 12 ]; do
      _now_ver=$(db_migration_version)
      [ -n "$_now_ver" ] && break
      sleep 5; _tries=$((_tries + 1))
    done
    _pre_ver=${UPD_PRE_MIGRATE_VERSION:-}
    if [ -n "$_now_ver" ] && [ -n "$_pre_ver" ] && [ "$_now_ver" = "$_pre_ver" ]; then
      report_phase rollback "veritabanı değişmedi (şema $_now_ver) — yıkıcı geri yükleme atlandı"
    else
      report_phase rollback "veritabanı yedekten geri yükleniyor (şema $_pre_ver -> $_now_ver)"
      compose stop worker frontend >>"${UPD_LOG:-/dev/null}" 2>&1
      if compose exec -T api varyaone backup restore - --force < "$UPD_PRE_BACKUP" >>"${UPD_LOG:-/dev/null}" 2>&1; then
        compose up -d --force-recreate api >>"${UPD_LOG:-/dev/null}" 2>&1
      else
        # DB tutarsiz: yeni imajlari BASLATMA, postgres'i durdur, elle mudahale.
        compose stop >>"${UPD_LOG:-/dev/null}" 2>&1
        report_phase rollback "KRİTİK: otomatik DB geri yükleme başarısız — sistem DURDURULDU"
        report_result fail "Geri alma sırasında DB geri yükleme başarısız. Sistem tutarsız ve durduruldu. Elle geri yükleyin: $UPD_PRE_BACKUP" 0
        release_lock
        exit 1
      fi
    fi
  fi

  # 4) Servisleri onceki imajlarla ayaga kaldir.
  compose up -d >>"${UPD_LOG:-/dev/null}" 2>&1
  reload_nginx_soft >>"${UPD_LOG:-/dev/null}" 2>&1
  if wait_healthy 180; then
    report_phase rollback "önceki sürüme dönüldü — sistem sağlıklı ($UPD_PREV_RELEASE)"
    rm -f "$project_dir/deploy/.update-rollback"
    report_result fail "Güncelleme '$_phase' aşamasında başarısız oldu; sistem $UPD_PREV_RELEASE sürümüne geri alındı." 1
  else
    report_phase rollback "KRİTİK: geri alma sonrası sistem sağlıksız — elle müdahale gerekli"
    report_result fail "Güncelleme '$_phase' aşamasında başarısız; geri alma da sağlıksız — deploy/update log dosyasına bakın." 0
  fi
  release_lock
  exit 1
}

# Yarida kalan bir guncellemeyi (SIGKILL, elektrik kesintisi) sonraki calistirmada
# ya da elle `./deploy.sh recover` ile temizle.
recover_update() {
  _rb="$project_dir/deploy/.update-rollback"
  [ -f "$_rb" ] || return 0
  require_docker
  acquire_lock update
  trap 'release_lock' EXIT INT TERM
  banner "Yarım Kalan Güncelleme Kurtarma"
  UPD_LOG="$project_dir/deploy/recover-$(date -u +%Y%m%dT%H%M%SZ).log"
  : > "$UPD_LOG"
  # shellcheck disable=SC1090
  UPD_PREV_COMMIT=$(sed -n 's/^prev_commit=//p' "$_rb" | head -n1)
  UPD_PREV_RELEASE=$(sed -n 's/^prev_release=//p' "$_rb" | head -n1)
  UPD_TARGET_VERSION=$(sed -n 's/^target_version=//p' "$_rb" | head -n1)
  UPD_PRE_MIGRATE_VERSION=$(sed -n 's/^pre_migrate_version=//p' "$_rb" | head -n1)
  UPD_MIGRATED=$(sed -n 's/^migrated=//p' "$_rb" | head -n1); UPD_MIGRATED=${UPD_MIGRATED:-0}
  UPD_PG_UPGRADED=$(sed -n 's/^pg_upgraded=//p' "$_rb" | head -n1); UPD_PG_UPGRADED=${UPD_PG_UPGRADED:-0}
  UPD_PG_FROM_MAJOR=$(sed -n 's/^pg_from_major=//p' "$_rb" | head -n1)
  UPD_PRE_BACKUP=$(sed -n 's/^pre_backup=//p' "$_rb" | head -n1)
  [ -n "$UPD_PRE_BACKUP" ] || UPD_PRE_BACKUP=$(ls -1t "$project_dir"/backups/pre-update-*.varya 2>/dev/null | head -n1)
  [ -n "$UPD_PREV_COMMIT" ] || { echo "  .update-rollback okunamadı; elle müdahale gerekli." >&2; exit 1; }
  echo "  Önceki sürüm: ${UPD_PREV_RELEASE:-?} ($UPD_PREV_COMMIT), migrated=$UPD_MIGRATED"
  rollback_update interrupted "$UPD_MIGRATED"
}

run_update() {
  # Adim 4 (`git checkout <target>`) bu betigin kendisini diskte degistirir.
  # Kabuk betigi calisirken dosyayi degistirmek (POSIX sh dahil) ofset kaymasi
  # yuzunden yarim/yanlis calismaya yol acar. Bu yuzden ilk is: betigi gecici
  # bir kopyaya al ve oradan yeniden calis; diskteki deploy.sh artik serbestce
  # degistirilebilir. Kopyayi release_lock temizler.
  if [ "${VARYAONE_UPDATE_REEXEC:-0}" != 1 ]; then
    # Kopyayi repo ile ayni dosya sisteminde tut: /tmp bazen noexec olur, ama
    # deploy.sh'in kendisi buradan calisti, yani burasi calistirilabilir.
    _self_copy="$project_dir/deploy/.deploy-self.$$"
    rm -f "$project_dir"/deploy/.deploy-self.* 2>/dev/null || true
    cat "$project_dir/deploy.sh" > "$_self_copy" && chmod +x "$_self_copy" || {
      rm -f "$_self_copy"; echo "Geçici betik kopyası oluşturulamadı." >&2; exit 1; }
    VARYAONE_UPDATE_REEXEC=1 VARYAONE_PROJECT_DIR="$project_dir" \
      VARYAONE_SELF_COPY="$_self_copy" exec "$_self_copy" update "$@"
  fi

  require_docker
  target_ref=""
  assume_yes=0
  for _a in "$@"; do
    case "$_a" in
      --report) UPD_REPORT=1 ;;
      --yes|-y) assume_yes=1 ;;
      --target) _expect_target=1 ;;
      --target=*) target_ref=${_a#--target=} ;;
      *)
        if [ "${_expect_target:-0}" = 1 ]; then target_ref=$_a; _expect_target=0
        else echo "Bilinmeyen seçenek: $_a" >&2; exit 2; fi ;;
    esac
  done

  [ -f .env ] || { echo ".env yok; önce ./deploy.sh install çalıştırın." >&2; exit 1; }
  command -v git >/dev/null 2>&1 || { echo "git gerekli." >&2; exit 1; }
  [ -d "$project_dir/.git" ] || { echo "Bu dizin bir git deposu değil; güncelleme git ile yapılır." >&2; exit 1; }

  mkdir -p "$project_dir/deploy" "$project_dir/backups"

  # Aynı anda ikinci bir güncelleme/yedek çalışmasın (host agent + elle çakışması).
  acquire_lock update
  trap 'release_lock' EXIT INT TERM

  # Önceki güncelleme yarıda kaldıysa (SIGKILL/elektrik) önce onu temizle.
  if [ -f "$project_dir/deploy/.update-rollback" ]; then
    echo "  Önceki güncelleme tamamlanmamış — önce geri alma çalıştırılıyor." >&2
    UPD_LOG="$project_dir/deploy/recover-$(date -u +%Y%m%dT%H%M%SZ).log"; : > "$UPD_LOG"
    UPD_PREV_COMMIT=$(sed -n 's/^prev_commit=//p' "$project_dir/deploy/.update-rollback" | head -n1)
    UPD_PREV_RELEASE=$(sed -n 's/^prev_release=//p' "$project_dir/deploy/.update-rollback" | head -n1)
    UPD_TARGET_VERSION=$(sed -n 's/^target_version=//p' "$project_dir/deploy/.update-rollback" | head -n1)
    UPD_PRE_MIGRATE_VERSION=$(sed -n 's/^pre_migrate_version=//p' "$project_dir/deploy/.update-rollback" | head -n1)
    _lm=$(sed -n 's/^migrated=//p' "$project_dir/deploy/.update-rollback" | head -n1)
    UPD_PG_UPGRADED=$(sed -n 's/^pg_upgraded=//p' "$project_dir/deploy/.update-rollback" | head -n1); UPD_PG_UPGRADED=${UPD_PG_UPGRADED:-0}
    UPD_PG_FROM_MAJOR=$(sed -n 's/^pg_from_major=//p' "$project_dir/deploy/.update-rollback" | head -n1)
    UPD_PRE_BACKUP=$(sed -n 's/^pre_backup=//p' "$project_dir/deploy/.update-rollback" | head -n1)
    [ -n "$UPD_PRE_BACKUP" ] || UPD_PRE_BACKUP=$(ls -1t "$project_dir"/backups/pre-update-*.varya 2>/dev/null | head -n1)
    [ -n "$UPD_PREV_COMMIT" ] && rollback_update interrupted "${_lm:-0}"
    rm -f "$project_dir/deploy/.update-rollback"
    UPD_ROLLING_BACK=0
  fi

  UPD_LOG="$project_dir/deploy/update-$(date -u +%Y%m%dT%H%M%SZ).log"
  : > "$UPD_LOG"
  if [ "$UPD_REPORT" = 1 ]; then
    UPD_TOKEN=$(env_get VARYAONE_UPDATE_AGENT_TOKEN)
    _p=$(env_get VARYAONE_API_PORT); UPD_SELF_URL="http://127.0.0.1:${_p:-8080}"
  fi

  banner "Güncelleme"
  UPD_FROM_VERSION=$(current_release)
  UPD_PREV_COMMIT=$(git rev-parse HEAD)
  UPD_PREV_RELEASE=$(env_get VARYAONE_RELEASE); [ -n "$UPD_PREV_RELEASE" ] || UPD_PREV_RELEASE=$UPD_FROM_VERSION

  # 1) On kontroller
  update_preflight || { report_result fail "Ön kontroller başarısız (bkz. yukarıdaki sebep)." 0; exit 1; }

  # Hedef referansi coz
  report_phase fetch "sürüm bilgisi alınıyor"
  git fetch --tags --prune --quiet origin >>"$UPD_LOG" 2>&1 || { report_result fail "git fetch başarısız." 0; exit 1; }
  if [ -z "$target_ref" ]; then
    # Manual updates follow the stable release channel too: prefer the newest
    # final SemVer tag instead of deploying whatever happens to be on main.
    # Development repositories without release tags retain the old main/HEAD
    # fallback so bootstrap environments still work.
    target_ref=$(git tag --list 'v*' --sort=-version:refname \
      | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -n1)
    if [ -n "$target_ref" ]; then
      target_ref="refs/tags/$target_ref"
    else
      target_ref=$(git rev-parse --verify --quiet origin/HEAD >/dev/null 2>&1 && echo "origin/HEAD" || echo "origin/main")
    fi
  elif git rev-parse --verify --quiet "refs/tags/$target_ref" >/dev/null 2>&1; then
    target_ref="refs/tags/$target_ref"
  fi
  git rev-parse --verify --quiet "$target_ref" >/dev/null 2>&1 || { report_result fail "Hedef sürüm bulunamadı: $target_ref" 0; exit 1; }
  UPD_TARGET_VERSION=${target_ref#refs/tags/}

  if [ "$assume_yes" != 1 ]; then
    interactive || { echo "Etkileşimsiz çalıştırma için --yes gerekli." >&2; exit 2; }
    ask_yesno "  $UPD_FROM_VERSION -> $target_ref güncellensin mi? (yedek otomatik alınır)" h || { echo "Vazgeçildi."; exit 0; }
  fi

  # 2) Anlik durum (geri donus noktasi)
  report_phase snapshot "geri dönüş noktası kaydediliyor"
  UPD_PRE_MIGRATE_VERSION=$(db_migration_version)
  printf 'prev_commit=%s\nprev_release=%s\ntarget_version=%s\npre_migrate_version=%s\nstarted_at=%s\n' \
    "$UPD_PREV_COMMIT" "$UPD_PREV_RELEASE" "$UPD_TARGET_VERSION" "${UPD_PRE_MIGRATE_VERSION:-}" "$(date -u +%FT%TZ)" \
    > "$project_dir/deploy/.update-rollback"
  for _svc in api worker frontend migrate; do
    $DK image inspect "varyaone-${_svc}:latest" >/dev/null 2>&1 && \
      $DK image tag "varyaone-${_svc}:latest" "varyaone-${_svc}:preupdate" 2>/dev/null || true
  done
  # Buradan sonra herhangi bir beklenmedik çıkış (hata/sinyal) otomatik geri alır.
  trap 'rollback_update interrupted "$UPD_MIGRATED"' EXIT INT TERM

  # 3) DB + dosya yedegi — sistem daha once kurulduysa ZORUNLU ve DOGRULANIR.
  report_phase backup "tam sistem yedeği alınıyor"
  if system_installed; then
    _tag=$(printf '%s' "$target_ref" | sed 's#refs/tags/##; s#[^A-Za-z0-9._-]#-#g')
    UPD_PRE_BACKUP="$project_dir/backups/pre-update-${_tag}-$(date -u +%Y%m%dT%H%M%SZ).varya"
    if ! compose exec -T api varyaone backup create - > "$UPD_PRE_BACKUP.partial" 2>>"$UPD_LOG"; then
      rm -f "$UPD_PRE_BACKUP.partial"
      trap - EXIT INT TERM
      report_result fail "Yedek alınamadı; güncelleme durduruldu (hiçbir şey değişmedi)." 0
      rm -f "$project_dir/deploy/.update-rollback"; release_lock
      exit 1
    fi
    mv "$UPD_PRE_BACKUP.partial" "$UPD_PRE_BACKUP"
    if ! verify_backup_file "$UPD_PRE_BACKUP"; then
      rm -f "$UPD_PRE_BACKUP"
      trap - EXIT INT TERM
      report_result fail "Yedek doğrulanamadı; güncelleme durduruldu (hiçbir şey değişmedi)." 0
      rm -f "$project_dir/deploy/.update-rollback"; release_lock
      exit 1
    fi
    if ! sha256sum "$UPD_PRE_BACKUP" > "$UPD_PRE_BACKUP.sha256" 2>/dev/null; then
      report_phase backup "UYARI: sha256sum yok — yedek sağlama dosyası yazılamadı"
    fi
    report_phase backup "yedek doğrulandı: $(basename "$UPD_PRE_BACKUP")"
    printf 'pre_backup=%s\n' "$UPD_PRE_BACKUP" >> "$project_dir/deploy/.update-rollback"
    # Son 3 on-guncelleme yedegini tut.
    ls -1t "$project_dir"/backups/pre-update-*.varya 2>/dev/null | tail -n +4 | while read -r _old; do
      rm -f "$_old" "$_old.sha256"
    done
  else
    report_phase backup "sistem henüz kurulmamış — yedek atlanıyor (ilk kurulum gibi)"
  fi

  # 4) Kaynak kodu hedef surume getir
  report_phase fetch "kaynak kod güncelleniyor ($target_ref)"
  git checkout --quiet --force --detach "$target_ref" >>"$UPD_LOG" 2>&1 || rollback_update fetch 0
  UPD_NEW_RELEASE=$(current_release)
  env_set VARYAONE_RELEASE "$UPD_NEW_RELEASE"

  # 5) Yeni image'lari derle (DB'ye henuz dokunulmadi)
  report_phase build "docker görüntüleri derleniyor"
  compose build >>"$UPD_LOG" 2>&1 || rollback_update build 0

  # 5b) PostgreSQL majör yükseltmesi (örn. 18 -> 19). Aynı majörde bütünüyle
  # atlanır. `postgres` imajı sürümlü PGDATA kullanır (/var/lib/postgresql/<majör>/
  # docker), dolayısıyla yeni imaj eski veriye DOKUNMADAN yeni majör dizinine boş
  # initdb yapar. Yapılacak tek şey güncelleme öncesi .varya dökümünü yeni majöre
  # yüklemek. Eski majör dizini volume'de kalır; hata halinde rollback eski imaja
  # dönünce o dizin bozulmadan devreye girer (yıkıcı geri yükleme gerekmez).
  UPD_PG_UPGRADED=0
  _pg_run=$(pg_server_major)
  _pg_new=$(compose_pg_major)
  if [ -n "$_pg_run" ] && [ -n "$_pg_new" ] && [ "$_pg_new" -gt "$_pg_run" ]; then
    report_phase pg-upgrade "PostgreSQL $_pg_run -> $_pg_new: veritabanı taşınıyor"
    if [ -z "$UPD_PRE_BACKUP" ] || [ ! -f "$UPD_PRE_BACKUP" ]; then
      report_phase pg-upgrade "majör yükseltme için doğrulanmış yedek yok"
      rollback_update pg-upgrade 0
    fi
    UPD_PG_UPGRADED=1
    UPD_PG_FROM_MAJOR=$_pg_run
    sed -i "s/^started_at=/pg_upgraded=1\npg_from_major=$_pg_run\nstarted_at=/" "$project_dir/deploy/.update-rollback" 2>/dev/null || true
    compose stop >>"$UPD_LOG" 2>&1
    # --no-deps: `migrate` servisini ÇALIŞTIRMA. Geri yükleme tertemiz boş bir
    # veritabanına gitmeli; şema sonraki `migrate` adımında kurulur.
    compose up -d --no-deps postgres >>"$UPD_LOG" 2>&1 || rollback_update pg-upgrade 0
    _t=0; while [ "$_t" -lt 90 ]; do
      compose exec -T postgres pg_isready >/dev/null 2>&1 && break
      sleep 3; _t=$((_t + 3))
    done
    compose exec -T postgres pg_isready >/dev/null 2>&1 || rollback_update pg-upgrade 0
    compose up -d --no-deps api >>"$UPD_LOG" 2>&1 || rollback_update pg-upgrade 0
    _t=0; while [ "$_t" -lt 60 ]; do
      compose exec -T api true >/dev/null 2>&1 && break
      sleep 3; _t=$((_t + 3))
    done
    if ! compose exec -T api varyaone backup restore - --force < "$UPD_PRE_BACKUP" >>"$UPD_LOG" 2>&1; then
      report_phase pg-upgrade "yeni majöre geri yükleme başarısız"
      rollback_update pg-upgrade 1
    fi
    report_phase pg-upgrade "veritabanı PostgreSQL $_pg_new üzerine yüklendi (şema sürümü $UPD_PRE_MIGRATE_VERSION)"
  fi

  # 6) Migration — donusu olmayan nokta
  report_phase migrate "veritabanı taşınıyor"
  UPD_MIGRATED=1
  sed -i 's/^started_at=/migrated=1\nstarted_at=/' "$project_dir/deploy/.update-rollback" 2>/dev/null || true
  compose run --rm migrate >>"$UPD_LOG" 2>&1 || rollback_update migrate 1

  # 6b) Zorlama zaten açıksa (VARYAONE_APP_DATABASE_URL dolu) varyaone_app
  # grant'lerini güncel şemaya göre tazele — yeni tablolar app rolüne otomatik
  # açılır (ALTER DEFAULT PRIVILEGES) ama bu ekstra bir emniyet ağı. Güncelleme
  # sırasında zorlama KENDİLİĞİNDEN açılmaz; operatör `./deploy.sh` fresh install
  # veya elle etkinleştirir.
  #
  # Bir majör PostgreSQL yükseltmesi veritabanini BOS bir kumeye geri yukler.
  # Roller kume seviyesindedir, yedekle tasinmaz: varyaone_app'i migration
  # 000148 parolasiz ve NOLOGIN olarak yeniden yaratir. Yani bu adim yalnizca
  # bir emniyet agi degil, yukseltmeden sonra rolun tek onarim yeridir.
  #
  # Basarisiz olursa .env'deki DSN giris yapamayan bir role isaret etmeye devam
  # ederdi: api baglanamaz, /health/ready 503 doner ve butun kurulum ayaga
  # kalkmaz. Bunun yerine URL temizlenir; sunucu superuser baglantisina doner
  # (izolasyon yalnizca uygulama yukune dayanir) ve sistem ayakta kalir.
  if [ -n "$(env_get VARYAONE_APP_DATABASE_URL)" ]; then
    if ! ensure_app_role >>"$UPD_LOG" 2>&1; then
      env_set VARYAONE_APP_DATABASE_URL ""
      report_phase migrate "UYARI: varyaone_app rolü kurulamadı — superuser bağlantısına dönüldü. Düzeltmek için: ./deploy.sh repair-app-role"
    fi
  fi

  # 7) Servisleri yeniden baslat
  report_phase restart "servisler yeniden başlatılıyor"
  compose up -d >>"$UPD_LOG" 2>&1 || rollback_update restart 1
  reload_nginx_soft >>"$UPD_LOG" 2>&1

  # 8) Saglik kontrolu
  report_phase healthcheck "sağlık kontrolü (en fazla 3 dk)"
  wait_healthy 180 || rollback_update healthcheck 1

  # 9) Dogrulama
  report_phase verify "sürüm doğrulanıyor"
  compose exec -T api varyaone migrate status >>"$UPD_LOG" 2>&1 || rollback_update verify 1

  # Basari — otomatik geri alma trap'ini kaldır.
  trap - EXIT INT TERM
  for _svc in api worker frontend migrate; do
    $DK image rm "varyaone-${_svc}:preupdate" >/dev/null 2>&1 || true
  done
  $DK image prune -f >/dev/null 2>&1 || true
  # Majör yükseltme başarılıysa eski majörün veri dizinini geri kazan.
  [ "${UPD_PG_UPGRADED:-0}" = 1 ] && pg_drop_old_major_dir "${UPD_PG_FROM_MAJOR:-}"
  rm -f "$project_dir/deploy/.update-rollback"
  report_phase done "tamamlandı — $UPD_FROM_VERSION -> $UPD_NEW_RELEASE"
  report_result ok "" 0
  release_lock
  echo
  echo "  Güncelleme tamamlandı: $UPD_NEW_RELEASE"
}

install_agent() {
  banner "Güncelleme Aracı Kurulumu"
  have systemctl || { echo "systemd (systemctl) bulunamadı; bu host'ta agent kurulamaz." >&2; return 1; }
  [ -f .env ] || { echo ".env yok; önce ./deploy.sh install." >&2; return 1; }
  elevate || { echo "Bu işlem root/sudo gerektirir." >&2; return 1; }

  ensure_update_token

  _run_user=${SUDO_USER:-$(id -un)}
  _unit=/etc/systemd/system/varyaone-update-agent.service
  sed -e "s#__USER__#${_run_user}#g" -e "s#__REPO_DIR__#${project_dir}#g" \
    "$project_dir/deploy/varyaone-update-agent.service" | run_root tee "$_unit" >/dev/null
  run_root chmod 0644 "$_unit"
  run_root systemctl daemon-reload
  run_root systemctl enable --now varyaone-update-agent.service

  # Token .env'e yeni yazilmis olabilir; api'yi onu ortam degiskeni olarak
  # gorecek sekilde yeniden olustur, yoksa /internal/update/* uclari kapali
  # kalir ve agent 401 alir.
  if $DK compose version >/dev/null 2>&1 && [ -n "$(compose ps -q api 2>/dev/null)" ]; then
    echo "  api konteyneri güncel belirteçle yeniden oluşturuluyor..."
    compose up -d --force-recreate api >/dev/null 2>&1 || true
    reload_nginx_soft
  fi

  echo
  echo "  Kuruldu ve başlatıldı (kullanıcı: ${_run_user})."
  echo "  Durum:   systemctl status varyaone-update-agent"
  echo "  Günlük:  journalctl -u varyaone-update-agent -f"
}

uninstall_agent() {
  banner "Güncelleme Aracı Kaldırma"
  have systemctl || { echo "systemd bulunamadı." >&2; return 1; }
  elevate || { echo "Bu işlem root/sudo gerektirir." >&2; return 1; }
  run_root systemctl disable --now varyaone-update-agent.service >/dev/null 2>&1 || true
  run_root rm -f /etc/systemd/system/varyaone-update-agent.service
  run_root systemctl daemon-reload
  echo "  Kaldırıldı. (VARYAONE_UPDATE_AGENT_TOKEN .env içinde kaldı; elle silebilirsiniz.)"
}

# Kurulumun son adimi: systemd varsa guncelleme agent'ini sessizce kur.
# Basarisiz olursa kurulumu bozmaz; sadece uyarir.
post_install_agent() {
  have systemctl || { echo "  Not: systemd yok — otomatik güncelleme aracı atlandı."; return 0; }
  if ! elevate 2>/dev/null; then
    echo "  Not: otomatik güncelleme aracı için yetki yok. Sonra: sudo ./deploy.sh install-agent"
    return 0
  fi
  echo
  echo "Otomatik güncelleme aracı kuruluyor (varyaone-update-agent.service)..."
  if install_agent; then
    :
  else
    echo "  Uyarı: güncelleme aracı kurulamadı. Sonra deneyin: ./deploy.sh install-agent" >&2
  fi
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
warn()  { _row '\033[33m!\033[0m' "${1:-}"; }
fail()  { _row '\033[31m✗\033[0m' "${1:-}"; DOCTOR_FAILED=1; }

doctor() {
  banner "Ortam Denetimi"
  DOCTOR_FAILED=0

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

    check "SSL sertifikası"
    if compose run --rm --no-deps --entrypoint sh certbot -c "test -s /etc/letsencrypt/live/$_d/fullchain.pem" >/dev/null 2>&1; then
      _exp=$(compose run --rm --no-deps --entrypoint sh certbot -c \
        "openssl x509 -enddate -noout -in /etc/letsencrypt/live/$_d/fullchain.pem 2>/dev/null | cut -d= -f2" 2>/dev/null | tr -d '\r')
      pass "geçerli${_exp:+ (bitiş: $_exp)}"
    else
      warn "henüz alınmadı — ./deploy.sh install"
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
    warn "varyaone_app giriş YAPAMIYOR — api bu yüzden hazır olmaz. Düzelt: ./deploy.sh repair-app-role"
  fi

  check "Servis sağlığı"
  if compose exec -T api wget -q -O - http://127.0.0.1:8080/health/ready >/dev/null 2>&1; then
    pass "api /health/ready → ok"
  else
    warn "api yanıt vermiyor (kurulu değil veya durdurulmuş olabilir)"
  fi

  check "Çalışan sürüm"
  _rel=$(env_get VARYAONE_RELEASE); _git=$(current_release)
  pass "${_rel:-?} (git: ${_git})"

  check "Güncelleme aracı"
  if have systemctl && run_root systemctl is-active --quiet varyaone-update-agent 2>/dev/null; then
    pass "varyaone-update-agent çalışıyor"
  elif have systemctl && [ -f /etc/systemd/system/varyaone-update-agent.service ]; then
    warn "kurulu ama çalışmıyor — systemctl start varyaone-update-agent"
  elif [ -z "$(env_get VARYAONE_UPDATE_AGENT_TOKEN)" ]; then
    warn "belirteç yok — otomatik güncelleme kapalı"
  else
    warn "kurulu değil — ./deploy.sh install-agent"
  fi

  check "Yarım kalan güncelleme"
  if [ -f "$project_dir/deploy/.update-rollback" ]; then
    fail "önceki güncelleme tamamlanmamış — ./deploy.sh recover"
  else
    pass "yok"
  fi

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
  if [ "$DOCTOR_FAILED" = 1 ]; then
    echo "  Bazı kontroller başarısız — yukarıdaki ✗ satırlarını giderin." >&2
    exit 1
  fi
  echo "  Tüm kontroller başarılı."
}

backup() {
  require_docker
  [ -f .env ] || { echo ".env bulunamadı." >&2; exit 1; }
  compose ps --status running api 2>/dev/null | grep -q api || {
    echo "api servisi çalışmıyor; önce ./deploy.sh install veya rebuild." >&2; exit 1
  }
  acquire_lock update
  trap 'release_lock' EXIT INT TERM
  mkdir -p "$project_dir/backups"
  timestamp=$(date -u +%Y%m%dT%H%M%SZ)
  out="$project_dir/backups/varyaone-$timestamp.varya"
  echo "Tam sistem yedeği alınıyor (veritabanı + dosyalar)..."
  if compose exec -T api varyaone backup create - > "$out.partial" 2>/dev/null; then
    mv "$out.partial" "$out"
  else
    rm -f "$out.partial"
    echo "Yedek başarısız." >&2
    exit 1
  fi
  # Yedeği doğrula — bozuk bir yedeğin "başarılı" görünmesine izin verme.
  if ! verify_backup_file "$out"; then
    rm -f "$out"
    echo "Yedek doğrulanamadı; dosya silindi." >&2
    exit 1
  fi
  if ! sha256sum "$out" > "$out.sha256" 2>/dev/null; then
    echo "  UYARI: sha256sum yok — sağlama dosyası yazılamadı." >&2
  fi
  size=$(du -h "$out" 2>/dev/null | awk '{print $1}')
  echo "Yedek oluşturuldu ve doğrulandı: $out${size:+ ($size)}"
}

restore() {
  require_docker
  file=${1:-}
  shift 2>/dev/null || true
  confirm=""
  force=""
  for arg in "$@"; do
    case "$arg" in
      --confirm) confirm=1 ;;
      --force) force="--force" ;;
      *) echo "Bilinmeyen seçenek: $arg" >&2; exit 2 ;;
    esac
  done
  [ -n "$file" ] && [ -f "$file" ] || { echo "Geçerli bir .varya dosyası verin." >&2; exit 1; }
  [ "$confirm" = "1" ] || { echo "Geri yükleme mevcut veriyi SİLER. Onaylayın:" >&2
    echo "  ./deploy.sh restore \"$file\" --confirm" >&2; exit 1; }

  acquire_lock update
  trap 'release_lock' EXIT INT TERM

  if [ -f "$file.sha256" ]; then
    echo "Sağlama doğrulanıyor..."
    (cd "$(dirname "$file")" && sha256sum -c "$(basename "$file").sha256") \
      || { echo "Sağlama doğrulaması başarısız — dosya bozuk." >&2; exit 1; }
  else
    echo "  UYARI: $file.sha256 yok — bütünlük doğrulanamıyor." >&2
  fi
  # Arşivin kendi manifest sağlamalarını da doğrula (DB'ye dokunmadan).
  if compose ps --status running api 2>/dev/null | grep -q api; then
    echo "Arşiv bütünlüğü doğrulanıyor..."
    compose exec -T api varyaone backup verify - < "$file" \
      || { echo "Arşiv doğrulaması başarısız — geri yükleme durduruldu." >&2; exit 1; }
  fi

  # Geri yükleme yıkıcı: önce mevcut durumun güvenlik yedeğini al.
  if system_installed; then
    _safety="$project_dir/backups/pre-restore-$(date -u +%Y%m%dT%H%M%SZ).varya"
    echo "Güvenlik yedeği alınıyor: $(basename "$_safety")"
    if compose exec -T api varyaone backup create - > "$_safety.partial" 2>/dev/null && \
       mv "$_safety.partial" "$_safety" && verify_backup_file "$_safety"; then
      sha256sum "$_safety" > "$_safety.sha256" 2>/dev/null || true
    else
      rm -f "$_safety.partial" "$_safety"
      echo "  UYARI: güvenlik yedeği alınamadı; yine de devam ediliyor." >&2
      _safety=""
    fi
  fi

  echo "Geri yükleniyor (worker/frontend durduruluyor)..."
  compose stop worker frontend >/dev/null 2>&1 || true
  if compose exec -T api varyaone backup restore - $force < "$file"; then
    compose up -d --force-recreate api >/dev/null 2>&1 || compose restart api >/dev/null 2>&1 || true
    compose up -d worker frontend >/dev/null 2>&1 || true
    reload_nginx_soft
    echo "Geri yükleme tamamlandı. ./deploy.sh doctor ile doğrulayın."
    [ -n "${_safety:-}" ] && echo "  Önceki durumun yedeği: $_safety"
  else
    compose up -d worker frontend >/dev/null 2>&1 || true
    reload_nginx_soft
    echo "Geri yükleme başarısız (farklı MASTER_KEY veya daha yeni sürüm ise --force gerekebilir)." >&2
    [ -n "${_safety:-}" ] && echo "  Sistem değişmedi olabilir; gerekirse önceki durum: $_safety" >&2
    exit 1
  fi
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

  echo "  Bu işlem KALICI olarak şunları siler:"
  echo "    • Tüm Varya One konteynerleri ve ağları"
  echo "    • Docker volume'leri — PostgreSQL veritabanı ve yüklenen dosyalar DAHİL"
  echo "    • Derlenen image'lar (varyaone-*) + çekilen nginx/certbot/postgres image'ları"
  echo "    • Üretilen .env (+ geçici kopyaları), nginx yapılandırması, kilitler, güncelleme günlükleri"
  echo "    • systemd güncelleme aracı (varyaone-update-agent)"
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
    $DK compose -f compose.yaml -f "$DOMAIN_OVERRIDE" down --volumes --remove-orphans --rmi local >/dev/null 2>&1 || true
    $DK compose -f compose.yaml down --volumes --remove-orphans --rmi local >/dev/null 2>&1 || true
    # Compose 'down -v' yalnızca anonim/harici olmayan volume'leri siler; kalanları temizle.
    for _v in varyaone_postgres-data varyaone_storage-data varyaone_letsencrypt varyaone_certbot-webroot; do
      $DK volume rm "$_v" >/dev/null 2>&1 || true
    done
    for _svc in api worker frontend migrate backend-tests; do
      $DK image rm "varyaone-${_svc}:latest" "varyaone-${_svc}:preupdate" >/dev/null 2>&1 || true
    done
    # compose dosyalarının çektiği sabitlenmiş üçüncü taraf image'lar (nginx,
    # certbot, postgres). Yalnızca bu yığın kullandığı için kaldırılıyor;
    # başka bir şey referans veriyorsa `image rm` zaten sessizce başarısız olur.
    for _img in nginx:1.27-alpine certbot/certbot:latest postgres:18.4-alpine; do
      $DK image rm "$_img" >/dev/null 2>&1 || true
    done
  else
    echo "  Docker erişilemiyor — konteyner/volume temizliği atlanıyor (dosyalar yine silinecek)." >&2
  fi

  # 2) systemd güncelleme aracı.
  if have systemctl && { [ -f /etc/systemd/system/varyaone-update-agent.service ] || run_root systemctl is-enabled varyaone-update-agent.service >/dev/null 2>&1; }; then
    if elevate 2>/dev/null; then
      echo "  systemd güncelleme aracı kaldırılıyor..."
      run_root systemctl disable --now varyaone-update-agent.service >/dev/null 2>&1 || true
      run_root rm -f /etc/systemd/system/varyaone-update-agent.service
      run_root systemctl daemon-reload >/dev/null 2>&1 || true
    else
      echo "  Uyarı: yetki yok — systemd aracı kalabilir: sudo ./deploy.sh uninstall-agent" >&2
    fi
  fi

  # 3) Üretilen dosyalar.
  echo "  Üretilen dosyalar siliniyor..."
  # .env ve yarıda kalmış geçici kopyaları (env_set `.env.tmp.XXXXXX` üretir;
  # bunlar şifreleme anahtarını içerebilir — mutlaka temizle).
  rm -f "$project_dir/.env" "$project_dir/.env.tmp" "$project_dir/.env.partial"
  rm -f "$project_dir"/.env.tmp.*
  rm -f "$NGINX_CONF_DIR"/*.conf
  rm -f "$project_dir/deploy/.update-rollback"
  rm -f "$project_dir"/deploy/update-*.log
  rm -f "$project_dir"/deploy/.deploy-self.*
  rm -rf "$project_dir"/deploy/.lock-*
  [ "$keep_backups" = 1 ] || rm -rf "$project_dir/backups"

  # 4) Proje dizininin kendisi (isteğe bağlı, hiçbir iz bırakmaz).
  if [ "$purge" = 1 ]; then
    echo "  Proje dizini siliniyor: $project_dir"
    echo
    echo "  Varya One tamamen kaldırıldı."
    # Betik hâlâ bu dizinden çalışıyor; dizini biz çıktıktan sonra sil.
    _self=$(mktemp /tmp/varyaone-uninstall.XXXXXX) || _self=/tmp/varyaone-uninstall.$$
    cat > "$_self" <<EOF
#!/bin/sh
sleep 1
rm -rf -- "$project_dir"
rm -f -- "$_self"
EOF
    chmod +x "$_self"
    cd /
    if have setsid; then
      setsid "$_self" >/dev/null 2>&1 &
    elif have nohup; then
      nohup "$_self" >/dev/null 2>&1 &
    else
      "$_self" >/dev/null 2>&1 &
    fi
    exit 0
  fi

  echo
  echo "  Varya One kaldırıldı. Proje dosyaları (git deposu) yerinde bırakıldı."
  echo "  Tümüyle silmek için:  rm -rf \"$project_dir\""
}

case "${1:-}" in
  install) shift; install_stack "$@" ;;
  bootstrap) bootstrap ;;
  rebuild) shift; rebuild "$@" ;;
  update) shift; run_update "$@" ;;
  recover) recover_update ;;
  install-agent) install_agent || exit 1 ;;
  uninstall-agent) uninstall_agent || exit 1 ;;
  status) show_status ;;
  doctor) doctor ;;
  repair-app-role) repair_app_role ;;
  renew-cert) renew_cert ;;
  backup) backup ;;
  restore) shift; restore "$@" ;;
  uninstall) shift; uninstall "$@" ;;
  *) usage ;;
esac
