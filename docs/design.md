# L2 Mesh VPN Control Plane 設計書

既存の tinc (switch mode) ベースの L2 VPN を WireGuard + VXLAN + EVPN で代替するための設計。

## 背景と目的

tinc の switch モードで `10.0.0.0/8` の L2 セグメントを30ノード超で延伸している既存環境を想定する。tinc はシングルスレッド・ユーザースペース実装のためスループットがボトルネックになりやすい。データプレーンを WireGuard (暗号化) + VXLAN (L2 オーバーレイ) のカーネル空間実装に置き換え、Root 間のループ防止と MAC 配布を EVPN (BGP) で行う。

## 設計思想

- 中央コーディネーターなし
- Leaf の参加は Root 管理者が CLI で承認（Discord 等で連絡 → CLI で追加）
- WireGuard の公開鍵交換が信頼の起点
- Root 間の MAC 配布とループ防止は EVPN（FRR の BGP EVPN）で実現
- Leaf は EVPN を意識しない。Root がプロキシとして動作
- **全ノードに FRR をプリインストール。** Leaf では停止状態で待機し、いつでも Root に昇格可能

## ネットワークレイヤー構成

```
レイヤー                  アドレス空間           役割
──────────────────────────────────────────────────────────────
L2 延伸セグメント          10.0.0.0/8           延伸対象のセグメント本体
                          (br0 上)             端末が実際に使う IP

WireGuard overlay         100.64.0.0/24         ノード間の暗号化 L3 トンネル
(= VXLAN VTEP アドレス)                         VXLAN の配送インフラ
(= BGP ピアリングアドレス)                       EVPN のコントロールプレーン

物理 / underlay            各ノードのグローバル IP  WireGuard の Endpoint
──────────────────────────────────────────────────────────────
```

各ノードがオーバーレイ IP を自称（他と被らなければ自由）。Root / Leaf でレンジを分ける必要はない。昇格・降格があるため固定的なレンジ分割は行わない。

## ネットワークトポロジ

```
         Root 間フルメッシュ（WireGuard + BGP EVPN）
  Root A ◄══════════════► Root B ◄══════════════► Root C
100.64.0.1              100.64.0.2              100.64.0.3
  FRR(BGP)                FRR(BGP)                FRR(BGP)
   ▲  ▲                    ▲  ▲                    ▲
   │  │                    │  │                    │
   │  └── Leaf 2           │  └── Leaf 5           │
 Leaf 1    Leaf 3       Leaf 4    Leaf 6        Leaf 7
 .10  .12  .13          .11  .14  .15           .16
  FRR停止   FRR停止       FRR停止   FRR停止       FRR停止
            (NAT 配下、ポート開放不要)
```

- **Root**: グローバル IP・ポート開放。FRR で BGP EVPN を運用。配下 Leaf のトラフィックを中継
- **Leaf**: NAT 配下 OK。FRR インストール済みだが停止中。Root に VXLAN で接続
- **全ノード**: いつでも Root ↔ Leaf のロール切り替えが可能

## ロール切り替え

### Leaf → Root 昇格

前提: 公開 IP + ポート開放が必要。

```
1. 公開 IP を確保しポート開放 (UDP 51820)
2. $ l2mesh promote --endpoint 203.0.113.4
   内部処理:
   ├── role を root に変更
   ├── VXLAN を nolearning に切り替え
   ├── FRR 設定を生成・FRR を起動
   ├── IP forwarding を有効化
   └── 旧 Primary Root への Leaf 接続を解除
3. 他の Root 管理者に連絡（Discord 等）
4. 各 Root 管理者:
   $ l2mesh root add --name root-d --pubkey xxx --endpoint 203.0.113.4 --ip 100.64.0.20
   → WireGuard ピア追加 + FRR に BGP neighbor 追加
5. BGP ピアリング確立 → EVPN ルート交換 → 完了
```

### Root → Leaf 降格

