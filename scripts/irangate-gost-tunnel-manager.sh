#!/usr/bin/env bash
set -Euo pipefail

VERSION="1.0.0"
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

valid_port(){ [[ "${1:-}" =~ ^[0-9]+$ ]] && (( $1>=1 && $1<=65535 )); }
valid_ttl(){ [[ "${1:-}" =~ ^[0-9]+$ ]] && (( $1>=10 && $1<=86400 )); }
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
prompt_text(){ local var=$1 prompt=$2 value=; while [[ -z "$value" ]]; do read -r -p "$prompt: " value; done; printf -v "$var" %s "$value"; }
prompt_port(){
  local var=$1 prompt=$2 def="${3:-}" value
  while true; do
    read -r -p "$prompt${def:+ [$def]}: " value; value="${value:-$def}"
    valid_port "$value" && { printf -v "$var" %s "$value"; return; }
    warn "Enter a valid port (1-65535)."
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
    valid_ttl "$value" && { printf -v "$var" %s "$value"; return; }
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
  unset ID ROLE NAME CONTAINER CREATED_AT TTL LISTEN_PORT FOREIGN_HOST FOREIGN_GOST_PORT TARGET_HOST TARGET_PORT ALLOWED_IRAN_IP
  # shellcheck disable=SC1090
  source "$1"
}
container_running(){ [[ "$(docker inspect -f '{{.State.Running}}' "$1" 2>/dev/null || true)" == true ]]; }
container_exists(){ docker inspect "$1" >/dev/null 2>&1; }
udp_listening(){ ss -H -lun "sport = :$1" 2>/dev/null | grep -q .; }
managed_conflict(){
  local role=$1 port=$2 f
  while IFS= read -r f; do
    [[ -n "$f" ]] || continue; load_conf "$f"
    if [[ "$ROLE" == "$role" && "$LISTEN_PORT" == "$port" ]]; then echo "$NAME ($ID)"; return 0; fi
  done < <(all_confs)
  return 1
}

ipt_add(){ if [[ $1 == filter ]]; then shift; iptables -C "$@" >/dev/null 2>&1 || iptables -I "$@"; else local t=$1; shift; iptables -t "$t" -C "$@" >/dev/null 2>&1 || iptables -t "$t" -I "$@"; fi; }
ipt_del(){
  if [[ $1 == filter ]]; then shift; while iptables -C "$@" >/dev/null 2>&1; do iptables -D "$@" || break; done
  else local t=$1; shift; while iptables -t "$t" -C "$@" >/dev/null 2>&1; do iptables -t "$t" -D "$@" || break; done; fi
}
add_iran_rules(){
  local lp=$2 host=$3 fp=$4 tag="irangate-gost:$1"
  ipt_add filter INPUT -p udp --dport "$lp" -m comment --comment "$tag:input" -j ACCEPT
  valid_ipv4 "$host" && ipt_add filter OUTPUT -p udp -d "$host" --dport "$fp" -m comment --comment "$tag:output" -j ACCEPT || true
}
del_iran_rules(){
  local lp=$2 host=$3 fp=$4 tag="irangate-gost:$1"
  ipt_del filter INPUT -p udp --dport "$lp" -m comment --comment "$tag:input" -j ACCEPT
  valid_ipv4 "$host" && ipt_del filter OUTPUT -p udp -d "$host" --dport "$fp" -m comment --comment "$tag:output" -j ACCEPT || true
}
add_foreign_rules(){
  local lp=$2 allowed=$3 tag="irangate-gost:$1"
  if [[ -n "$allowed" ]]; then ipt_add filter INPUT -p udp -s "$allowed" --dport "$lp" -m comment --comment "$tag:input" -j ACCEPT
  else ipt_add filter INPUT -p udp --dport "$lp" -m comment --comment "$tag:input" -j ACCEPT; fi
}
del_foreign_rules(){
  local lp=$2 allowed=$3 tag="irangate-gost:$1"
  if [[ -n "$allowed" ]]; then ipt_del filter INPUT -p udp -s "$allowed" --dport "$lp" -m comment --comment "$tag:input" -j ACCEPT
  else ipt_del filter INPUT -p udp --dport "$lp" -m comment --comment "$tag:input" -j ACCEPT; fi
}

