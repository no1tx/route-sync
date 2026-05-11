# route-sync

`route-sync` is a Linux policy-routing controller for traffic steering. It is not a generic `ip route` loader: it owns a documented subset of routes and policy rules, fetches routing sources, computes a deterministic plan, and reconciles Linux kernel state through netlink.

The built-in default behavior is opinionated:

- RU networks are fetched automatically from RIPE Stat.
- RU prefixes can be routed through a dedicated table/gateway.
- Special zones can be loaded from TXT CIDR lists and routed through higher-priority tables.
- Reverse Exit Node mode can send RU prefixes through the local WAN while routing everything else through an upstream tunnel.

## What It Manages

`route-sync` manages:

- Linux routes in configured routing tables.
- Linux policy rules.
- Last-known-good source state.
- Optional managed default routes.
- Optional throw-route exclusions.

`route-sync` does not flush whole routing tables and does not delete unrelated rules.

Routes are owned by the configured route protocol value:

```yaml
global:
  route_protocol: 99
```

Rules are owned by configured table and priority inside the managed priority band:

```yaml
global:
  rule_priority_base: 1000
  rule_priority_step: 10
```

## Requirements

Runtime:

- Linux.
- `CAP_NET_ADMIN` or root.
- A reachable gateway/tunnel for the target route table.

Build:

- Go 1.22 or newer.

Optional for the reverse tunnel example:

- WireGuard.
- `iptables` or `nftables` for NAT on the upstream tunnel endpoint.
- Tailscale if the host is used as a Tailscale Exit Node.

## Build And Install

```sh
make build
sudo install -m 0755 bin/route-sync /usr/local/bin/route-sync
```

Check:

```sh
/usr/local/bin/route-sync version
```

Run tests:

```sh
make test
make vet
```

## Commands

```sh
route-sync check --config /etc/route-sync.yaml
route-sync apply --config /etc/route-sync.yaml
route-sync cleanup --config /etc/route-sync.yaml
route-sync daemon --config /etc/route-sync.yaml
route-sync version
```

Common flags:

```sh
--dry-run
--disable-ru-default
--log-format text|json
--interval 30m
--metrics-listen 127.0.0.1:9108
--cleanup-on-shutdown
```

Command behavior:

- `check`: load config, fetch enabled sources, inspect owned kernel state, print plan only.
- `apply`: one-shot reconciliation.
- `cleanup`: remove only owned routes and owned rules for configured groups.
- `daemon`: periodic reconciliation loop with metrics and SIGHUP reload.
- `version`: print version.

## Source Types

### Built-In RIPE Country Source

The built-in source provider is named `ripe_country`.

```yaml
source:
  type: ripe_country
  country: RU
```

For `RU`, the provider uses:

```text
https://stat.ripe.net/data/country-resource-list/data.json?resource=RU&v4_format=prefix
```

It supports IPv4 and IPv6 when present, normalizes prefixes, deduplicates them, and can aggregate safely.

The RU built-in source is enabled by default:

```yaml
defaults:
  enable_ru_builtin_source: true
```

Disable it with config:

```yaml
defaults:
  enable_ru_builtin_source: false
```

or CLI:

```sh
route-sync daemon --config /etc/route-sync.yaml --disable-ru-default
```

### TXT Source

```yaml
source:
  type: txt
  url: file:///etc/route-sync/telegram.txt
```

TXT sources support:

- `https://`
- `http://`
- `file://`
- plain local filesystem paths

Parsing rules:

- one CIDR per line;
- blank lines are ignored;
- comments starting with `#` or `;` are ignored;
- invalid CIDRs are logged and skipped;
- valid entries continue to be processed;
- duplicates are removed during normalization.

## Config Reference

Minimal shape:

```yaml
global:
  refresh_interval: 1h
  http_timeout: 30s
  state_dir: /var/lib/route-sync
  log_format: text
  metrics_listen: 127.0.0.1:9108
  route_protocol: 99
  rule_priority_base: 1000
  rule_priority_step: 10
  enable_prefix_aggregation: true
  health_check_interval: 5s
  cleanup_on_shutdown: false

defaults:
  enable_ru_builtin_source: true

routing:
  ru_default:
    enabled: true
    source:
      type: ripe_country
      country: RU
    target:
      table: 100
      dev: eth0
      gateways:
        - name: primary-uplink
          via4: 192.0.2.1
        - name: backup-uplink
          via4: 198.51.100.1
      family: ipv4
    rule:
      enabled: true
      priority: 2000
      fwmark: 100
      mask: 255
      table: 100

special_zones: []
```

Target fields:

- `table`: routing table ID.
- `dev`: output interface for source prefixes.
- `via`, `via4`, `via6`: next-hop gateways.
- `gateways`: optional ordered list of failover gateways for source prefixes.
- `health_check.targets`: optional list of ping targets for the gateway. If omitted, IPv4 gateways use `8.8.8.8` and `1.1.1.1`; IPv6 gateways use public IPv6 resolver anycast addresses.
- `health_check.timeout`: per-ping timeout. Defaults to `1s`.
- `onlink`: set Linux onlink route flag.
- `family`: `ipv4`, `ipv6`, or `dual`.
- `default`: optional managed default route in the same table.
- `global.health_check_interval`: daemon-only fast tick for gateway failover re-evaluation. Defaults to `5s`.
- `exclude_local_ips`: skip fetched prefixes that contain this host's non-private local IPs.
- `exclude_prefixes`: explicit `throw` routes in the managed table.

Rule fields:

- `priority`: Linux policy rule priority. Lower number means higher precedence.
- `table`: selected routing table.
- `fwmark` and `mask`: optional mark matcher.
- `from`: optional source CIDR matcher.

When `mask: 0`, route-sync does not install a fwmark matcher. This is useful for source-only rules:

```yaml
rule:
  enabled: true
  priority: 1500
  table: 100
  from: 100.64.0.0/10
  fwmark: 0
  mask: 0
```

## Forward Mode

Forward mode sends fetched prefixes through a dedicated gateway/table.

Example:

```yaml
routing:
  ru_default:
    enabled: true
    source:
      type: ripe_country
      country: RU
    target:
      table: 100
      dev: wg-upstream
      family: ipv4
      gateways:
        - name: primary-upstream
          via4: 10.77.0.1
        - name: backup-upstream
          via4: 10.78.0.1
    rule:
      enabled: true
      priority: 1500
      table: 100
      from: 100.64.0.0/10
      fwmark: 0
      mask: 0
```

Result:

- Traffic from `100.64.0.0/10` enters table `100`.
- RU prefixes in table `100` go through `wg-upstream`.
- Other traffic falls through to later rules, usually Tailscale table/main/default.

## Reverse Exit Node Mode

Reverse mode is useful when a host is a Tailscale Exit Node and should route:

- RU destinations through its ordinary local WAN;
- non-RU destinations through an upstream WireGuard tunnel.

This is done by putting both route types in the same selected table:

- RU fetched prefixes via local WAN.
- Managed table default route via WireGuard.
- Throw exclusions for tailnet/service destinations.

Example:

```yaml
routing:
  ru_default:
    enabled: true
    source:
      type: ripe_country
      country: RU
    target:
      table: 100

      # RU prefixes use local WAN.
      dev: wan0
      family: ipv4
      gateways:
        - name: ru-local-primary
          via4: 203.0.113.1
        - name: ru-local-backup
          via4: 203.0.113.2

      # If a fetched prefix contains this host's public/non-private IP,
      # skip that entire fetched prefix to preserve host reachability.
      exclude_local_ips: true

      # These become throw routes in table 100.
      # They let tailnet/service destinations continue to later rules.
      exclude_prefixes:
        - 100.64.0.0/10
        - 100.100.100.100/32

      # Everything not matched above uses the upstream tunnel.
      default:
        dev: wg-upstream
        gateways:
          - name: foreign-primary
            via4: 10.77.0.2
          - name: foreign-backup
            via4: 10.78.0.2

    rule:
      enabled: true
      priority: 1500
      table: 100
      from: 100.64.0.0/10
      fwmark: 0
      mask: 0
```

Expected table:

```text
throw 100.64.0.0/10 proto 99
throw 100.100.100.100 proto 99
default via 10.77.0.2 dev wg-upstream proto 99
RU_PREFIX via 203.0.113.1 dev wan0 proto 99
```

Expected lookups:

```sh
ip route get 91.142.141.5 from 100.64.0.24 iif tailscale0
ip route get 8.8.8.8 from 100.64.0.24 iif tailscale0
ip route get 100.100.100.100 from 100.64.0.24 iif tailscale0
```

Expected behavior:

- RU destination: local WAN route.
- non-RU destination: WireGuard default route.
- tailnet/service destination: throw route, then later Tailscale/main rules.

## WireGuard Tunnel Setup

This section shows a generic point-to-point WireGuard tunnel suitable for reverse mode.

Assumptions:

- `gateway` is the upstream server with a public UDP endpoint.
- `exit-node` is the machine running route-sync.
- Tunnel interface name: `wg-upstream`.
- Gateway tunnel IP: `10.77.0.2/30`.
- Exit-node tunnel IP: `10.77.0.1/30`.
- WireGuard listen port: `51820/udp`.
- Gateway WAN interface placeholder: `wan0`.

Adjust names and addresses for your environment.

### Install WireGuard

Debian/Ubuntu:

```sh
sudo apt update
sudo apt install wireguard wireguard-tools
```

Arch Linux:

```sh
sudo pacman -S wireguard-tools
```

Fedora/RHEL-like:

```sh
sudo dnf install wireguard-tools
```

### Generate Keys

On each host:

```sh
sudo mkdir -p /etc/wireguard
sudo chmod 700 /etc/wireguard
wg genkey | sudo tee /etc/wireguard/wg-upstream.key | wg pubkey | sudo tee /etc/wireguard/wg-upstream.pub
sudo chmod 600 /etc/wireguard/wg-upstream.key
sudo cat /etc/wireguard/wg-upstream.pub
```

Record both public keys.

### Gateway Config

On the upstream gateway:

```sh
sudo editor /etc/wireguard/wg-upstream.conf
```

```ini
[Interface]
Address = 10.77.0.2/30
ListenPort = 51820
PrivateKey = GATEWAY_PRIVATE_KEY

PostUp = sysctl -w net.ipv4.ip_forward=1
PostUp = iptables -t nat -C POSTROUTING -s 100.64.0.0/10 -o wan0 -j MASQUERADE || iptables -t nat -A POSTROUTING -s 100.64.0.0/10 -o wan0 -j MASQUERADE
PostUp = iptables -t nat -C POSTROUTING -s 10.77.0.0/30 -o wan0 -j MASQUERADE || iptables -t nat -A POSTROUTING -s 10.77.0.0/30 -o wan0 -j MASQUERADE
PostUp = iptables -C FORWARD -i wg-upstream -o wan0 -j ACCEPT || iptables -A FORWARD -i wg-upstream -o wan0 -j ACCEPT
PostUp = iptables -C FORWARD -i wan0 -o wg-upstream -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT || iptables -A FORWARD -i wan0 -o wg-upstream -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT

PostDown = iptables -t nat -D POSTROUTING -s 100.64.0.0/10 -o wan0 -j MASQUERADE || true
PostDown = iptables -t nat -D POSTROUTING -s 10.77.0.0/30 -o wan0 -j MASQUERADE || true
PostDown = iptables -D FORWARD -i wg-upstream -o wan0 -j ACCEPT || true
PostDown = iptables -D FORWARD -i wan0 -o wg-upstream -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT || true

[Peer]
PublicKey = EXIT_NODE_PUBLIC_KEY
AllowedIPs = 10.77.0.1/32, 100.64.0.0/10
```

Replace `wan0` with the real WAN interface:

```sh
ip route get 8.8.8.8
```

Open UDP `51820` on the gateway firewall/security group.

### Exit-Node Config

On the route-sync host:

```sh
sudo editor /etc/wireguard/wg-upstream.conf
```

