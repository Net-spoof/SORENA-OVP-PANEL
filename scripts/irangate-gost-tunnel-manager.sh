#!/usr/bin/env bash
set -Euo pipefail

VERSION="1.1.0"
BASE_DIR="/etc/irangate-gost"
CONF_DIR="$BASE_DIR/tunnels"
GOST_IMAGE="${GOST_IMAGE:-ginuerzh/gost:latest}"

R='\033[0m'; B='\033[1m'; G='\033[32m'; Y='\033[33m'; C='\033[36m'; RED='\033[31m'
info(){ echo -e "${C}[INFO]${R} $*"; }
ok(){ echo -e "${G}[OK]${R} $*"; }
warn(){ echo -e "${Y}[WARN]${R} $*"; }
fail(){ echo -e "${RED}[FAIL]${R} $*"; }
die(){ echo -e "${RED}Error:${R} $*" >&2; exit 1; }
pause(){ echo; read -r -p "Press Enter to continue..." _; }
confirm(){ local a; read -r -p "${1:-Continue?} [y/N]: " a; [[ "${a,,}" =~ ^(y|yes)$ ]]; }

[[ $EUID -eq 0 ]] || die "Run as root."
mkdir -p "$CONF_DIR"
chmod 700 "$BASE_DIR" "$CONF_DIR"