maybe_fix_openvpn_low_port(){
  local port=$1
  ((port<1024)) || return 0
  systemctl list-unit-files openvpn@server.service >/dev/null 2>&1 || { warn "openvpn@server not detected; capability fix skipped."; return; }
  confirm "OpenVPN target is privileged port $port. Apply CAP_NET_BIND_SERVICE fix?" || return
  mkdir -p /etc/systemd/system/openvpn@server.service.d
  cat >/etc/systemd/system/openvpn@server.service.d/cap-net-bind.conf <<'CAPEOF'
[Service]
CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_BIND_SERVICE CAP_NET_RAW CAP_SETGID CAP_SETUID CAP_DAC_OVERRIDE
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_BIND_SERVICE CAP_NET_RAW
CAPEOF
  systemctl daemon-reload
  systemctl reset-failed openvpn@server >/dev/null 2>&1 || true
  systemctl restart openvpn@server
  systemctl is-active --quiet openvpn@server && ok "openvpn@server is active." || warn "OpenVPN restart failed; inspect journalctl."
}

create_iran(){
  local name id container lp host fp ttl conflict
  echo -e "\n${B}Create Iran-side Tunnel${R}"
  echo "USER_PORT/UDP -> FOREIGN_HOST:GOST_PORT/UDP"
  prompt_text name "Tunnel name"
  id="$(slugify "$name")"; [[ -e "$(conf_path "$id")" ]] && id="$id-$(date +%s)"
  prompt_port lp "User-facing UDP port on Iran" 443
  prompt_host host "Foreign server IP or hostname"
  prompt_port fp "GOST UDP port on foreign server" 8080
  prompt_ttl ttl
  if conflict="$(managed_conflict iran "$lp" 2>/dev/null)"; then die "Port $lp is already used by: $conflict"; fi
  if udp_listening "$lp"; then warn "UDP $lp already has a listener."; confirm "Continue anyway?" || return; fi
  ensure_deps; pull_image
  container="irangate-gost-iran-$id"
  docker rm -f "$container" >/dev/null 2>&1 || true
  docker run -d --name "$container" --restart always --network host \
    --label irangate-gost.managed=true --label irangate-gost.role=iran --label "irangate-gost.id=$id" \
    "$GOST_IMAGE" "-L=udp://:$lp/$host:$fp?ttl=$ttl" >/dev/null
  add_iran_rules "$id" "$lp" "$host" "$fp"
  save_conf "$(conf_path "$id")" "ID=$id" "ROLE=iran" "NAME=$name" "CONTAINER=$container" "CREATED_AT=$(date -Is)" "TTL=$ttl" "LISTEN_PORT=$lp" "FOREIGN_HOST=$host" "FOREIGN_GOST_PORT=$fp"
  sleep 1
  if container_running "$container"; then ok "Iran tunnel created."; echo "Listen : 0.0.0.0:$lp/udp"; echo "Target : $host:$fp/udp"; else fail "Container is not running."; docker logs --tail 50 "$container" 2>&1 || true; fi
}