```ini
[Interface]
Address = 10.77.0.1/30
PrivateKey = EXIT_NODE_PRIVATE_KEY
Table = off
PostUp = ip route add 10.77.0.2/32 dev wg-upstream
PostDown = ip route del 10.77.0.2/32 dev wg-upstream

[Peer]
PublicKey = GATEWAY_PUBLIC_KEY
Endpoint = GATEWAY_PUBLIC_IP:51820
AllowedIPs = 10.77.0.2/32, 0.0.0.0/0
PersistentKeepalive = 25
```

`Table = off` is important: WireGuard must not install the host default route. route-sync will choose what enters the tunnel.

`AllowedIPs = 0.0.0.0/0` is also important for WireGuard cryptokey routing. It permits this peer to carry arbitrary IPv4 destinations, but routes are still controlled by Linux policy routing.

### Start WireGuard

On both hosts:

```sh
sudo systemctl enable --now wg-quick@wg-upstream
sudo wg show wg-upstream
ip addr show wg-upstream
```

From the exit-node:

```sh
ping -c 3 10.77.0.2
```

From the gateway:

```sh
ping -c 3 10.77.0.1
```

If there is no handshake:

```sh
sudo tcpdump -ni any udp port 51820
sudo journalctl -u wg-quick@wg-upstream -n 100 --no-pager
```

### Verify NAT

On the gateway:

```sh
sudo iptables -t nat -S POSTROUTING
sudo iptables -S FORWARD
sudo iptables -t nat -vnL POSTROUTING
sudo iptables -vnL FORWARD
```

During traffic, counters should increase on the MASQUERADE and FORWARD rules.

## Running route-sync With Reverse Mode

Install config:

```sh
sudo cp examples/configs/reverse-ru-local-rest-tunnel.yaml /etc/route-sync.yaml
sudo editor /etc/route-sync.yaml
```

Update:

- `target.dev`: local WAN interface.
- `target.via4` or `target.gateways`: local WAN gateway or failover gateways.
- `target.default.dev`: WireGuard interface.
- `target.default.via4` or `target.default.gateways`: upstream tunnel IP or failover gateways.
- `rule.from`: source CIDR for traffic that should use the split table.

Check:

```sh
sudo /usr/local/bin/route-sync check --config /etc/route-sync.yaml
```

Dry-run apply:

```sh
sudo /usr/local/bin/route-sync apply --config /etc/route-sync.yaml --dry-run
```

Apply:

```sh
sudo /usr/local/bin/route-sync apply --config /etc/route-sync.yaml
```

Inspect:

```sh
ip rule show
ip route show table 100 | head
ip route show table 100 | grep -E 'throw|default'
```

Lookup tests:

```sh
ip route get 91.142.141.5 from 100.64.0.24 iif tailscale0
ip route get 8.8.8.8 from 100.64.0.24 iif tailscale0
ip route get 100.100.100.100 from 100.64.0.24 iif tailscale0
```

Use real source/destination addresses for your environment.

## systemd

Install binary and config:

```sh
sudo install -m 0755 bin/route-sync /usr/local/bin/route-sync
sudo cp examples/configs/reverse-ru-local-rest-tunnel.yaml /etc/route-sync.yaml
sudo editor /etc/route-sync.yaml
```

Unit:

```ini
[Unit]
Description=route-sync policy routing controller
After=network-online.target wg-quick@wg-upstream.service tailscaled.service
Wants=network-online.target
Requires=wg-quick@wg-upstream.service

[Service]
Type=simple
ExecStart=/usr/local/bin/route-sync daemon --config /etc/route-sync.yaml
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
RestartSec=5s

AmbientCapabilities=CAP_NET_ADMIN
CapabilityBoundingSet=CAP_NET_ADMIN
NoNewPrivileges=true

StateDirectory=route-sync
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/route-sync
PrivateTmp=true
PrivateDevices=false
RestrictAddressFamilies=AF_INET AF_INET6 AF_NETLINK AF_UNIX
SystemCallFilter=@system-service @network-io
LockPersonality=true
MemoryDenyWriteExecute=true

[Install]
WantedBy=multi-user.target
```

Install:

```sh
sudo editor /etc/systemd/system/route-sync.service
sudo systemctl daemon-reload
sudo systemctl enable --now route-sync
```

