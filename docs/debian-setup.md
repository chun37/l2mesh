# Debian セットアップ手順

Debian 12 (bookworm) 以降を対象。Debian 12 は FRR 8.x、Debian 13 は FRR 10.x。どちらでも EVPN は動く。

## 1. パッケージインストール

```bash
apt update
apt install -y wireguard wireguard-tools frr frr-pythontools git golang
```

`frr-pythontools` は `frr-reload.py` を提供する。

## 2. FRR の bgpd / bfdd を有効化

```bash
sed -i 's/^bgpd=no/bgpd=yes/' /etc/frr/daemons
sed -i 's/^bfdd=no/bfdd=yes/' /etc/frr/daemons
systemctl restart frr
```

`/etc/frr/vtysh.conf` に `service integrated-vtysh-config` があることを確認。なければ追記。BFD は sub-second 単位の peer 死活検出に使う (Plan B 採用)。

## 3. WireGuard 鍵生成 + インターフェース起動

```bash
mkdir -p /etc/wireguard && chmod 700 /etc/wireguard
cd /etc/wireguard
wg genkey | tee wg-l2mesh.key | wg pubkey > wg-l2mesh.pub
chmod 600 wg-l2mesh.key
cat wg-l2mesh.pub   # ← この公開鍵を相手 Root と交換
```

`/etc/wireguard/wg-l2mesh.conf`:

```ini
[Interface]
PrivateKey = <wg-l2mesh.key の中身>
ListenPort = 51820
Address = 100.64.0.<このノード番号>/24
# Peer は書かない — l2mesh が runtime で管理する
```

起動:

```bash
systemctl enable --now wg-quick@wg-l2mesh
```

ファイアウォールがあれば公開側で UDP 51820 を inbound 許可。

## 4. l2mesh をビルド + インストール

```bash
git clone https://github.com/chun37/l2mesh /tmp/l2mesh
cd /tmp/l2mesh
go build -trimpath -ldflags="-s -w" -o l2mesh .
install -m 0755 l2mesh /usr/local/bin/
mkdir -p /var/lib/l2mesh
```

## 5. state.json 初期化

`l2mesh init` で自ノードの identity を書き込む:

```bash
sudo l2mesh init \
  --name my-node \
  --role root \
  --overlay-ip 100.64.0.2 \
  --endpoint my-public-host:51820
```

フラグを省略すると TTY 上で対話入力。`asn` は全 Root で同一にすること (iBGP, デフォルト 65000)。`overlay_ip` は Mesh 内で一意な `100.64.0.0/24` の IP。

L2 のデフォルト (`vxlan-l2mesh` / `br-l2mesh`, VNI 100 等) は自動で書かれる。`bridge_addrs` を付けたい場合は init 後に `/var/lib/l2mesh/state.json` を直接編集して `l2.bridge_addrs: ["172.16.1.2/24"]` を足す。Root の追加も同様に `l2mesh root add` で行うか、state.json を編集する:

```bash
sudo l2mesh root add \
  --name peer-root \
  --pubkey '<相手Rootの公開鍵>' \
  --endpoint '[peer-host]:51820' \
  --ip 100.64.0.1
```

## 6. systemd unit

`/etc/systemd/system/l2mesh-sync.service`:

```ini
[Unit]
Description=L2 Mesh agent: sync WG peers, VXLAN, FRR from state.json
After=wg-quick@wg-l2mesh.service frr.service
Requires=wg-quick@wg-l2mesh.service
Wants=frr.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/bin/l2mesh sync

[Install]
WantedBy=multi-user.target
```

```bash
systemctl daemon-reload
systemctl enable --now l2mesh-sync
```

## 7. 動作確認

```bash
l2mesh status                              # peer: alive
ip -d link show vxlan-l2mesh | grep nolearning
ip -br addr show br-l2mesh                 # 172.16.1.2/24
vtysh -c "show bgp l2vpn evpn summary"     # Established
ping 172.16.1.1                            # 相手 Root の bridge IP に L2 到達
```

## トラブルシュート

- **BGP が Idle のまま**: 相手側の FRR が起動していない / `asn` が一致していない / WG ハンドシェイクが未確立
- **`l2mesh sync` で FRR が失敗**: `frr-reload.py` が見つからない → `apt install frr-pythontools` を確認
- **`ping` が通らないが BGP は Established**: VXLAN の MTU 不一致 / nolearning + EVPN ルート未交換 → `vtysh -c "show evpn mac vni 100"` で対向 MAC が表示されるか確認