```
1. 配下 Leaf を他の Root に移動
   $ l2mesh peer migrate --to root-a
   → 配下全 Leaf に Secondary Root への切り替えを指示
2. $ l2mesh demote --primary-root root-a
   内部処理:
   ├── FRR を停止
   ├── role を leaf に変更
   ├── VXLAN を learning 有効に切り替え
   ├── IP forwarding を無効化
   ├── BUM エントリを Primary Root のみに変更
   └── WireGuard ピアを Root 向けに再構成
3. 他の Root 管理者に連絡
4. 各 Root 管理者:
   $ l2mesh root remove --name root-d
   → WireGuard ピア削除 + FRR から BGP neighbor 削除
```

## EVPN アーキテクチャ

### なぜ EVPN が必要か

Root 間がフルメッシュで VXLAN BUM をフラッドすると、3台以上の Root でループが発生する。

```
問題: 受動的フラッド & ラーンでのループ
Root A → flood → Root B
Root A → flood → Root C
Root B → re-flood → Root C  ← 重複
Root C → re-flood → Root A  ← ループ
```

EVPN はこれを解決する:
- **MAC 配布**: BGP Type-2 ルートで MAC の所在を全 Root に通知。フラッドなしでユニキャスト転送
- **BUM 制御**: Type-3 ルート + スプリットホライズンでループフリーなフラッディング

### Root の役割（EVPN PE）

Root は EVPN の PE (Provider Edge) として動作。配下 Leaf は CE (Customer Edge) に相当。

**MAC 学習フロー:**

```
Leaf 1 の端末が通信
  │
  ▼
Root A (br0) が送信元 MAC を学習
  │
  ▼
FRR: BGP EVPN Type-2 ルートとして全 Root に広告
  │ NLRI: MAC=aa:bb:cc:dd:ee:11, VNI=100, VTEP=100.64.0.1
  │
  ▼
Root B, C: Type-2 ルートを受信 → FDB にインストール
  bridge fdb add aa:bb:cc:dd:ee:11 dev vxlan0 dst 100.64.0.1
```

### FRR が自動的に行うこと

| 動作 | 説明 |
|------|------|
| MAC 広告 | bridge で新 MAC 検出 → Type-2 ルートで全 Root に広告 |
| リモート MAC の FDB 投入 | 他 Root から Type-2 受信 → `bridge fdb add` を自動実行 |
| BUM イングレスレプリケーション | Type-3 ルートで各 Root の VTEP を交換 → BUM 先を自動構成 |
| スプリットホライズン | リモート VTEP からの BUM を他リモート VTEP に再フラッドしない |
| MAC withdraw | MAC エージング → Type-2 withdraw → 他 Root の FDB から削除 |

### Leaf の役割

Leaf は EVPN を意識しない。VXLAN は learning 有効で、Root からのフレームで受動学習。BUM は Primary Root のみに送信。

## データプレーン

### インターフェーススタック（Root / Leaf 共通構造）

```
[ローカル端末: 10.x.x.x]
        │
    ┌───┴───┐
    │  br0  │  Linux bridge
    └───┬───┘
        │
   ┌────┴────┐
   │ vxlan0  │  VXLAN (VNI: 100)
   └────┬────┘    Root: nolearning / Leaf: learning
        │
   ┌────┴────┐
   │  wg0    │  WireGuard (100.64.0.0/24)
   └────┬────┘
        │
   [物理 NIC]
```

### WireGuard 設定例 — Root A

```ini
[Interface]
PrivateKey = <root_a_privkey>
ListenPort = 51820
Address = 100.64.0.1/24

# --- 他の Root ---
[Peer]  # Root B
PublicKey = <root_b_pubkey>
Endpoint = 203.0.113.2:51820
AllowedIPs = 100.64.0.2/32
PersistentKeepalive = 25

# --- 自配下の Leaf ---
[Peer]  # Leaf 1
PublicKey = <leaf_1_pubkey>
AllowedIPs = 100.64.0.10/32
```

### WireGuard 設定例 — Leaf 1

```ini
[Interface]
PrivateKey = <leaf_1_privkey>
ListenPort = 51820
Address = 100.64.0.10/24

[Peer]  # Primary Root A
PublicKey = <root_a_pubkey>
Endpoint = 203.0.113.1:51820
AllowedIPs = 100.64.0.0/24
PersistentKeepalive = 25

[Peer]  # Secondary Root B
PublicKey = <root_b_pubkey>
Endpoint = 203.0.113.2:51820
AllowedIPs = 100.64.0.2/32
PersistentKeepalive = 25
```

