# l2mesh

WireGuard + VXLAN + EVPN を組み合わせた L2 オーバーレイ VPN のエージェント CLI。tinc switch mode の代替として、L2 セグメントを暗号化付き・カーネル空間データプレーンで延伸することを狙う。

> **状態: alpha** — Root↔Root 構成の WireGuard ピア管理・VXLAN+bridge データプレーン・FRR/BGP EVPN コントロールプレーンが動作する。Leaf も BGP/EVPN speaker として参加する設計 (BFD で sub-second 検出)、ただし Leaf-to-Leaf transit の data plane (WG AllowedIPs catch-all + Root の ip_forward) は未実装 ([#14](https://github.com/chun37/l2mesh/issues/14))。

## アーキテクチャ

```
[端末: 10.x.x.x]
       │
   ┌───┴───┐
   │  br0  │  Linux bridge
   └───┬───┘
       │
   ┌───┴────┐
   │ vxlan0 │  VXLAN VNI 100, 全ノード nolearning (EVPN が FDB を埋める)
   └───┬────┘
       │
   ┌───┴────┐
   │  wg0   │  WireGuard (100.64.0.0/24 オーバーレイ)
   └───┬────┘
       │
   [物理 NIC]
```

- **全ノード**で FRR (bgpd + bfdd) を動かし、BGP/EVPN で MAC・ARP・BUM を分散
- **Root**: 公開エンドポイント (v4/v6) を持つ。他 Root と full-mesh iBGP。配下 Leaf に対しては Route Reflector
- **Leaf**: NAT 配下可。通常は 2-3 個の Root と iBGP peering、Roots を RR として使う
- **BFD** (rx/tx 300ms × 3) で sub-second の peer 死活検出 → BGP route の即時撤回 → 自動 failover
- Root 同士・Root↔Leaf 全て同一 AS (default 65000)、オーバーレイ IP を BGP ピアアドレスに使用

詳細: [`docs/design.md`](docs/design.md)

## セットアップ

ディストリ別:
- **Debian / Ubuntu**: [`docs/debian-setup.md`](docs/debian-setup.md)
- **NixOS**: [`docs/nixos-setup.md`](docs/nixos-setup.md)

ビルド要件: Linux x86_64 + Go 1.21 以降。

```bash
git clone https://github.com/chun37/l2mesh
cd l2mesh
go build -trimpath -ldflags="-s -w" -o l2mesh .
sudo install -m 0755 l2mesh /usr/local/bin/l2mesh
sudo mkdir -p /var/lib/l2mesh
```

WireGuard インターフェース・FRR 本体は外で先に用意しておく必要がある (`wg-quick` / `services.frr` 等)。本ツールはピア・VXLAN/bridge・FRR config を runtime で管理する。

## クイックスタート

```bash
# 1. 自ノードの identity を state.json に書く (対話 or フラグ)
sudo l2mesh init \
  --name my-node \
  --role root \
  --overlay-ip 100.64.0.1 \
  --endpoint '[2001:db8::1]:51820'

# 2. 他 Root を追加
sudo l2mesh root add \
  --name root-b \
  --pubkey '<相手の WG 公開鍵>' \
  --endpoint '[2001:db8::2]:51820' \
  --ip 100.64.0.2

# 3. VXLAN/bridge を up + BUM 同期 + FRR reload (sync は全部やる)
sudo l2mesh sync

# 4. 状態確認
sudo l2mesh status
sudo vtysh -c "show bgp l2vpn evpn summary"   # BGP セッション
```

`init` はフラグ未指定の項目を TTY なら対話入力する。`bridge_addrs` などの L2 詳細はデフォルトで書かれるので、必要なら `/var/lib/l2mesh/state.json` を直接編集する。

## コマンド

| コマンド | 動作 |
|----------|------|
| `l2mesh init [--name N --role root\|leaf --overlay-ip I --endpoint E] [--force]` | state.json を初期化（既存があれば error、`--force` で上書き）。TTY なら省略フラグを対話入力 |
| `l2mesh status` | ノード/ピア/L2/FRR EVPN の状態をまとめて表示（後述の出力例参照）|
| `l2mesh up` | VXLAN + bridge を作成（idempotent）、bridge IP・BUM(FDB) を state に合わせて同期。Root では `nolearning`、Leaf では learning |
| `l2mesh down` | VXLAN + bridge を削除 |
| `l2mesh root add --name N --pubkey K --endpoint E --ip I` | Root を追加（WG ピア + BUM 自動追加 + FRR BGP neighbor 追加）|
| `l2mesh root remove --name N` | Root を削除（WG ピア + BUM + FRR neighbor 削除）|
| `l2mesh peer add --name N --pubkey K --ip I` | Leaf を追加（endpoint なし、NAT 配下想定）|
| `l2mesh peer remove --name N` | Leaf を削除 |
| `l2mesh peer list` | 全ピア一覧 |
| `l2mesh mac list` | EVPN MAC テーブル (local / remote)。各 MAC に紐づく IP・VTEP・peer 名を表示 |
| `l2mesh promote [--endpoint E]` | Leaf → Root に昇格。FRR config 反映 + VXLAN を nolearning に。`endpoint` が空ならエラー |
| `l2mesh demote` | Root → Leaf に降格。FRR の BGP config をクリア + VXLAN を learning に。配下 leaf が残っていれば拒否 |
| `l2mesh agent` | 常駐デーモン (必須)。peer に gossip して overlay graph を学習、MST を計算 (現状は informational、FRR が BUM FDB を所有)。`/info` `/topology` を overlay 上に serve |
| `l2mesh sync` | state.json から kernel/FRR に全部反映: WG `ReplacePeers` + L2 up + FDB 同期 + FRR reload。boot 時 systemd 用 |
| `l2mesh frr show` | state.json から生成される FRR 設定を stdout に表示（書き込みも reload もしない、dry-run 用）|

`--state PATH` でファイルパス指定可（既定: `/var/lib/l2mesh/state.json`）。

### `l2mesh status` 出力例

```
Node:      aibauiha (role=root)
Overlay:   100.64.0.1
Endpoint:  [2001:db8::1]:51820
Interface: wg-l2mesh (listen 51820)

Configured peers: 1 (state) / 1 (kernel)

KIND  NAME    OVERLAY     ENDPOINT             HANDSHAKE  WG     BGP
root  anemos  100.64.0.2  anemos.example:51820  1m42s ago  alive  Established (rcv=3 snt=3)

L2:
  vxlan-l2mesh on br-l2mesh (vni=100, dstport=4789, mtu=1370)
  Bridge addrs: 172.16.1.1/24

FRR / EVPN:
  BGP router-id: 100.64.0.1 (AS 65000)
  VNI 100 (L2): 2 MACs, 4 ARPs, 1 remote VTEPs, advertise-svi-ip=Yes
```

FRR が未インストール / 未起動の場合、`FRR / EVPN:` セクションは "not available" と表示され、BGP 列は `-` になる（Leaf や pre-FRR セットアップでも壊れない）。

## state.json スキーマ

```json
{
  "node": {
    "name": "my-node",
    "role": "root",
    "overlay_ip": "100.64.0.1",
    "endpoint": "[2001:db8::1]:51820",
    "asn": 65000,
    "listen_port": 51820,
    "interface": "wg-l2mesh"
  },
  "l2": {
    "vxlan_iface": "vxlan-l2mesh",
    "bridge_iface": "br-l2mesh",
    "vni": 100,
    "port": 4789,
    "mtu": 1370,
    "local_ports": [],
    "bridge_addrs": ["172.16.1.1/24"]
  },
  "roots": [
    { "name": "root-b", "pubkey": "...", "overlay_ip": "100.64.0.2", "endpoint": "[2001:db8::2]:51820" }
  ],
  "leafs": [
    { "name": "leaf-1", "pubkey": "...", "overlay_ip": "100.64.0.10" }
  ]
}
```

| フィールド | 意味 |
|----------|------|
| `node.asn` | BGP AS 番号。全 Root で同一にする (iBGP) |
| `node.overlay_ip` | このノードの overlay IP。BGP router-id / VXLAN local としても使われる |
| `l2.local_ports` | bridge に attach する物理/VLAN iface 名 |
| `l2.bridge_addrs` | bridge に付ける IP (CIDR)。global scope のみ管理、link-local は kernel 任せ |

### BUM FDB (現状: FRR 管理)

VXLAN の broadcast/unknown-unicast/multicast (BUM) 配送先 FDB (`00:00:00:00:00:00` エントリ) は **FRR の zebra が EVPN Type-3 route から自動管理** している。l2mesh は直接 FDB を触らない。

### `l2mesh agent` の役割 (現状)

各ノードで `l2mesh agent` を常駐させると:

- TCP `4444` (overlay) で `/info` `/topology` を serve (WG が認証境界)
- 5 秒間隔で peer の `/info` を poll、topology を集約
- Kruskal で MST 計算 (重み 1、tiebreak で全ノード同じ tree に収束)
- 結果は `/topology` 経由で観測可能 (障害監視やデバッグ用)

systemd unit 例:

```ini
# /etc/systemd/system/l2mesh-agent.service
[Unit]
Description=L2 Mesh agent (gossip + topology + MST)
After=l2mesh-sync.service
Requires=l2mesh-sync.service
[Service]
ExecStart=/usr/local/bin/l2mesh agent
Restart=on-failure
[Install]
WantedBy=multi-user.target
```

### 3+ Root mesh で BUM ループする話 (Phase 2b: 部分実装)

EVPN ingress replication + WG underlay (unicast only) で 3 Roots full-mesh だと BUM がループする。Linux kernel の source-VTEP split horizon は 1 hop 分しか効かないため。

agent が計算する MST に基づき、**直接 peer な非 MST neighbor の Type-3 を route-map で block** する実装まで入ってる:

- agent が MST 変化を検知 → `frr.Apply` で per-neighbor `route-map BLOCK_T3 in` を更新
- 非 MST 直接 peer からの Type-3 はその場で reject される
- 結果: 余分な direct Type-3 が消えて重複が減る

ただし完全ではない:
- RR が reflect した Type-3 (originator は非 MST、NH は MST neighbor via next-hop-self force) は通ってしまう
- FRR は EVPN Type-3 の prefix 内に埋め込まれた originator VTEP を `match ip address prefix-list` で参照できないため、reflect 経路に対する filter は今の FRR では作れない
- さらに reflect された Type-3 が installl される時、FDB の dst は **prefix 内の originator IP** (BGP NH ではない)。WG underlay でその originator に直接到達できないと BUM が落ちる

実用上の挙動:
- **2 Root + 任意 Leaf**: ループ起きない (3 ノード以下では構造的に不可)
- **3+ Root full-mesh**: BLOCK_T3 で direct duplicate は減らせるが、完全な loop-free 保証には至らない。WG AllowedIPs catch-all + Root の `ip_forward` で transit を組む別の作業が必要

3+ Root を実投入するときは中央 Root を star のハブにする運用が安全。

書き込みは `state.json.lock` への `flock(LOCK_EX)` で直列化される（並列の `l2mesh add` でも安全）。書き換えは temp + rename で atomic。

## 制約

- Linux x86_64 (kernel WireGuard, flock, netlink)
- root 権限が必要 (CAP_NET_ADMIN + FRR の vtysh)
- **Leaf-to-Leaf transit の data plane は未実装** ([#14](https://github.com/chun37/l2mesh/issues/14)): 現在 `peerConfig` は AllowedIPs を `/32` 固定で生成しており、Leaf として Primary Root に catch-all (`100.64.0.0/24`) を振り、Root に `ip_forward=1` を入れる仕組みがない。Plan B の BGP/BFD コントロールプレーンは完成しているが、実トラフィックの Leaf→Root→Leaf の中継は別途必要
- peer migrate ([#4](https://github.com/chun37/l2mesh/issues/4)) 未実装
- テスト未整備

## ライセンス

MIT