valid_port(){ [[ "${1:-}" =~ ^[0-9]+$ ]] && (( 10#$1>=1 && 10#$1<=65535 )); }
valid_ttl(){ [[ "${1:-}" =~ ^[0-9]+$ ]] && (( 10#$1>=10 && 10#$1<=86400 )); }
valid_host(){ [[ -n "${1:-}" && "$1" =~ ^[A-Za-z0-9._:-]+$ && "$1" != *"/"* && "$1" != *"?"* ]]; }
valid_ipv4(){
  local ip="${1:-}" IFS=. o; local -a p
  read -ra p <<<"$ip"; [[ ${#p[@]} -eq 4 ]] || return 1
  for o in "${p[@]}"; do [[ "$o" =~ ^[0-9]+$ ]] && ((10#$o<=255)) || return 1; done
}
slugify(){
  local s="${1,,}"
  s="$(echo "$s" | tr ' ' '-' | sed -E 's/[^a-z0-9._-]+/-/g;s/^-+//;s/-+$//;s/-+/-/g')"
  [[ -n "$s" ]] || s=tunnel
  echo "${s:0:40}"
}
normalize_port_csv(){
  local raw="${1:-}" compact token normalized=""
  local -a items=()
  compact="$(printf '%s' "$raw" | tr -d '[:space:]')"
  [[ -n "$compact" ]] || return 1
  IFS=',' read -r -a items <<<"$compact"
  ((${#items[@]} > 0)) || return 1
  for token in "${items[@]}"; do
    valid_port "$token" || return 1
    token="$((10#$token))"
    [[ ",$normalized," != *",$token,"* ]] || return 1
    normalized="${normalized:+$normalized,}$token"
  done
  printf '%s\n' "$normalized"
}
csv_to_array(){
  local csv=$1 name=$2
  local -n ref="$name"
  IFS=',' read -r -a ref <<<"$csv"
}
csv_count(){
  local -a values=()
  csv_to_array "$1" values
  printf '%s\n' "${#values[@]}"
}
csv_contains(){
  local csv=$1 wanted=$2 value
  local -a values=()
  csv_to_array "$csv" values
  for value in "${values[@]}"; do [[ "$value" == "$wanted" ]] && return 0; done
  return 1
}
first_csv_value(){ printf '%s\n' "${1%%,*}"; }
prompt_text(){ local var=$1 prompt=$2 value=; while [[ -z "$value" ]]; do read -r -p "$prompt: " value; done; printf -v "$var" %s "$value"; }
prompt_ports(){
  local var=$1 prompt=$2 def="${3:-}" value normalized
  while true; do
    read -r -p "$prompt${def:+ [$def]}: " value
    value="${value:-$def}"
    if normalized="$(normalize_port_csv "$value")"; then
      printf -v "$var" %s "$normalized"
      return 0
    fi
    warn "Enter unique UDP ports separated by commas (example: 443 or 443,500,4500)."
  done
}
prompt_host(){
  local var=$1 prompt=$2 def="${3:-}" value
  while true; do
    read -r -p "$prompt${def:+ [$def]}: " value; value="${value:-$def}"
    valid_host "$value" && { printf -v "$var" %s "$value"; return; }
    warn "Enter a valid IP or hostname."
  done
}
prompt_ttl(){
  local var=$1 value
  while true; do
    read -r -p "UDP session TTL [300]: " value; value="${value:-300}"
    valid_ttl "$value" && { printf -v "$var" %s "$((10#$value))"; return; }
    warn "Enter TTL between 10 and 86400 seconds."
  done
}

ensure_deps(){
  local need=0 x
  for x in docker iptables tcpdump ss ping; do command -v "$x" >/dev/null 2>&1 || need=1; done
  if ((need)); then
    info "Installing dependencies..."
    export DEBIAN_FRONTEND=noninteractive
    apt-get update
    apt-get install -y docker.io iptables tcpdump iproute2 iputils-ping ca-certificates curl
  fi
  systemctl enable --now docker >/dev/null 2>&1 || true
  docker info >/dev/null 2>&1 || die "Docker is not running."
}
pull_image(){ docker image inspect "$GOST_IMAGE" >/dev/null 2>&1 || { info "Pulling $GOST_IMAGE..."; docker pull "$GOST_IMAGE"; }; }

conf_path(){ echo "$CONF_DIR/$1.conf"; }
all_confs(){ find "$CONF_DIR" -maxdepth 1 -type f -name '*.conf' -print 2>/dev/null | sort; }
has_tunnels(){ find "$CONF_DIR" -maxdepth 1 -type f -name '*.conf' -print -quit 2>/dev/null | grep -q .; }
save_conf(){
  local file=$1 pair key value; shift
  : >"$file"; chmod 600 "$file"
  for pair in "$@"; do key="${pair%%=*}"; value="${pair#*=}"; printf '%s=%q\n' "$key" "$value" >>"$file"; done
}
load_conf(){
  unset ID ROLE NAME CONTAINER CREATED_AT TTL LISTEN_PORT LISTEN_PORTS FOREIGN_HOST FOREIGN_GOST_PORT FOREIGN_GOST_PORTS TARGET_HOST TARGET_PORT TARGET_PORTS ALLOWED_IRAN_IP
  # shellcheck disable=SC1090
  source "$1"
  LISTEN_PORTS="${LISTEN_PORTS:-${LISTEN_PORT:-}}"
  LISTEN_PORT="${LISTEN_PORT:-$(first_csv_value "$LISTEN_PORTS")}"
  if [[ "$ROLE" == iran ]]; then
    FOREIGN_GOST_PORTS="${FOREIGN_GOST_PORTS:-${FOREIGN_GOST_PORT:-}}"
    FOREIGN_GOST_PORT="${FOREIGN_GOST_PORT:-$(first_csv_value "$FOREIGN_GOST_PORTS")}"
  else
    TARGET_PORTS="${TARGET_PORTS:-${TARGET_PORT:-}}"
    TARGET_PORT="${TARGET_PORT:-$(first_csv_value "$TARGET_PORTS")}"
  fi
}
container_running(){ [[ "$(docker inspect -f '{{.State.Running}}' "$1" 2>/dev/null || true)" == true ]]; }
container_exists(){ docker inspect "$1" >/dev/null 2>&1; }
udp_listening(){ ss -H -lun "sport = :$1" 2>/dev/null | grep -q .; }
managed_conflict(){
  local role=$1 port=$2 f existing
  local -a ports=()
  while IFS= read -r f; do
    [[ -n "$f" ]] || continue
    load_conf "$f"
    [[ "$ROLE" == "$role" ]] || continue
    csv_to_array "$LISTEN_PORTS" ports
    for existing in "${ports[@]}"; do
      [[ "$existing" == "$port" ]] && { echo "$NAME ($ID)"; return 0; }
    done
  done < <(all_confs)
  return 1
}
validate_parallel_ports(){
  local left=$1 right=$2 left_name=$3 right_name=$4 lc rc
  lc="$(csv_count "$left")"; rc="$(csv_count "$right")"
  [[ "$lc" == "$rc" ]] || die "$left_name contains $lc port(s), but $right_name contains $rc. Counts must match."
}
assert_listen_ports_free(){
  local role=$1 csv=$2 port conflict
  local -a ports=()
  csv_to_array "$csv" ports
  for port in "${ports[@]}"; do
    if conflict="$(managed_conflict "$role" "$port" 2>/dev/null)"; then
      die "UDP port $port is already managed by: $conflict"
    fi
    udp_listening "$port" && die "UDP port $port already has a listener. Stop or remove the conflicting service first."
  done
}

ipt_add(){ if [[ $1 == filter ]]; then shift; iptables -C "$@" >/dev/null 2>&1 || iptables -I "$@"; else local t=$1; shift; iptables -t "$t" -C "$@" >/dev/null 2>&1 || iptables -t "$t" -I "$@"; fi; }
ipt_del(){
  if [[ $1 == filter ]]; then shift; while iptables -C "$@" >/dev/null 2>&1; do iptables -D "$@" || break; done
  else local t=$1; shift; while iptables -t "$t" -C "$@" >/dev/null 2>&1; do iptables -t "$t" -D "$@" || break; done; fi
}
add_iran_rule(){
  local id=$1 lp=$2 host=$3 fp=$4 tag="irangate-gost:$id"
  ipt_add filter INPUT -p udp --dport "$lp" -m comment --comment "$tag:input" -j ACCEPT
  valid_ipv4 "$host" && ipt_add filter OUTPUT -p udp -d "$host" --dport "$fp" -m comment --comment "$tag:output" -j ACCEPT || true
}
del_iran_rule(){
  local id=$1 lp=$2 host=$3 fp=$4 tag="irangate-gost:$id"
  ipt_del filter INPUT -p udp --dport "$lp" -m comment --comment "$tag:input" -j ACCEPT
  valid_ipv4 "$host" && ipt_del filter OUTPUT -p udp -d "$host" --dport "$fp" -m comment --comment "$tag:output" -j ACCEPT || true
}
add_foreign_rule(){
  local id=$1 lp=$2 allowed=$3 tag="irangate-gost:$id"
  if [[ -n "$allowed" ]]; then ipt_add filter INPUT -p udp -s "$allowed" --dport "$lp" -m comment --comment "$tag:input" -j ACCEPT
  else ipt_add filter INPUT -p udp --dport "$lp" -m comment --comment "$tag:input" -j ACCEPT; fi
}
del_foreign_rule(){
  local id=$1 lp=$2 allowed=$3 tag="irangate-gost:$id"
  if [[ -n "$allowed" ]]; then ipt_del filter INPUT -p udp -s "$allowed" --dport "$lp" -m comment --comment "$tag:input" -j ACCEPT
  else ipt_del filter INPUT -p udp --dport "$lp" -m comment --comment "$tag:input" -j ACCEPT; fi
}
add_iran_rules(){
  local id=$1 listeners=$2 host=$3 foreign_ports=$4 i
  local -a lps=() fps=()
  csv_to_array "$listeners" lps; csv_to_array "$foreign_ports" fps
  for i in "${!lps[@]}"; do add_iran_rule "$id" "${lps[$i]}" "$host" "${fps[$i]}" || return 1; done
}
del_iran_rules(){
  local id=$1 listeners=$2 host=$3 foreign_ports=$4 i
  local -a lps=() fps=()
  csv_to_array "$listeners" lps; csv_to_array "$foreign_ports" fps
  for i in "${!lps[@]}"; do del_iran_rule "$id" "${lps[$i]}" "$host" "${fps[$i]}" || true; done
}
add_foreign_rules(){
  local id=$1 listeners=$2 allowed=$3 lp
  local -a lps=()
  csv_to_array "$listeners" lps
  for lp in "${lps[@]}"; do add_foreign_rule "$id" "$lp" "$allowed" || return 1; done
}
del_foreign_rules(){
  local id=$1 listeners=$2 allowed=$3 lp
  local -a lps=()
  csv_to_array "$listeners" lps
  for lp in "${lps[@]}"; do del_foreign_rule "$id" "$lp" "$allowed" || true; done
}

maybe_fix_openvpn_low_ports(){
  local target=$1 target_ports=$2 port
  [[ "$target" == 127.0.0.1 || "$target" == localhost || "$target" == ::1 ]] || return 0
  port="$(awk '$1=="port" && $2 ~ /^[0-9]+$/ {print $2; exit}' /etc/openvpn/server.conf 2>/dev/null || true)"
  valid_port "$port" || return 0
  port="$((10#$port))"
  csv_contains "$target_ports" "$port" || return 0
  ((port<1024)) || return 0
  systemctl list-unit-files openvpn@server.service >/dev/null 2>&1 || { warn "openvpn@server not detected; capability fix skipped."; return 0; }
  confirm "OpenVPN target includes privileged port $port. Apply CAP_NET_BIND_SERVICE fix?" || return 0
  mkdir -p /etc/systemd/system/openvpn@server.service.d
  rm -f /etc/systemd/system/openvpn@server.service.d/cap-net-bind.conf
  systemctl daemon-reload
  systemctl reset-failed openvpn@server >/dev/null 2>&1 || true
  systemctl restart openvpn@server
  systemctl is-active --quiet openvpn@server && ok "openvpn@server is active." || warn "OpenVPN restart failed; inspect journalctl."
}
build_iran_gost_args(){
  local listeners=$1 host=$2 foreign_ports=$3 ttl=$4 output_name=$5 i
  local -n output="$output_name"
  local -a lps=() fps=()
  csv_to_array "$listeners" lps; csv_to_array "$foreign_ports" fps
  output=()
  for i in "${!lps[@]}"; do output+=("-L=udp://:${lps[$i]}/$host:${fps[$i]}?ttl=$ttl"); done
}
build_foreign_gost_args(){
  local listeners=$1 target=$2 target_ports=$3 ttl=$4 output_name=$5 i
  local -n output="$output_name"
  local -a lps=() tps=()
  csv_to_array "$listeners" lps; csv_to_array "$target_ports" tps
  output=()
  for i in "${!lps[@]}"; do output+=("-L=udp://:${lps[$i]}/$target:${tps[$i]}?ttl=$ttl"); done
}
mapping_lines(){
  local listeners=$1 target_host=$2 target_ports=$3 prefix=${4:-} i
  local -a lps=() tps=()
  csv_to_array "$listeners" lps; csv_to_array "$target_ports" tps
  for i in "${!lps[@]}"; do printf '%s%s/udp -> %s:%s/udp\n' "$prefix" "${lps[$i]}" "$target_host" "${tps[$i]}"; done
}
compact_mapping(){
  local listeners=$1 target_host=$2 target_ports=$3 count
  count="$(csv_count "$listeners")"
  if ((count==1)); then printf ':%s -> %s:%s' "$listeners" "$target_host" "$target_ports"
  else printf '%s ports -> %s [%s]' "$count" "$target_host" "$target_ports"; fi
}

create_iran(){
  local name id container listeners host foreign_ports ttl
  local -a gost_args=()
  echo -e "\n${B}Create Iran-side Tunnel${R}"
  echo "USER_PORTS/UDP -> FOREIGN_HOST:GOST_PORTS/UDP"
  prompt_text name "Tunnel name"
  id="$(slugify "$name")"; [[ -e "$(conf_path "$id")" ]] && id="$id-$(date +%s)"
  prompt_ports listeners "User-facing UDP port(s) on Iran, comma-separated" 443
  prompt_host host "Foreign server IP or hostname"
  prompt_ports foreign_ports "GOST UDP port(s) on foreign server, comma-separated" 8080
  prompt_ttl ttl
  validate_parallel_ports "$listeners" "$foreign_ports" "Iran listener list" "Foreign GOST list"
  assert_listen_ports_free iran "$listeners"
  ensure_deps; pull_image
  build_iran_gost_args "$listeners" "$host" "$foreign_ports" "$ttl" gost_args
  container="irangate-gost-iran-$id"
  docker rm -f "$container" >/dev/null 2>&1 || true
  if ! docker run -d --name "$container" --restart always --network host \
    --label irangate-gost.managed=true --label irangate-gost.role=iran --label "irangate-gost.id=$id" \
    "$GOST_IMAGE" "${gost_args[@]}" >/dev/null; then
    docker rm -f "$container" >/dev/null 2>&1 || true
    die "Docker could not create the Iran tunnel container."
  fi
  sleep 1
  if ! container_running "$container"; then
    fail "Container exited during startup."; docker logs --tail 100 "$container" 2>&1 || true
    docker rm -f "$container" >/dev/null 2>&1 || true
    return 1
  fi
  if ! add_iran_rules "$id" "$listeners" "$host" "$foreign_ports"; then
    del_iran_rules "$id" "$listeners" "$host" "$foreign_ports"; docker rm -f "$container" >/dev/null 2>&1 || true
    die "Firewall setup failed; tunnel rolled back."
  fi
  if ! save_conf "$(conf_path "$id")" \
    "ID=$id" "ROLE=iran" "NAME=$name" "CONTAINER=$container" "CREATED_AT=$(date -Is)" "TTL=$ttl" \
    "LISTEN_PORTS=$listeners" "LISTEN_PORT=$(first_csv_value "$listeners")" \
    "FOREIGN_HOST=$host" "FOREIGN_GOST_PORTS=$foreign_ports" "FOREIGN_GOST_PORT=$(first_csv_value "$foreign_ports")"; then
    del_iran_rules "$id" "$listeners" "$host" "$foreign_ports"; docker rm -f "$container" >/dev/null 2>&1 || true
    die "Configuration save failed; tunnel rolled back."
  fi
  ok "Iran tunnel created with $(csv_count "$listeners") UDP mapping(s)."
  mapping_lines "$listeners" "$host" "$foreign_ports" "  "
}

create_foreign(){
  local name id container listeners target target_ports ttl allowed
  local -a gost_args=()
  echo -e "\n${B}Create Foreign-side Tunnel${R}"
  echo "GOST_PORTS/UDP -> LOCAL_TARGET_HOST:TARGET_PORTS/UDP"
  prompt_text name "Tunnel name"
  id="$(slugify "$name")"; [[ -e "$(conf_path "$id")" ]] && id="$id-$(date +%s)"
  prompt_ports listeners "GOST UDP listen port(s) on foreign server, comma-separated" 8080
  prompt_host target "Local target host" 127.0.0.1
  prompt_ports target_ports "Local target UDP port(s), comma-separated" 443
  prompt_ttl ttl
  read -r -p "Allowed Iran IPv4 (blank = allow all): " allowed
  [[ -z "$allowed" ]] || valid_ipv4 "$allowed" || die "Invalid IPv4."
  validate_parallel_ports "$listeners" "$target_ports" "Foreign GOST listener list" "Local target list"
  assert_listen_ports_free foreign "$listeners"
  ensure_deps; pull_image
  maybe_fix_openvpn_low_ports "$target" "$target_ports"
  build_foreign_gost_args "$listeners" "$target" "$target_ports" "$ttl" gost_args
  container="irangate-gost-foreign-$id"
  docker rm -f "$container" >/dev/null 2>&1 || true
  if ! docker run -d --name "$container" --restart always --network host \
    --label irangate-gost.managed=true --label irangate-gost.role=foreign --label "irangate-gost.id=$id" \
    "$GOST_IMAGE" "${gost_args[@]}" >/dev/null; then
    docker rm -f "$container" >/dev/null 2>&1 || true
    die "Docker could not create the foreign tunnel container."
  fi
  sleep 1
  if ! container_running "$container"; then
    fail "Container exited during startup."; docker logs --tail 100 "$container" 2>&1 || true
    docker rm -f "$container" >/dev/null 2>&1 || true
    return 1
  fi
  if ! add_foreign_rules "$id" "$listeners" "$allowed"; then
    del_foreign_rules "$id" "$listeners" "$allowed"; docker rm -f "$container" >/dev/null 2>&1 || true
    die "Firewall setup failed; tunnel rolled back."
  fi
  if ! save_conf "$(conf_path "$id")" \
    "ID=$id" "ROLE=foreign" "NAME=$name" "CONTAINER=$container" "CREATED_AT=$(date -Is)" "TTL=$ttl" \
    "LISTEN_PORTS=$listeners" "LISTEN_PORT=$(first_csv_value "$listeners")" \
    "TARGET_HOST=$target" "TARGET_PORTS=$target_ports" "TARGET_PORT=$(first_csv_value "$target_ports")" \
    "ALLOWED_IRAN_IP=$allowed"; then
    del_foreign_rules "$id" "$listeners" "$allowed"; docker rm -f "$container" >/dev/null 2>&1 || true
    die "Configuration save failed; tunnel rolled back."
  fi
  ok "Foreign tunnel created with $(csv_count "$listeners") UDP mapping(s)."
  mapping_lines "$listeners" "$target" "$target_ports" "  "
}

create_menu(){
  clear; echo -e "${B}Create Tunnel${R}\n"; echo "1) Iran server"; echo "2) Foreign server"; echo "0) Back"; read -r -p "Select role: " c
  case "$c" in 1) create_iran;; 2) create_foreign;; 0) return;; *) warn "Invalid option.";; esac
}
status_word(){ if container_running "$1"; then echo RUNNING; elif container_exists "$1"; then echo STOPPED; else echo MISSING; fi; }
print_table(){
  local i=0 f status listen target
  printf '%-4s %-24s %-9s %-9s %-24s %-42s\n' "#" "NAME" "ROLE" "STATUS" "LISTEN PORTS" "TARGET"
  printf '%-4s %-24s %-9s %-9s %-24s %-42s\n' ---- ------------------------ --------- --------- ------------------------ ------------------------------------------
  while IFS= read -r f; do
    [[ -n "$f" ]] || continue; load_conf "$f"; ((i+=1)); status="$(status_word "$CONTAINER")"; listen="$LISTEN_PORTS/udp"
    [[ "$ROLE" == iran ]] && target="$(compact_mapping "$LISTEN_PORTS" "$FOREIGN_HOST" "$FOREIGN_GOST_PORTS")" || target="$(compact_mapping "$LISTEN_PORTS" "$TARGET_HOST" "$TARGET_PORTS")"
    printf '%-4s %-24s %-9s %-9s %-24s %-42s\n' "$i" "${NAME:0:24}" "$ROLE" "$status" "${listen:0:24}" "${target:0:42}"
  done < <(all_confs)
}
select_tunnel(){
  local -a files=(); local f n
  while IFS= read -r f; do [[ -n "$f" ]] && files+=("$f"); done < <(all_confs)
  ((${#files[@]})) || { warn "No managed tunnels."; return 1; }
  echo; print_table; echo; read -r -p "Select tunnel number (0 = cancel): " n
  [[ "$n" =~ ^[0-9]+$ ]] || { warn "Invalid selection."; return 1; }
  ((n>0 && n<=${#files[@]})) || return 1
  SELECTED_FILE="${files[$((n-1))]}"
}
details_file(){
  load_conf "$1"; local st started restarts
  st="$(status_word "$CONTAINER")"; started="$(docker inspect -f '{{.State.StartedAt}}' "$CONTAINER" 2>/dev/null || echo -)"; restarts="$(docker inspect -f '{{.RestartCount}}' "$CONTAINER" 2>/dev/null || echo -)"
  echo -e "\n${B}${C}Tunnel Details${R}"; echo "------------------------------------------------------------"
  echo "ID            : $ID"; echo "Name          : $NAME"; echo "Role          : $ROLE"; echo "Created       : $CREATED_AT"; echo "Container     : $CONTAINER"; echo "Status        : $st"; echo "Started       : $started"; echo "Restart Count : $restarts"; echo "TTL           : $TTL"; echo "Mappings      : $(csv_count "$LISTEN_PORTS")"
  if [[ "$ROLE" == iran ]]; then mapping_lines "$LISTEN_PORTS" "$FOREIGN_HOST" "$FOREIGN_GOST_PORTS" "  "
  else mapping_lines "$LISTEN_PORTS" "$TARGET_HOST" "$TARGET_PORTS" "  "; echo "Allowed Iran  : ${ALLOWED_IRAN_IP:-ALL}"; fi
  echo; echo "GOST command:"; docker inspect -f '{{json .Config.Cmd}}' "$CONTAINER" 2>/dev/null || echo "Container missing."
}
details(){ select_tunnel && details_file "$SELECTED_FILE"; }
counters(){ echo -e "\nFirewall packet counters:"; iptables -nvL INPUT --line-numbers 2>/dev/null | grep "irangate-gost:$1" || echo "No matching INPUT rule."; iptables -nvL OUTPUT --line-numbers 2>/dev/null | grep "irangate-gost:$1" || true; }
check_file(){
  load_conf "$1"; local bad=0 port
  local -a lps=() tps=()
  echo -e "\n${B}${C}Tunnel Health Check${R}"; echo "Name: $NAME"; echo "Role: $ROLE"; echo "------------------------------------------------------------"
  if container_running "$CONTAINER"; then ok "Container is running."; else fail "Container is not running."; bad=1; fi
  csv_to_array "$LISTEN_PORTS" lps
  for port in "${lps[@]}"; do
    if udp_listening "$port"; then ok "UDP listener detected on $port."; ss -lunp 2>/dev/null | grep -E ":$port[[:space:]]" | head -n 5 || true
    else fail "No UDP listener on $port."; bad=1; fi
  done
  if [[ "$ROLE" == iran ]]; then
    if getent ahostsv4 "$FOREIGN_HOST" >/dev/null 2>&1 || valid_ipv4 "$FOREIGN_HOST"; then ok "Foreign host resolves: $FOREIGN_HOST"; else fail "Foreign host cannot resolve."; bad=1; fi
    ip route get "$FOREIGN_HOST" >/dev/null 2>&1 && { ok "Route to foreign host exists."; ip route get "$FOREIGN_HOST" | head -n1; } || warn "Route check failed."
    ping -c 2 -W 2 "$FOREIGN_HOST" >/dev/null 2>&1 && ok "Foreign host answers ICMP." || warn "No ICMP reply; UDP may still work."
  else
    csv_to_array "$TARGET_PORTS" tps
    for port in "${tps[@]}"; do
      ss -H -lun "sport = :$port" 2>/dev/null | grep -q . && ok "UDP target service detected on $TARGET_HOST:$port." || warn "No local UDP listener detected on target $TARGET_HOST:$port."
    done
    if csv_contains "$TARGET_PORTS" 500 || csv_contains "$TARGET_PORTS" 4500; then systemctl is-active --quiet strongswan && ok "strongswan is active." || warn "strongswan is not active."; fi
  fi
  counters "$ID"; echo -e "\nRecent GOST logs:"; docker logs --tail 30 "$CONTAINER" 2>&1 || true; echo
  ((bad==0)) && { ok "Local health checks passed."; warn "End-to-end UDP validation still requires real client traffic."; } || fail "One or more checks failed."
}
check_tunnel(){ select_tunnel && check_file "$SELECTED_FILE"; }
restart_tunnel(){ select_tunnel || return; load_conf "$SELECTED_FILE"; docker restart "$CONTAINER" >/dev/null; sleep 1; container_running "$CONTAINER" && ok "Tunnel restarted." || { fail "Restart failed."; docker logs --tail 50 "$CONTAINER" 2>&1 || true; }; }
delete_tunnel(){
  select_tunnel || return; local f="$SELECTED_FILE"; load_conf "$f"; details_file "$f"; echo; confirm "Delete this tunnel permanently?" || { info "Cancelled."; return; }
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  if [[ "$ROLE" == iran ]]; then del_iran_rules "$ID" "$LISTEN_PORTS" "$FOREIGN_HOST" "$FOREIGN_GOST_PORTS"; else del_foreign_rules "$ID" "$LISTEN_PORTS" "${ALLOWED_IRAN_IP:-}"; fi
  rm -f "$f"; ok "Tunnel deleted: $NAME"
}
build_tcpdump_filter(){
  local left=$1 right=$2 port expr="" seen=","
  local -a values=() combined=()
  csv_to_array "$left" values; combined+=("${values[@]}")
  csv_to_array "$right" values; combined+=("${values[@]}")
  for port in "${combined[@]}"; do [[ "$seen" == *",$port,"* ]] && continue; seen+="$port,"; expr="${expr:+$expr or }udp port $port"; done
  printf '%s\n' "$expr"
}
traffic_monitor(){
  select_tunnel || return; load_conf "$SELECTED_FILE"; local filter
  echo "Press Ctrl+C to stop."
  [[ "$ROLE" == iran ]] && filter="$(build_tcpdump_filter "$LISTEN_PORTS" "$FOREIGN_GOST_PORTS")" || filter="$(build_tcpdump_filter "$LISTEN_PORTS" "$TARGET_PORTS")"
  echo "Filter: $filter"; tcpdump -ni any "$filter"
}

tunnels_menu(){
  while true; do
    clear; echo -e "${B}${C}Managed Tunnels${R}\n"; has_tunnels && print_table || warn "No managed tunnels."
    echo; echo "1) View tunnel details"; echo "2) Check tunnel health"; echo "3) Restart tunnel"; echo "4) Delete tunnel"; echo "5) Live traffic monitor"; echo "0) Back"
    read -r -p "Select option: " c
    case "$c" in 1) details; pause;; 2) check_tunnel; pause;; 3) restart_tunnel; pause;; 4) delete_tunnel; pause;; 5) traffic_monitor; pause;; 0) return;; *) warn "Invalid option."; sleep 1;; esac
  done
}
system_status(){
  clear; echo -e "${B}${C}System Status${R}\n"; systemctl is-active --quiet docker && ok "Docker service is active." || fail "Docker service is inactive."
  echo -e "\nManaged GOST containers:"; docker ps -a --filter label=irangate-gost.managed=true --format 'table {{.Names}}\t{{.Status}}\t{{.Image}}' 2>/dev/null || true
  echo -e "\nConfigured tunnels:"; has_tunnels && print_table || warn "No managed tunnels."
}
install_command(){ local src; src="$(readlink -f "$0")"; [[ "$src" == /usr/local/bin/irangate-gost ]] && { ok "Already installed."; return; }; cp "$src" /usr/local/bin/irangate-gost; chmod +x /usr/local/bin/irangate-gost; ok "Installed command: irangate-gost"; }
self_test(){
  local result
  result="$(normalize_port_csv ' 443 ')" && [[ "$result" == 443 ]] || die "Single-port parser test failed."
  result="$(normalize_port_csv '443, 500,4500')" && [[ "$result" == 443,500,4500 ]] || die "Multi-port parser test failed."
  ! normalize_port_csv '443,443' >/dev/null 2>&1 || die "Duplicate-port rejection test failed."
  ! normalize_port_csv '0,443' >/dev/null 2>&1 || die "Invalid-port rejection test failed."
  [[ "$(csv_count '443,500,4500')" == 3 ]] || die "CSV count test failed."
  ok "Multi-port parser self-test passed."
}
main(){
  while true; do
    clear; echo -e "${B}${C}============================================================"; echo "        NET SPOOF GOST UDP Tunnel Manager v$VERSION"; echo -e "============================================================${R}\n"
    echo "1) Create tunnel"; echo "2) Tunnels"; echo "3) System status"; echo "4) Install manager command"; echo "0) Exit"; echo
    read -r -p "Select option: " c
    case "$c" in 1) create_menu; pause;; 2) tunnels_menu;; 3) system_status; pause;; 4) install_command; pause;; 0) echo "Bye."; exit 0;; *) warn "Invalid option."; sleep 1;; esac
  done
}
if [[ "${1:-}" == "--self-test" ]]; then
  self_test
  exit 0
fi

main