### VXLAN + Bridge 設定 — Root

```bash
ip link add vxlan0 type vxlan \
  id 100 local 100.64.0.1 dstport 4789 nolearning

ip link add br0 type bridge
ip link set vxlan0 master br0
ip link set vxlan0 up
ip link set br0 up

sysctl -w net.ipv4.ip_forward=1
```

### VXLAN + Bridge 設定 — Leaf

```bash
ip link add vxlan0 type vxlan \
  id 100 local 100.64.0.10 dstport 4789

ip link add br0 type bridge
ip link set vxlan0 master br0
ip link set vxlan0 up
ip link set br0 up

# BUM → Primary Root のみ
bridge fdb append 00:00:00:00:00:00 dev vxlan0 dst 100.64.0.1
```

### BUM エントリ

**Root**: Root 間は EVPN (Type-3) が自動管理。配下 Leaf のみ Agent が管理:

```bash
# Leaf（Agent 管理）
bridge fdb append 00:00:00:00:00:00 dev vxlan0 dst 100.64.0.10  # Leaf 1
bridge fdb append 00:00:00:00:00:00 dev vxlan0 dst 100.64.0.13  # Leaf 3
# 他 Root（EVPN 自動管理 → 手動設定不要）
```

**Leaf**: Primary Root の VTEP のみ:

```bash
bridge fdb append 00:00:00:00:00:00 dev vxlan0 dst 100.64.0.1
```

## パケットフロー

### Leaf 1 → Leaf 5（異なる Root 配下）

```
Leaf 1 (br0)
  │ L2 フレーム: src=10.1.2.3, dst=10.4.5.6
  ▼
Leaf 1 (vxlan0) → VXLAN encap → dst VTEP: 100.64.0.1 (Root A)
  ▼
Leaf 1 (wg0) ──WG──► Root A (wg0)
  ▼
Root A (vxlan0) decap → br0 FDB: dst MAC → VTEP 100.64.0.2 (Root B, EVPN)
  ▼
Root A (vxlan0) re-encap ──WG──► Root B (wg0)
  ▼
Root B (vxlan0) decap → br0 FDB: dst MAC → VTEP 100.64.0.14 (Leaf 5, Agent)
  ▼
Root B (vxlan0) re-encap ──WG──► Leaf 5 (wg0)
  ▼
Leaf 5 (vxlan0) decap → br0 → 宛先端末
```

### BUM フロー（ブロードキャスト）

```
Leaf 1: ARP broadcast → Root A

Root A:
  ├── EVPN イングレスレプリケーション（自動）:
  │   → Root B, Root C
  └── Agent 管理:
      → 自配下 Leaf 全て

Root B 受信:
  │ スプリットホライズン → Root C への再フラッドなし
  └── 自配下 Leaf にのみ転送

Root C 受信:
  │ スプリットホライズン → Root A, B への再フラッドなし
  └── 自配下 Leaf にのみ転送
```

## コンポーネント

### Root Agent

```
Root Agent
├── データプレーン設定
│   ├── WireGuard I/F + ピア管理
│   ├── VXLAN (nolearning) + Bridge + IP fwd
│   └── 配下 Leaf の BUM エントリ管理
├── FRR 連携
│   └── FRR 設定生成 + プロセス管理
├── 死活監視（30 秒ループ）
│   └── wg latest-handshakes → dead Leaf の BUM 除外/復活
└── CLI
```

### Leaf Agent

```
Leaf Agent
├── データプレーン設定（起動時のみ）
│   ├── WireGuard I/F + Root ピア設定
│   ├── VXLAN (learning) + Bridge
│   └── BUM → Primary Root
├── フェイルオーバー監視
│   └── Primary Root ハンドシェイク監視 → Secondary に切り替え
└── FRR: インストール済み・停止中
```

## CLI