create_foreign(){
  local name id container lp target tp ttl allowed conflict
  echo -e "\n${B}Create Foreign-side Tunnel${R}"
  echo "GOST_PORT/UDP -> LOCAL_OPENVPN_HOST:OPENVPN_PORT/UDP"
  prompt_text name "Tunnel name"
  id="$(slugify "$name")"; [[ -e "$(conf_path "$id")" ]] && id="$id-$(date +%s)"
  prompt_port lp "GOST UDP listen port on foreign server" 8080
  prompt_host target "Local OpenVPN host" 127.0.0.1
  prompt_port tp "Local OpenVPN UDP port" 443
  prompt_ttl ttl
  read -r -p "Allowed Iran IPv4 (blank = allow all): " allowed
  [[ -z "$allowed" ]] || valid_ipv4 "$allowed" || die "Invalid IPv4."
  if conflict="$(managed_conflict foreign "$lp" 2>/dev/null)"; then die "Port $lp is already used by: $conflict"; fi
  if udp_listening "$lp"; then warn "UDP $lp already has a listener."; confirm "Continue anyway?" || return; fi
  ensure_deps; pull_image
  maybe_fix_openvpn_low_port "$tp"
  container="irangate-gost-foreign-$id"
  docker rm -f "$container" >/dev/null 2>&1 || true
  docker run -d --name "$container" --restart always --network host \
    --label irangate-gost.managed=true --label irangate-gost.role=foreign --label "irangate-gost.id=$id" \
    "$GOST_IMAGE" "-L=udp://:$lp/$target:$tp?ttl=$ttl" >/dev/null
  add_foreign_rules "$id" "$lp" "$allowed"
  save_conf "$(conf_path "$id")" "ID=$id" "ROLE=foreign" "NAME=$name" "CONTAINER=$container" "CREATED_AT=$(date -Is)" "TTL=$ttl" "LISTEN_PORT=$lp" "TARGET_HOST=$target" "TARGET_PORT=$tp" "ALLOWED_IRAN_IP=$allowed"
  sleep 1
  if container_running "$container"; then ok "Foreign tunnel created."; echo "Listen : 0.0.0.0:$lp/udp"; echo "Target : $target:$tp/udp"; else fail "Container is not running."; docker logs --tail 50 "$container" 2>&1 || true; fi
}

create_menu(){
  clear; echo -e "${B}Create Tunnel${R}\n"; echo "1) Iran server"; echo "2) Foreign server"; echo "0) Back"; read -r -p "Select role: " c
  case "$c" in 1) create_iran;; 2) create_foreign;; 0) return;; *) warn "Invalid option.";; esac
}
status_word(){ if container_running "$1"; then echo RUNNING; elif container_exists "$1"; then echo STOPPED; else echo MISSING; fi; }
print_table(){
  local i=0 f status listen target
  printf '%-4s %-24s %-9s %-9s %-20s %-30s\n' "#" "NAME" "ROLE" "STATUS" "LISTEN" "TARGET"
  printf '%-4s %-24s %-9s %-9s %-20s %-30s\n' ---- ------------------------ --------- --------- -------------------- ------------------------------
  while IFS= read -r f; do
    [[ -n "$f" ]] || continue; load_conf "$f"; ((i+=1)); status="$(status_word "$CONTAINER")"; listen=":$LISTEN_PORT/udp"
    [[ "$ROLE" == iran ]] && target="$FOREIGN_HOST:$FOREIGN_GOST_PORT/udp" || target="$TARGET_HOST:$TARGET_PORT/udp"
    printf '%-4s %-24s %-9s %-9s %-20s %-30s\n' "$i" "${NAME:0:24}" "$ROLE" "$status" "$listen" "${target:0:30}"
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
  echo "ID            : $ID"; echo "Name          : $NAME"; echo "Role          : $ROLE"; echo "Created       : $CREATED_AT"; echo "Container     : $CONTAINER"; echo "Status        : $st"; echo "Started       : $started"; echo "Restart Count : $restarts"; echo "TTL           : $TTL"; echo "Listen        : 0.0.0.0:$LISTEN_PORT/udp"
  if [[ "$ROLE" == iran ]]; then echo "Foreign       : $FOREIGN_HOST:$FOREIGN_GOST_PORT/udp"; echo "Path          : USER -> Iran:$LISTEN_PORT -> $FOREIGN_HOST:$FOREIGN_GOST_PORT"; else echo "Local Target  : $TARGET_HOST:$TARGET_PORT/udp"; echo "Allowed Iran  : ${ALLOWED_IRAN_IP:-ALL}"; echo "Path          : Iran -> Foreign:$LISTEN_PORT -> $TARGET_HOST:$TARGET_PORT"; fi
  echo; echo "GOST command:"; docker inspect -f '{{json .Config.Cmd}}' "$CONTAINER" 2>/dev/null || echo "Container missing."
}
details(){ select_tunnel && details_file "$SELECTED_FILE"; }
counters(){ echo -e "\nFirewall packet counters:"; iptables -nvL INPUT --line-numbers 2>/dev/null | grep "irangate-gost:$1" || echo "No matching INPUT rule."; iptables -nvL OUTPUT --line-numbers 2>/dev/null | grep "irangate-gost:$1" || true; }

