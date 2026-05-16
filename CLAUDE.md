# l2mesh — ルール

## ドキュメント整合性

コード変更時は、関連する README / docs / 設計書の記述を**同じコミット内で**更新する。特に以下が変わったときは必須:

- CLI コマンド / サブコマンド / フラグの追加・削除・改名
- `state.json` のスキーマ（フィールド名、型、デフォルト値、必須/省略可）
- 出力フォーマット（`status` などユーザが目視する表示）
- 外部依存（FRR / WireGuard / カーネル機能の前提）
- インストール手順 / systemd unit の構造

更新対象の代表例:

| 変更 | 更新すべき箇所 |
|------|---------------|
| 新コマンド追加 | `README.md` のコマンド表 |
| FRR template 変更 | `README.md` (該当箇所) + 必要なら `docs/design.md` |
| state.json フィールド追加 | `README.md` のスキーマ + `docs/debian-setup.md` のテンプレ |
| Debian/NixOS の手順変更 | 該当 docs ファイル |
| 設計判断の変更 | `docs/design.md` |