Logs:

```sh
journalctl -u route-sync -f
```

Reload config:

```sh
sudo systemctl reload route-sync
```

Stop:

```sh
sudo systemctl stop route-sync
```

## Cleanup Mode

Manual cleanup:

```sh
sudo route-sync cleanup --config /etc/route-sync.yaml
```

Preview:

```sh
sudo route-sync cleanup --config /etc/route-sync.yaml --dry-run
```

Daemon shutdown cleanup:

```yaml
global:
  cleanup_on_shutdown: true
```

or:

```sh
route-sync daemon --config /etc/route-sync.yaml --cleanup-on-shutdown
```

Cleanup removes only daemon-owned routes and rules. It does not flush unmanaged routes.

## Metrics

Default endpoint:

```sh
curl http://127.0.0.1:9108/metrics
```

Example metrics:

```text
route_sync_successful_reconcile_total
route_sync_failed_reconcile_total
route_sync_successful_cleanup_total
route_sync_failed_cleanup_total
route_sync_source_fetch_success_total
route_sync_source_fetch_failure_total
route_sync_managed_prefixes{group="ru_default"}
route_sync_managed_routes{group="ru_default"}
route_sync_managed_rules{group="ru_default"}
route_sync_last_success_timestamp
```

## Hot Reload

Send SIGHUP:

```sh
sudo systemctl reload route-sync
```

or:

```sh
sudo systemctl kill -s HUP route-sync
```

If the new config is invalid, the daemon keeps the last valid config.

## Examples And Fixtures

Example configs:

- `examples/configs/minimal-ru-default.yaml`
- `examples/configs/ru-plus-telegram.yaml`
- `examples/configs/ru-plus-multiple-zones.yaml`
- `examples/configs/reverse-ru-local-rest-tunnel.yaml`
- `examples/configs/txt-only.yaml`
- `examples/configs/local-dev.yaml`

TXT fixtures:

- `examples/fixtures/txt/telegram.txt`
- `examples/fixtures/txt/youtube_eu.txt`
- `examples/fixtures/txt/corporate_vpn.txt`
- `examples/fixtures/txt/mixed_dualstack.txt`

RIPE fixtures:

- `examples/fixtures/ripe/ru-normal.json`
- `examples/fixtures/ripe/ru-empty.json`
- `examples/fixtures/ripe/ru-duplicates.json`
- `examples/fixtures/ripe/ru-malformed.json`

Test fixtures live under `testdata/`.

Local development:

```sh
route-sync check --config examples/configs/local-dev.yaml
route-sync apply --config examples/configs/local-dev.yaml --dry-run
examples/run-check.sh
examples/run-dry.sh
```

## Troubleshooting

Show rules and managed table:

```sh
ip rule show
ip route show table 100
```

Check route lookups:

```sh
ip route get DESTINATION from SOURCE iif tailscale0
```

Check Tailscale routes:

```sh
ip route show table 52
tailscale status
```

Check WireGuard:

```sh
sudo wg show
ip addr show wg-upstream
```

Check NAT counters:

```sh
sudo iptables -t nat -vnL POSTROUTING
sudo iptables -vnL FORWARD
```

Common issues:

- No WireGuard handshake: check keys, endpoint, UDP firewall, and `AllowedIPs`.
- Tunnel works but no internet: check gateway forwarding and MASQUERADE.
- Tailnet addresses break in reverse mode: ensure `exclude_prefixes` includes `100.64.0.0/10` and `100.100.100.100/32`.
- Host public address becomes unreachable: enable `exclude_local_ips`.
- Route lookup chooses main/default: check source rule priority and `rule.from`.

## Caveats

- `route-sync` uses netlink for route and rule operations.
- Linux policy rules do not have route-protocol ownership, so rule ownership is table/priority-band based.
- `exclude_local_ips` skips whole fetched prefixes that contain local public/non-private addresses.
- Reverse mode requires careful tunnel NAT and explicit tailnet exclusions.
- For IPv6 reverse mode, configure `via6`, IPv6 forwarding, IPv6 tunnel routes, and NAT/routing according to your network design.