check_file(){
  load_conf "$1"; local bad=0
  echo -e "\n${B}${C}Tunnel Health Check${R}"; echo "Name: $NAME"; echo "Role: $ROLE"; echo "------------------------------------------------------------"
  if container_running "$CONTAINER"; then ok "Container is running."; else fail "Container is not running."; bad=1; fi
  if udp_listening "$LISTEN_PORT"; then ok "UDP listener detected on $LISTEN_PORT."; ss -lunp 2>/dev/null | grep -E ":$LISTEN_PORT[[:space:]]" | head -n 5 || true; else fail "No UDP listener on $LISTEN_PORT."; bad=1; fi
  if [[ "$ROLE" == iran ]]; then
    if getent ahostsv4 "$FOREIGN_HOST" >/dev/null 2>&1 || valid_ipv4 "$FOREIGN_HOST"; then ok "Foreign host resolves: $FOREIGN_HOST"; else fail "Foreign host cannot resolve."; bad=1; fi
    if ip route get "$FOREIGN_HOST" >/dev/null 2>&1; then ok "Route to foreign host exists."; ip route get "$FOREIGN_HOST" | head -n1; else warn "Route check failed."; fi
    if ping -c 2 -W 2 "$FOREIGN_HOST" >/dev/null 2>&1; then ok "Foreign host answers ICMP."; else warn "No ICMP reply; UDP may still work."; fi
  else
    if ss -H -lun "sport = :$TARGET_PORT" 2>/dev/null | grep -q .; then ok "UDP service detected on local target port $TARGET_PORT."; else warn "No wildcard UDP listener detected on target $TARGET_PORT."; fi
    if systemctl list-unit-files openvpn@server.service >/dev/null 2>&1; then systemctl is-active --quiet openvpn@server && ok "openvpn@server is active." || warn "openvpn@server is not active."; fi
  fi
  counters "$ID"; echo -e "\nRecent GOST logs:"; docker logs --tail 30 "$CONTAINER" 2>&1 || true; echo
  ((bad==0)) && { ok "Local health checks passed."; warn "End-to-end UDP validation still requires real client traffic."; } || fail "One or more checks failed."
}
check_tunnel(){ select_tunnel && check_file "$SELECTED_FILE"; }
restart_tunnel(){ select_tunnel || return; load_conf "$SELECTED_FILE"; docker restart "$CONTAINER" >/dev/null; sleep 1; container_running "$CONTAINER" && ok "Tunnel restarted." || { fail "Restart failed."; docker logs --tail 50 "$CONTAINER" 2>&1 || true; }; }
delete_tunnel(){
  select_tunnel || return; local f="$SELECTED_FILE"; load_conf "$f"; details_file "$f"; echo; confirm "Delete this tunnel permanently?" || { info "Cancelled."; return; }
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  if [[ "$ROLE" == iran ]]; then del_iran_rules "$ID" "$LISTEN_PORT" "$FOREIGN_HOST" "$FOREIGN_GOST_PORT"; else del_foreign_rules "$ID" "$LISTEN_PORT" "${ALLOWED_IRAN_IP:-}"; fi
  rm -f "$f"; ok "Tunnel deleted: $NAME"
}
traffic_monitor(){
  select_tunnel || return; load_conf "$SELECTED_FILE"; echo "Press Ctrl+C to stop."
  if [[ "$ROLE" == iran ]]; then tcpdump -ni any "udp port $LISTEN_PORT or udp port $FOREIGN_GOST_PORT"; else tcpdump -ni any "udp port $LISTEN_PORT or udp port $TARGET_PORT"; fi
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
main(){
  while true; do
    clear; echo -e "${B}${C}============================================================"; echo "        IranGate GOST UDP Tunnel Manager v$VERSION"; echo -e "============================================================${R}\n"
    echo "1) Create tunnel"; echo "2) Tunnels"; echo "3) System status"; echo "4) Install manager command"; echo "0) Exit"; echo
    read -r -p "Select option: " c
    case "$c" in 1) create_menu; pause;; 2) tunnels_menu;; 3) system_status; pause;; 4) install_command; pause;; 0) echo "Bye."; exit 0;; *) warn "Invalid option."; sleep 1;; esac
  done
}
main