```
l2mesh peer add --name <name> --pubkey <key> --ip <overlay_ip>
    配下 Leaf を追加（WireGuard ピア + BUM エントリ）。

l2mesh peer remove --name <name>
    配下 Leaf を削除。

l2mesh peer list
    全ピア一覧（名前・overlay IP・ハンドシェイク・状態）。

l2mesh root add --name <name> --pubkey <key> --endpoint <host> --ip <overlay_ip>
    Root を追加（WireGuard ピア + FRR BGP neighbor）。

l2mesh root remove --name <name>
    Root を削除（WireGuard ピア + FRR BGP neighbor）。

l2mesh promote --endpoint <host>
    Leaf → Root に昇格。FRR 起動・VXLAN nolearning 化・IP fwd 有効化。
    他 Root で root add が必要な旨を表示。

l2mesh demote --primary-root <name>
    Root → Leaf に降格。配下 Leaf の移動確認 → FRR 停止・VXLAN learning 化。

l2mesh peer migrate --to <root-name>
    配下全 Leaf を指定 Root に移動（降格前の準備）。

l2mesh mac list
    EVPN 学習済み MAC テーブル。

l2mesh status
    ロール・BGP 状態・EVPN ルート数・Leaf 数・BUM エントリ数。
```

## FRR 設定

Agent が自動生成する FRR 設定。Root のみ。

```
frr version 10.x
frr defaults datacenter
hostname root-a

router bgp 65000
 bgp router-id 100.64.0.1
 no bgp default ipv4-unicast

 neighbor 100.64.0.2 remote-as 65000
 neighbor 100.64.0.2 update-source 100.64.0.1
 neighbor 100.64.0.3 remote-as 65000
 neighbor 100.64.0.3 update-source 100.64.0.1

 address-family l2vpn evpn
  neighbor 100.64.0.2 activate
  neighbor 100.64.0.3 activate
  advertise-all-vni
 exit-address-family
exit
```

`l2mesh root add/remove` 実行時に FRR 設定を再生成し `frr reload` で反映。

## 技術スタック

| コンポーネント | 選択肢 | 備考 |
|--------------|--------|------|
| Agent (Root/Leaf 共通バイナリ) | Go | ロールは設定ファイルで分岐 |
| WireGuard 操作 | `wgctrl-go` | ピア追加/削除・ハンドシェイク取得 |
| Netlink 操作 | `vishvananda/netlink` | VXLAN / bridge / FDB 操作 |
| EVPN | FRR | 全ノードにインストール。Root のみ起動 |
| FRR 操作 | 設定ファイル生成 + `frr reload` | Agent が FRR 設定を管理 |
| CLI | `cobra` | サブコマンド |
| ローカルストレージ | JSON ファイル | ピア一覧の永続化 |

## セキュリティ

| 脅威 | 対策 |
|------|------|
| 不正 Leaf の参加 | Root 管理者が CLI で手動承認 |
| 不正 Root の参加 | Root 同士の公開鍵は手動交換 |
| 不正な昇格 | promote しても他 Root で root add されない限りメッシュに入れない |
| BGP セッションの改竄 | WireGuard overlay 上で暗号化済み |
| 鍵漏洩 | `l2mesh peer remove` / `l2mesh root remove` で即座に無効化 |

## 制約と今後の課題

- **Root の帯域集中**: 全 Leaf トラフィックが Root 経由。Root 台数で負荷分散
- **VXLAN 二重 encap/decap**: 異なる Root 配下の Leaf 間で発生。カーネル空間のため tinc より高速
- **FRR の運用**: Agent が設定を自動生成するため直接操作は少ないが、トラブルシュート時は BGP の知識が必要
- **Leaf の BUM エントリ**: EVPN 管轄外のため Agent が管理
- **Leaf 直接接続**: 将来的に NAT ホールパンチで直接ピアリング可能
- **マルチ VNI**: 初期は VNI 固定。複数 L2 セグメントが必要になれば追加
- **overlay IP 衝突**: CLI の peer add 時に重複チェック
- **昇格時の収束**: promote → 他 Root で root add まで BGP ピアリング未確立。その間は孤立 Root として動作
