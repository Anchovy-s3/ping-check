# Google Ping Monitor

Google(8.8.8.8)への継続的なpingモニタリングと統計報告を行うPythonアプリケーションです。

## 機能

- Google(8.8.8.8)へ1秒間隔でpingを送信
- 応答時間の記録と統計計算（平均・最大・最小）
- 到達不能時間の記録
- デフォルトゲートウェイの自動検出と到達不能時の確認ping
- 1日の終わりに統計をDiscord Webhookに送信
- Windows/Linux両対応

## セットアップ

### 1. 依存パッケージのインストール

```bash
pip install -r requirements.txt
```

### 2. Discord Webhook設定

`config.json`ファイルを編集して、Discord WebhookのURLを設定してください：

```json
{
    "discord_webhook_url": "https://discord.com/api/webhooks/YOUR_WEBHOOK_ID/YOUR_WEBHOOK_TOKEN"
}
```

#### Discord Webhookの取得方法

1. Discordサーバーで、通知を送信したいチャンネルの設定を開く
2. 「連携サービス」→「ウェブフック」→「新しいウェブフック」をクリック
3. ウェブフック名を設定し、「ウェブフックURLをコピー」をクリック
4. コピーしたURLを`config.json`に貼り付け

## 使用方法

### 基本的な実行

```bash
python ping_monitor.py
```

### バックグラウンド実行（Linux）

```bash
nohup python ping_monitor.py > ping_monitor.log 2>&1 &
```

### systemdサービスとして実行（Linux）

1. サービスファイルを作成：

```bash
sudo nano /etc/systemd/system/ping-monitor.service
```

2. 以下の内容を記述：

```ini
[Unit]
Description=Google Ping Monitor
After=network.target

[Service]
Type=simple
User=your-username
WorkingDirectory=/path/to/ping-check
ExecStart=/usr/bin/python3 /path/to/ping-check/ping_monitor.py
Restart=always

[Install]
WantedBy=multi-user.target
```

3. サービスを有効化・開始：

```bash
sudo systemctl enable ping-monitor.service
sudo systemctl start ping-monitor.service
```

## 出力例

### コンソール出力

```
🌐 Google Ping Monitor
==============================
デフォルトゲートウェイ: 192.168.1.1
送信元IPアドレス: 192.168.1.100
Google(8.8.8.8)へのpingモニタリングを開始します...
Ctrl+Cで停止できます
14:30:01 - Google ping: 12.3ms
14:30:02 - Google ping: 11.8ms
14:30:03 - Google到達不能
  -> デフォルトゲートウェイ(192.168.1.1): 1.2ms
```

### Discord通知内容

- 日付と監視対象
- 送信元IPアドレス
- 応答時間統計（平均・最大・最小）
- 到達性統計（成功率・成功回数・失敗回数）
- 到達不能期間の詳細
- 監視情報（総ping回数・監視間隔）

## 停止方法

- `Ctrl+C`で停止
- systemdサービス：`sudo systemctl stop ping-monitor.service`
- プロセス終了時に現在の統計がDiscordに送信されます

## トラブルシューティング

### Discord Webhookが設定されていない場合

統計はコンソールに出力されます。

### pingコマンドが見つからない場合

システムにpingコマンドがインストールされていることを確認してください。

### デフォルトゲートウェイが取得できない場合

フォールバック値（192.168.1.1）が使用されます。

## ファイル構成

```
ping-check/
├── ping_monitor.py    # メインプログラム
├── config.json        # Discord Webhook設定
├── requirements.txt   # 依存パッケージ
└── README.md         # このファイル
```

## 動作環境

- Python 3.6以上
- Windows 10/11
- Linux (Ubuntu, CentOS, etc.)
- pingコマンドが利用可能な環境
