# NixOS セットアップ手順

NixOS 25.05 以降を想定。FRR は `services.frr` モジュール経由でインストールする。

## モジュール

`/etc/nixos/modules/l2mesh.nix` (またはお好みの場所) を作成:

```nix
{ config, lib, pkgs, ... }:
{
  networking.wireguard.interfaces.wg-l2mesh = {
    ips = [ "100.64.0.1/24" ];
    listenPort = 51820;
    privateKeyFile = "/etc/wireguard/wg-l2mesh.key";
    peers = [ ];   # ★ 空のまま。ピアは l2mesh が runtime で管理
  };

  # bgpd + bfdd を有効化 (zebra は services.frr 内で常時有効化される)
  services.frr.bgpd.enable = true;
  services.frr.bfdd.enable = true;

  environment.systemPackages = [ pkgs.wireguard-tools pkgs.frr ];

  systemd.tmpfiles.rules = [
    "d /etc/wireguard 0700 root root - -"
    "d /var/lib/l2mesh 0755 root root - -"
  ];

  systemd.services.l2mesh-sync = {
    description = "L2 Mesh agent: sync WireGuard peers, VXLAN, FRR from state.json";
    after = [ "wireguard-wg-l2mesh.service" "bgpd.service" "bfdd.service" ];
    requires = [ "wireguard-wg-l2mesh.service" ];
    wants = [ "bgpd.service" "bfdd.service" ];
    wantedBy = [ "multi-user.target" ];
    serviceConfig = {
      Type = "oneshot";
      RemainAfterExit = true;
      ExecStart = "/usr/local/bin/l2mesh sync";
      ExecStartPre = "${pkgs.coreutils}/bin/test -x /usr/local/bin/l2mesh";
    };
  };
}
```

`configuration.nix` の `imports` にこのファイルを追加し、`nixos-rebuild switch`。

## ポイント

- `peers = [ ]` は **意図的に空**。NixOS で peers を持つと rebuild の度に kernel ピアがリセットされて l2mesh の runtime 操作と衝突する
- FRR config も NixOS では持たない。l2mesh が `/var/lib/l2mesh/frr.conf` に書いて `frr-reload.py` 経由で差分反映する
- `services.frr.zebra.enable` は **書かない** — 最近の NixOS では zebra は bgpd など他デーモン有効時に常時起動する仕様で、明示的な `enable=true` はエラーになる

## WireGuard 鍵生成

```bash
sudo mkdir -p /etc/wireguard && sudo chmod 700 /etc/wireguard
sudo nix-shell -p wireguard-tools --run \
  "wg genkey | tee /etc/wireguard/wg-l2mesh.key | wg pubkey > /etc/wireguard/wg-l2mesh.pub"
sudo chmod 600 /etc/wireguard/wg-l2mesh.key
sudo cat /etc/wireguard/wg-l2mesh.pub   # ← 相手 Root と交換
```

## l2mesh バイナリ配置

NixOS の宣言的な Go パッケージ化はまだ未対応 ([#7](https://github.com/chun37/l2mesh/issues/7))。当面は `/usr/local/bin/l2mesh` に手動配置:

```bash
git clone https://github.com/chun37/l2mesh /tmp/l2mesh
cd /tmp/l2mesh
nix-shell -p go --run "go build -trimpath -ldflags='-s -w' -o l2mesh ."
sudo install -m 0755 l2mesh /usr/local/bin/l2mesh
```

## state.json 初期化

`sudo l2mesh init` で対話入力 (or フラグ指定) して `/var/lib/l2mesh/state.json` を作成:

```bash
sudo l2mesh init \
  --name my-node \
  --role root \
  --overlay-ip 100.64.0.1 \
  --endpoint '[2001:db8::1]:51820'
```

L2 / `bridge_addrs` などのデフォルト・スキーマ詳細は [README の state.json スキーマ](../README.md#statejson-スキーマ) を参照。

## ファイアウォール (nftables)

WG underlay の UDP listen port (`51820`) を inbound 許可。オーバーレイ側 (`wg-l2mesh`) は WG レイヤーで認証済みなので全許可で良い (BGP 等の制御トラフィックを含む)。

例:

```nft
chain input {
  iifname "enp1s0f0" meta nfproto ipv6 udp dport 51820 accept
  iifname "wg-l2mesh" accept
  iifname "br-l2mesh" accept
}

chain output {
  oifname "enp1s0f0" meta nfproto ipv6 udp dport 51820 accept
  oifname "wg-l2mesh" accept
  oifname "br-l2mesh" accept
}
```

カーネル WG は v4/v6 両方のソケットを必ず open する仕様 (片方を無効化する手段なし) のため、v6 のみで運用する場合は v4 51820 を明示 drop しておくと意図が明確:

```nft
meta nfproto ipv4 udp dport 51820 drop
```

## 動作確認

```bash
sudo l2mesh status
sudo systemctl status l2mesh-sync
sudo vtysh -c "show bgp l2vpn evpn summary"
```
