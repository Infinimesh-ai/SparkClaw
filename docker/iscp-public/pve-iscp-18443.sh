#!/bin/bash
# Decision 0046: public TCP 18443 replaces 443 for TEST1 Relay. Scoped PVE legacy-iptables ingress; preserves all existing routes/rules.
set -euo pipefail
case "${1:-}" in start|stop) ;; *) exit 2 ;; esac
operation=$1
rule() {
  local table=$1 chain=$2
  shift 2
  if [[ $operation == start ]]; then
    /usr/sbin/iptables -w 10 -t "$table" -C "$chain" "$@" 2>/dev/null || /usr/sbin/iptables -w 10 -t "$table" -A "$chain" "$@"
  else
    if /usr/sbin/iptables -w 10 -t "$table" -C "$chain" "$@" 2>/dev/null; then /usr/sbin/iptables -w 10 -t "$table" -D "$chain" "$@"; fi
  fi
}
rule nat PREROUTING -i ens37f0 -d 210.16.177.239/32 -p tcp --dport 18443 -m comment --comment sparkclaw-iscp-18443 -j DNAT --to-destination 10.20.0.20:443
# Observed work2 outbound route is NATed by VM101 to 10.10.10.2 on vmnet0.
rule nat PREROUTING -i vmnet0 -s 10.10.10.2/32 -d 210.16.177.239/32 -p tcp --dport 18443 -m comment --comment sparkclaw-iscp-18443 -j DNAT --to-destination 10.20.0.20:443
# Guest default gateway is VM101; return exactly this mapped flow through PVE.
rule nat POSTROUTING -d 10.20.0.20/32 -o vmnet1 -p tcp --dport 443 -m conntrack --ctorigdst 210.16.177.239 --ctorigdstport 18443 -m comment --comment sparkclaw-iscp-18443 -j SNAT --to-source 10.20.0.254
rule filter FORWARD -i ens37f0 -o vmnet1 -d 10.20.0.20/32 -p tcp --dport 443 -m conntrack --ctstate NEW,ESTABLISHED --ctorigdst 210.16.177.239 --ctorigdstport 18443 -m comment --comment sparkclaw-iscp-18443 -j ACCEPT
rule filter FORWARD -i vmnet1 -o ens37f0 -s 10.20.0.20/32 -p tcp --sport 443 -m conntrack --ctstate ESTABLISHED --ctorigdst 210.16.177.239 --ctorigdstport 18443 -m comment --comment sparkclaw-iscp-18443 -j ACCEPT
rule filter FORWARD -i vmnet0 -o vmnet1 -s 10.10.10.2/32 -d 10.20.0.20/32 -p tcp --dport 443 -m conntrack --ctstate NEW,ESTABLISHED --ctorigdst 210.16.177.239 --ctorigdstport 18443 -m comment --comment sparkclaw-iscp-18443 -j ACCEPT
rule filter FORWARD -i vmnet1 -o vmnet0 -s 10.20.0.20/32 -d 10.10.10.2/32 -p tcp --sport 443 -m conntrack --ctstate ESTABLISHED --ctorigdst 210.16.177.239 --ctorigdstport 18443 -m comment --comment sparkclaw-iscp-18443 -j ACCEPT
