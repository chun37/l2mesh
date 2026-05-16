# l2mesh

WireGuard + VXLAN + EVPN を組み合わせた L2 オーバーレイ VPN のエージェント CLI。tinc switch mode の代替として、L2 セグメントを暗号化付き・カーネル空間データプレーンで延伸することを狙う。

> **状態: MVP (alpha)** — WireGuard ピア管理と VXLAN+bridge データプレーンが動作する。FRR/EVPN / 昇格降格・死活監視は未実装（[Issues](https://github.com/chun37/l2mesh/issues) 参照）。EVPN なしの暫定状態のため、現状は **learning モード** で動かしている (2ノード間テスト用、3+ Root では loop の可能性あり)。

## アーキテクチャ

```
[端末: 10.x.x.x]
       │
   ┌───┴───┐
   │  br0  │  Linux bridge
   └───┬───┘
       │
   ┌───┴────┐
   │ vxlan0 │  VXLAN VNI 100 (Root: nolearning / Leaf: learning)
   └───┬────┘
       │
   ┌───┴────┐
   │  wg0   │  WireGuard (100.64.0.0/24 オーバーレイ)
   └───┬────┘
       │
   [物理 NIC]
```

- **Root**: 公開 IP を持ち、ピアを中継する。FRR で BGP EVPN を運用（予定）
- **Leaf**: NAT 配下可。Root を経由して L2 セグメントに参加
- ノード間の MAC 配布とループ防止は EVPN (BGP Type-2/Type-3 ルート) で行う

詳細: [`docs/design.md`](docs/design.md)

## インストール

Linux x86_64 専用。

```bash
go build -trimpath -ldflags="-s -w" -o l2mesh .
sudo install -m 0755 l2mesh /usr/local/bin/l2mesh
sudo mkdir -p /var/lib/l2mesh
```

WireGuard インターフェース自体は外で先に作っておく必要がある（NixOS なら `networking.wireguard.interfaces.<name>`、それ以外なら `wg-quick` 等）。本ツールはピアのみを管理する。

## クイックスタート

```bash
# 1. 自ノード情報を state.json に書く (or デフォルトで起動して手で編集)
sudo l2mesh status   # /var/lib/l2mesh/state.json が無ければプレースホルダ生成

# 2. state.json の node セクションを編集
sudo vi /var/lib/l2mesh/state.json
#   "name":      "my-node-name"
#   "overlay_ip":"100.64.0.1"
#   "endpoint":  "[2001:db8::1]:51820"
#   "interface": "wg0"   (WG インターフェース名に合わせる)

# 3. 他 Root の追加
sudo l2mesh root add \
  --name root-b \
  --pubkey '...' \
  --endpoint '[2001:db8::2]:51820' \
  --ip 100.64.0.2

# 4. 配下 Leaf の追加 (NAT 配下なので endpoint なし)
sudo l2mesh peer add \
  --name leaf-1 \
  --pubkey '...' \
  --ip 100.64.0.10

# 5. 状態確認
sudo l2mesh status
```

## コマンド

| コマンド | 動作 |
|----------|------|
| `l2mesh status` | ノード情報 + ピア状態（ハンドシェイク・alive/stale/pending）|
| `l2mesh up` | VXLAN + bridge を作成（idempotent）、BUM (FDB) を peer に合わせて同期 |
| `l2mesh down` | VXLAN + bridge を削除 |
| `l2mesh root add --name N --pubkey K --endpoint E --ip I` | Root を追加（WG ピア + BUM 自動追加）|
| `l2mesh root remove --name N` | Root を削除（WG ピア + BUM 削除）|
| `l2mesh peer add --name N --pubkey K --ip I` | Leaf を追加（endpoint なし、NAT 配下想定）|
| `l2mesh peer remove --name N` | Leaf を削除 |
| `l2mesh peer list` | 全ピア一覧 |
| `l2mesh sync` | state.json から kernel に全部反映（WG ピア `ReplacePeers` + L2 up + FDB 同期）。boot 時 systemd 用 |

`--state PATH` でファイルパス指定可（既定: `/var/lib/l2mesh/state.json`）。

## NixOS との統合

WireGuard インターフェースは NixOS 側で宣言、ピア管理は本ツールが runtime で行う。

```nix
{ config, lib, pkgs, ... }:
{
  networking.wireguard.interfaces.wg0 = {
    ips = [ "100.64.0.1/24" ];
    listenPort = 51820;
    privateKeyFile = "/etc/wireguard/wg0.key";
    peers = [ ];   # ★ 空のまま。ピアは l2mesh が管理
  };

  systemd.tmpfiles.rules = [
    "d /var/lib/l2mesh 0755 root root - -"
  ];

  systemd.services.l2mesh-sync = {
    description = "Restore l2mesh peers from state.json on boot";
    after = [ "wireguard-wg0.service" ];
    requires = [ "wireguard-wg0.service" ];
    wantedBy = [ "multi-user.target" ];
    serviceConfig = {
      Type = "oneshot";
      RemainAfterExit = true;
      ExecStart = "/usr/local/bin/l2mesh sync";
    };
  };
}
```

NixOS の `peers = [ ]` は意図的に空にする。ピアを NixOS で持つと rebuild の度に kernel ピアがリセットされ、本ツールの runtime 操作と衝突するため。

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
    "interface": "wg0"
  },
  "l2": {
    "vxlan_iface": "vxlan-l2mesh",
    "bridge_iface": "br-l2mesh",
    "vni": 100,
    "port": 4789,
    "mtu": 1370,
    "local_ports": []
  },
  "roots": [
    { "name": "root-b", "pubkey": "...", "overlay_ip": "100.64.0.2", "endpoint": "[2001:db8::2]:51820" }
  ],
  "leafs": [
    { "name": "leaf-1", "pubkey": "...", "overlay_ip": "100.64.0.10" }
  ]
}
```

`l2.local_ports` に物理/VLAN インターフェース名を入れると `l2mesh up` 時に bridge へ attach される。MTU は WG (1420) - VXLAN overhead 50 = 1370 が既定。

書き込みは `state.json.lock` への `flock(LOCK_EX)` で直列化される（並列に複数の `l2mesh add` を叩いても安全）。実体の書き換えは temp + rename で atomic。

## 制約

- Linux x86_64（kernel WireGuard, flock 利用）
- `wgctrl` でカーネル WG を操作するため CAP_NET_ADMIN 相当が必要（通常は root で実行）
- VXLAN / EVPN / promote / demote / 死活監視はまだ動かない（Issues 参照）

## ライセンス

MIT
