#!/usr/bin/env python3
"""
Google Ping Monitor
Google(8.8.8.8)への継続的なpingモニタリングと統計報告
"""

import subprocess
import time
import json
import requests
import platform
import socket
import re
from datetime import datetime, timedelta
from statistics import mean
from threading import Thread, Event
import signal
import sys
import os

class PingMonitor:
    def __init__(self, config_file="config.json"):
        self.target_ip = "8.8.8.8"
        self.ping_interval = 1  # 1秒間隔
        self.ping_results = []
        self.unreachable_times = []
        self.running = True
        self.stop_event = Event()
        
        # 設定ファイルの読み込み
        self.load_config(config_file)
        
        # デフォルトゲートウェイを取得
        self.default_gateway = self.get_default_gateway()
        print(f"デフォルトゲートウェイ: {self.default_gateway}")
        
        # 自分のIPアドレスを取得
        self.local_ip = self.get_local_ip()
        print(f"送信元IPアドレス: {self.local_ip}")
        
        # シグナルハンドラーの設定
        signal.signal(signal.SIGINT, self.signal_handler)
        signal.signal(signal.SIGTERM, self.signal_handler)
    
    def load_config(self, config_file):
        """設定ファイルを読み込む"""
        try:
            with open(config_file, 'r', encoding='utf-8') as f:
                config = json.load(f)
                self.webhook_url = config.get('discord_webhook_url')
                if not self.webhook_url or self.webhook_url == "https://discord.com/api/webhooks/YOUR_WEBHOOK_ID/YOUR_WEBHOOK_TOKEN":
                    print("警告: Discord Webhook URLが設定されていません。config.jsonを編集してください。")
        except FileNotFoundError:
            print(f"設定ファイル {config_file} が見つかりません。")
            sys.exit(1)
        except json.JSONDecodeError:
            print(f"設定ファイル {config_file} の形式が正しくありません。")
            sys.exit(1)
    
    def get_default_gateway(self):
        """デフォルトゲートウェイのIPアドレスを取得（Windows/Linux対応）"""
        try:
            if platform.system() == "Windows":
                # Windows: route print コマンドを使用
                result = subprocess.run(['route', 'print', '0.0.0.0'], 
                                      capture_output=True, text=True, timeout=10)
                for line in result.stdout.split('\n'):
                    if '0.0.0.0' in line and 'Gateway' not in line:
                        parts = line.split()
                        if len(parts) >= 3:
                            return parts[2]
            else:
                # Linux/Unix: ip route コマンドを使用
                result = subprocess.run(['ip', 'route', 'show', 'default'], 
                                      capture_output=True, text=True, timeout=10)
                if result.returncode == 0:
                    match = re.search(r'default via (\d+\.\d+\.\d+\.\d+)', result.stdout)
                    if match:
                        return match.group(1)
                
                # 古いシステム用にrouteコマンドも試す
                result = subprocess.run(['route', '-n'], 
                                      capture_output=True, text=True, timeout=10)
                for line in result.stdout.split('\n'):
                    if line.startswith('0.0.0.0'):
                        parts = line.split()
                        if len(parts) >= 2:
                            return parts[1]
        except Exception as e:
            print(f"デフォルトゲートウェイの取得に失敗: {e}")
        
        return "192.168.1.1"  # フォールバック
    
    def get_local_ip(self):
        """ローカルIPアドレスを取得"""
        try:
            # Googleの公開DNSに接続を試行してローカルIPを取得
            with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as s:
                s.connect(("8.8.8.8", 80))
                return s.getsockname()[0]
        except Exception:
            return "不明"
    
    def ping_host(self, host):
        """指定したホストにpingを送信"""
        try:
            if platform.system() == "Windows":
                cmd = ['ping', '-n', '1', '-w', '3000', host]
            else:
                cmd = ['ping', '-c', '1', '-W', '3', host]
            
            start_time = time.time()
            result = subprocess.run(cmd, capture_output=True, text=True, timeout=5)
            end_time = time.time()
            
            if result.returncode == 0:
                # 応答時間をパース
                if platform.system() == "Windows":
                    match = re.search(r'時間[<>=]*(\d+)ms', result.stdout)
                    if match:
                        return float(match.group(1))
                else:
                    match = re.search(r'time=(\d+\.?\d*).*ms', result.stdout)
                    if match:
                        return float(match.group(1))
                
                # パースに失敗した場合は測定時間を使用
                return (end_time - start_time) * 1000
            else:
                return None
                
        except Exception as e:
            print(f"Ping実行エラー: {e}")
            return None
    
    def ping_loop(self):
        """メインのpingループ"""
        print(f"Google({self.target_ip})へのpingモニタリングを開始します...")
        print("Ctrl+Cで停止できます")
        
        last_day = datetime.now().date()
        
        while self.running and not self.stop_event.is_set():
            current_time = datetime.now()
            current_date = current_time.date()
            
            # 日付が変わったら前日の統計を送信
            if current_date != last_day and self.ping_results:
                self.send_daily_report(last_day)
                self.reset_daily_data()
                last_day = current_date
            
            # Googleにping
            response_time = self.ping_host(self.target_ip)
            
            if response_time is not None:
                self.ping_results.append(response_time)
                print(f"{current_time.strftime('%H:%M:%S')} - Google ping: {response_time:.1f}ms")
            else:
                # Googleに到達不能
                self.unreachable_times.append(current_time)
                print(f"{current_time.strftime('%H:%M:%S')} - Google到達不能")
                
                # デフォルトゲートウェイにping
                if self.default_gateway:
                    gw_response = self.ping_host(self.default_gateway)
                    if gw_response is not None:
                        print(f"  -> デフォルトゲートウェイ({self.default_gateway}): {gw_response:.1f}ms")
                    else:
                        print(f"  -> デフォルトゲートウェイ({self.default_gateway}): 到達不能")
            
            # 1秒待機
            if not self.stop_event.wait(self.ping_interval):
                continue
            else:
                break
    
    def reset_daily_data(self):
        """日次データをリセット"""
        self.ping_results = []
        self.unreachable_times = []
    
    def send_daily_report(self, report_date):
        """日次レポートをDiscordに送信"""
        if not self.webhook_url or "YOUR_WEBHOOK" in self.webhook_url:
            print("Discord Webhook URLが設定されていないため、レポートをコンソールに出力します：")
            self.print_daily_report(report_date)
            return
        
        try:
            # 統計を計算
            total_pings = len(self.ping_results) + len(self.unreachable_times)
            success_rate = (len(self.ping_results) / total_pings * 100) if total_pings > 0 else 0
            
            if self.ping_results:
                avg_time = mean(self.ping_results)
                max_time = max(self.ping_results)
                min_time = min(self.ping_results)
            else:
                avg_time = max_time = min_time = 0
            
            unreachable_count = len(self.unreachable_times)
            
            # Discord Embedメッセージを作成
            embed = {
                "title": f"🌐 Ping Monitor 日次レポート",
                "description": f"**日付**: {report_date}\n**対象**: Google (8.8.8.8)\n**送信元**: {self.local_ip}",
                "color": 0x00ff00 if success_rate >= 99 else 0xff9900 if success_rate >= 95 else 0xff0000,
                "fields": [
                    {
                        "name": "📊 応答時間統計",
                        "value": f"**平均**: {avg_time:.1f}ms\n**最大**: {max_time:.1f}ms\n**最小**: {min_time:.1f}ms",
                        "inline": True
                    },
                    {
                        "name": "📈 到達性統計",
                        "value": f"**成功率**: {success_rate:.2f}%\n**成功回数**: {len(self.ping_results)}\n**失敗回数**: {unreachable_count}",
                        "inline": True
                    },
                    {
                        "name": "⏱️ 監視情報",
                        "value": f"**総ping回数**: {total_pings}\n**監視間隔**: {self.ping_interval}秒",
                        "inline": True
                    }
                ],
                "timestamp": datetime.now().isoformat(),
                "footer": {
                    "text": "Ping Monitor by Python"
                }
            }
            
            if unreachable_count > 0:
                # 到達不能時間を追加
                unreachable_periods = self.format_unreachable_periods()
                embed["fields"].append({
                    "name": "⚠️ 到達不能期間",
                    "value": unreachable_periods[:1024],  # Discord制限
                    "inline": False
                })
            
            payload = {
                "embeds": [embed]
            }
            
            response = requests.post(self.webhook_url, json=payload, timeout=10)
            if response.status_code == 204:
                print(f"✅ {report_date}の日次レポートをDiscordに送信しました")
            else:
                print(f"❌ Discord送信エラー: {response.status_code}")
                self.print_daily_report(report_date)
                
        except Exception as e:
            print(f"レポート送信エラー: {e}")
            self.print_daily_report(report_date)
    
    def format_unreachable_periods(self):
        """到達不能時間を整形"""
        if not self.unreachable_times:
            return "なし"
        
        periods = []
        for unreachable_time in self.unreachable_times[:10]:  # 最初の10件
            periods.append(unreachable_time.strftime("%H:%M:%S"))
        
        result = "\n".join(periods)
        if len(self.unreachable_times) > 10:
            result += f"\n... 他{len(self.unreachable_times) - 10}件"
        
        return result
    
    def print_daily_report(self, report_date):
        """コンソールに日次レポートを出力"""
        print(f"\n{'='*50}")
        print(f"📊 Ping Monitor 日次レポート - {report_date}")
        print(f"{'='*50}")
        print(f"対象: Google (8.8.8.8)")
        print(f"送信元: {self.local_ip}")
        
        total_pings = len(self.ping_results) + len(self.unreachable_times)
        success_rate = (len(self.ping_results) / total_pings * 100) if total_pings > 0 else 0
        
        if self.ping_results:
            avg_time = mean(self.ping_results)
            max_time = max(self.ping_results)
            min_time = min(self.ping_results)
            print(f"\n📊 応答時間統計:")
            print(f"  平均: {avg_time:.1f}ms")
            print(f"  最大: {max_time:.1f}ms")
            print(f"  最小: {min_time:.1f}ms")
        
        print(f"\n📈 到達性統計:")
        print(f"  成功率: {success_rate:.2f}%")
        print(f"  成功回数: {len(self.ping_results)}")
        print(f"  失敗回数: {len(self.unreachable_times)}")
        print(f"  総ping回数: {total_pings}")
        
        if self.unreachable_times:
            print(f"\n⚠️ 到達不能時間:")
            for unreachable_time in self.unreachable_times[:10]:
                print(f"  {unreachable_time.strftime('%H:%M:%S')}")
            if len(self.unreachable_times) > 10:
                print(f"  ... 他{len(self.unreachable_times) - 10}件")
        
        print(f"{'='*50}\n")
    
    def signal_handler(self, signum, frame):
        """シグナルハンドラー"""
        print(f"\n終了シグナル({signum})を受信しました。停止中...")
        self.running = False
        self.stop_event.set()
        
        # 現在の日の統計があれば送信
        if self.ping_results or self.unreachable_times:
            print("現在の統計を送信中...")
            self.send_daily_report(datetime.now().date())
        
        sys.exit(0)
    
    def run(self):
        """メイン実行関数"""
        try:
            self.ping_loop()
        except KeyboardInterrupt:
            self.signal_handler(signal.SIGINT, None)
        except Exception as e:
            print(f"予期しないエラー: {e}")
            sys.exit(1)

def main():
    """メイン関数"""
    print("🌐 Google Ping Monitor")
    print("=" * 30)
    
    # 設定ファイルのパスを確認
    config_path = "config.json"
    if not os.path.exists(config_path):
        print(f"設定ファイル {config_path} が見つかりません。")
        return
    
    # モニター開始
    monitor = PingMonitor(config_path)
    monitor.run()

if __name__ == "__main__":
    main()
