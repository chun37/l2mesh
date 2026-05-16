# l2mesh

WireGuard + VXLAN + EVPN を組み合わせた L2 オーバーレイ VPN のエージェント CLI。tinc switch mode の代替として、L2 セグメントを暗号化付き・カーネル空間データプレーンで延伸することを狙う。

> **状態: alpha** — Root↔Root 構成の WireGuard ピア管理・VXLAN+bridge データプレーン・FRR/BGP EVPN コントロールプレーンが動作する。Leaf 対応 (promote/demote)・死活監視・テストは未実装（[Issues](https://github.com/chun37/l2mesh/issues) 参照）。

## アーキテクチャ

```
[端末: 10.x.x.x]
       │
   ┌───┴───┐
   │  br0  │  Linux bridge
   └───┬───┘
       │
   ┌───┴────┐
   │ vxlan0 │  VXLAN VNI 100 (Root: nolearning + EVPN / Leaf: learning)
   └───┬────┘
       │
   ┌───┴────┐
   │  wg0   │  WireGuard (100.64.0.0/24 オーバーレイ)
   └───┬────┘
       │
   [物理 NIC]
```

- **Root**: 公開エンドポイント (v4/v6) を持つ。FRR で BGP EVPN を運用、配下 Leaf を中継
- **Leaf**: NAT 配下可。Primary Root 経由で L2 セグメントに参加（未実装）
- ノード間の MAC 配布とループ防止は **EVPN (BGP Type-2 / Type-3 ルート)** が担当
- Root 同士はフルメッシュ iBGP (default AS 65000)、オーバーレイ IP を BGP ピアアドレスに使用

詳細: [`docs/design.md`](docs/design.md)

## セットアップ

ディストリ別:
- **Debian / Ubuntu**: [`docs/debian-setup.md`](docs/debian-setup.md)
- **NixOS**: 下記 [NixOS との統合](#nixos-との統合) 参照

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
# 1. プレースホルダ state.json を生成 (l2mesh status はファイルが無ければ作る)
sudo l2mesh status

# 2. /var/lib/l2mesh/state.json の node セクションを編集 (name / overlay_ip / endpoint / interface)

# 3. 他 Root を追加
sudo l2mesh root add \
  --name root-b \
  --pubkey '<相手の WG 公開鍵>' \
  --endpoint '[2001:db8::2]:51820' \
  --ip 100.64.0.2

# 4. VXLAN/bridge を up + BUM 同期 + FRR reload (sync は全部やる)
sudo l2mesh sync

# 5. 状態確認
sudo l2mesh status
sudo vtysh -c "show bgp l2vpn evpn summary"   # BGP セッション
```

## コマンド

| コマンド | 動作 |
|----------|------|
| `l2mesh status` | ノード/ピア/L2/FRR EVPN の状態をまとめて表示（後述の出力例参照）|
| `l2mesh up` | VXLAN + bridge を作成（idempotent）、bridge IP・BUM(FDB) を state に合わせて同期。Root では `nolearning`、Leaf では learning |
| `l2mesh down` | VXLAN + bridge を削除 |
| `l2mesh root add --name N --pubkey K --endpoint E --ip I` | Root を追加（WG ピア + BUM 自動追加 + FRR BGP neighbor 追加）|
| `l2mesh root remove --name N` | Root を削除（WG ピア + BUM + FRR neighbor 削除）|
| `l2mesh peer add --name N --pubkey K --ip I` | Leaf を追加（endpoint なし、NAT 配下想定）|
| `l2mesh peer remove --name N` | Leaf を削除 |
| `l2mesh peer list` | 全ピア一覧 |
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

## NixOS との統合

```nix
{ config, lib, pkgs, ... }:
{
  networking.wireguard.interfaces.wg-l2mesh = {
    ips = [ "100.64.0.1/24" ];
    listenPort = 51820;
    privateKeyFile = "/etc/wireguard/wg-l2mesh.key";
    peers = [ ];   # ★ 空のまま。ピアは l2mesh が管理
  };

  # bgpd を有効化 (zebra は services.frr の中で常時有効)
  services.frr.bgpd.enable = true;

  environment.systemPackages = [ pkgs.wireguard-tools pkgs.frr ];

  systemd.tmpfiles.rules = [
    "d /var/lib/l2mesh 0755 root root - -"
  ];

  systemd.services.l2mesh-sync = {
    description = "Restore l2mesh peers from state.json on boot";
    after = [ "wireguard-wg-l2mesh.service" "bgpd.service" ];
    requires = [ "wireguard-wg-l2mesh.service" ];
    wants = [ "bgpd.service" ];
    wantedBy = [ "multi-user.target" ];
    serviceConfig = {
      Type = "oneshot";
      RemainAfterExit = true;
      ExecStart = "/usr/local/bin/l2mesh sync";
    };
  };
}
```

NixOS の `peers = [ ]` は意図的に空にする。NixOS で peers を持つと rebuild の度に kernel ピアがリセットされて本ツールの runtime 操作と衝突する。FRR についても同様に config は NixOS では持たず、l2mesh が `/var/lib/l2mesh/frr.conf` に書いて `frr-reload.py` で適用する。

ファイアウォール: WG underlay の UDP listen port (51820) を inbound 許可。`wg-l2mesh` (オーバーレイ側) は全許可で良い (BGP 等の制御トラフィックも含む)。

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

書き込みは `state.json.lock` への `flock(LOCK_EX)` で直列化される（並列の `l2mesh add` でも安全）。書き換えは temp + rename で atomic。

## 制約

- Linux x86_64 (kernel WireGuard, flock, netlink)
- root 権限が必要 (CAP_NET_ADMIN + FRR の vtysh)
- Leaf 動作モード (`promote`/`demote` + フェイルオーバー監視) は未実装
- 死活監視ループ・テスト未整備

## ライセンス

MIT
