package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Config represents the configuration structure
type Config struct {
	DiscordWebhookURL string `json:"discord_webhook_url"`
}

// PingResult represents a single ping result
type PingResult struct {
	Timestamp    time.Time
	ResponseTime float64
	Success      bool
}

// PingMonitor handles ping monitoring functionality
type PingMonitor struct {
	targetIP         string
	pingInterval     time.Duration
	pingResults      []PingResult
	unreachableTimes []time.Time
	running          bool
	stopChan         chan struct{}
	mutex            sync.RWMutex
	config           Config
	defaultGateway   string
	localIP          string
}

// DiscordEmbed represents Discord embed structure
type DiscordEmbed struct {
	Title       string        `json:"title"`
	Description string        `json:"description"`
	Color       int           `json:"color"`
	Fields      []EmbedField  `json:"fields"`
	Timestamp   string        `json:"timestamp"`
	Footer      EmbedFooter   `json:"footer"`
}

// EmbedField represents Discord embed field
type EmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

// EmbedFooter represents Discord embed footer
type EmbedFooter struct {
	Text string `json:"text"`
}

// DiscordMessage represents Discord message structure
type DiscordMessage struct {
	Embeds []DiscordEmbed `json:"embeds"`
}

// NewPingMonitor creates a new PingMonitor instance
func NewPingMonitor(configFile string) (*PingMonitor, error) {
	pm := &PingMonitor{
		targetIP:     "8.8.8.8",
		pingInterval: 1 * time.Second,
		running:      true,
		stopChan:     make(chan struct{}),
	}

	// Load configuration
	if err := pm.loadConfig(configFile); err != nil {
		return nil, err
	}

	// Get default gateway
	pm.defaultGateway = pm.getDefaultGateway()
	fmt.Printf("デフォルトゲートウェイ: %s\n", pm.defaultGateway)

	// Get local IP
	pm.localIP = pm.getLocalIP()
	fmt.Printf("送信元IPアドレス: %s\n", pm.localIP)

	return pm, nil
}

// loadConfig loads configuration from file
func (pm *PingMonitor) loadConfig(configFile string) error {
	data, err := os.ReadFile(configFile)
	if err != nil {
		return fmt.Errorf("設定ファイル %s が見つかりません: %v", configFile, err)
	}

	if err := json.Unmarshal(data, &pm.config); err != nil {
		return fmt.Errorf("設定ファイル %s の形式が正しくありません: %v", configFile, err)
	}

	if pm.config.DiscordWebhookURL == "" || strings.Contains(pm.config.DiscordWebhookURL, "YOUR_WEBHOOK") {
		fmt.Println("警告: Discord Webhook URLが設定されていません。config.jsonを編集してください。")
	}

	return nil
}

// getDefaultGateway gets the default gateway IP address
func (pm *PingMonitor) getDefaultGateway() string {
	var cmd *exec.Cmd
	
	if runtime.GOOS == "windows" {
		cmd = exec.Command("route", "print", "0.0.0.0")
	} else {
		// Try ip route first
		cmd = exec.Command("ip", "route", "show", "default")
	}

	output, err := cmd.Output()
	if err == nil {
		if runtime.GOOS == "windows" {
			// Parse Windows route output
			lines := strings.Split(string(output), "\n")
			for _, line := range lines {
				if strings.Contains(line, "0.0.0.0") && !strings.Contains(line, "Gateway") {
					fields := strings.Fields(line)
					if len(fields) >= 3 {
						return fields[2]
					}
				}
			}
		} else {
			// Parse Linux ip route output
			re := regexp.MustCompile(`default via (\d+\.\d+\.\d+\.\d+)`)
			if match := re.FindStringSubmatch(string(output)); len(match) > 1 {
				return match[1]
			}
		}
	}

	// Fallback to route command for older Linux systems
	if runtime.GOOS != "windows" {
		cmd = exec.Command("route", "-n")
		if output, err := cmd.Output(); err == nil {
			lines := strings.Split(string(output), "\n")
			for _, line := range lines {
				if strings.HasPrefix(line, "0.0.0.0") {
					fields := strings.Fields(line)
					if len(fields) >= 2 {
						return fields[1]
					}
				}
			}
		}
	}

	return "192.168.1.1" // Fallback
}

// getLocalIP gets the local IP address
func (pm *PingMonitor) getLocalIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "不明"
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}

// pingHost pings the specified host and returns response time in milliseconds
func (pm *PingMonitor) pingHost(host string) (float64, error) {
	var cmd *exec.Cmd
	
	if runtime.GOOS == "windows" {
		cmd = exec.Command("ping", "-n", "1", "-w", "3000", host)
	} else {
		cmd = exec.Command("ping", "-c", "1", "-W", "3", host)
	}

	start := time.Now()
	output, err := cmd.Output()
	duration := time.Since(start)

	if err != nil {
		return 0, err
	}

	// Parse response time from output
	if runtime.GOOS == "windows" {
		re := regexp.MustCompile(`時間[<>=]*(\d+)ms`)
		if match := re.FindStringSubmatch(string(output)); len(match) > 1 {
			if ms, err := strconv.ParseFloat(match[1], 64); err == nil {
				return ms, nil
			}
		}
	} else {
		re := regexp.MustCompile(`time=(\d+\.?\d*).*ms`)
		if match := re.FindStringSubmatch(string(output)); len(match) > 1 {
			if ms, err := strconv.ParseFloat(match[1], 64); err == nil {
				return ms, nil
			}
		}
	}

	// If parsing failed, use measured duration
	return float64(duration.Nanoseconds()) / 1000000, nil
}

// pingLoop runs the main ping monitoring loop
func (pm *PingMonitor) pingLoop() {
	fmt.Printf("Google(%s)へのpingモニタリングを開始します...\n", pm.targetIP)
	fmt.Println("Ctrl+Cで停止できます")

	lastDay := time.Now().Format("2006-01-02")
	ticker := time.NewTicker(pm.pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-pm.stopChan:
			return
		case now := <-ticker.C:
			currentDate := now.Format("2006-01-02")

			// Check if day changed
			if currentDate != lastDay && len(pm.pingResults) > 0 {
				pm.sendDailyReport(lastDay)
				pm.resetDailyData()
				lastDay = currentDate
			}

			// Ping Google
			responseTime, err := pm.pingHost(pm.targetIP)
			
			pm.mutex.Lock()
			if err == nil {
				pm.pingResults = append(pm.pingResults, PingResult{
					Timestamp:    now,
					ResponseTime: responseTime,
					Success:      true,
				})
				fmt.Printf("%s - Google ping: %.1fms\n", now.Format("15:04:05"), responseTime)
			} else {
				// Google unreachable
				pm.unreachableTimes = append(pm.unreachableTimes, now)
				fmt.Printf("%s - Google到達不能\n", now.Format("15:04:05"))

				// Ping default gateway
				if pm.defaultGateway != "" {
					if gwResponse, gwErr := pm.pingHost(pm.defaultGateway); gwErr == nil {
						fmt.Printf("  -> デフォルトゲートウェイ(%s): %.1fms\n", pm.defaultGateway, gwResponse)
					} else {
						fmt.Printf("  -> デフォルトゲートウェイ(%s): 到達不能\n", pm.defaultGateway)
					}
				}
			}
			pm.mutex.Unlock()
		}
	}
}

// resetDailyData resets daily statistics
func (pm *PingMonitor) resetDailyData() {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()
	pm.pingResults = []PingResult{}
	pm.unreachableTimes = []time.Time{}
}

// sendDailyReport sends daily statistics to Discord
func (pm *PingMonitor) sendDailyReport(reportDate string) {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	if pm.config.DiscordWebhookURL == "" || strings.Contains(pm.config.DiscordWebhookURL, "YOUR_WEBHOOK") {
		fmt.Println("Discord Webhook URLが設定されていないため、レポートをコンソールに出力します：")
		pm.printDailyReport(reportDate)
		return
	}

	// Calculate statistics
	totalPings := len(pm.pingResults) + len(pm.unreachableTimes)
	successRate := 0.0
	if totalPings > 0 {
		successRate = float64(len(pm.pingResults)) / float64(totalPings) * 100
	}

	var avgTime, maxTime, minTime float64
	if len(pm.pingResults) > 0 {
		var sum float64
		maxTime = pm.pingResults[0].ResponseTime
		minTime = pm.pingResults[0].ResponseTime

		for _, result := range pm.pingResults {
			sum += result.ResponseTime
			if result.ResponseTime > maxTime {
				maxTime = result.ResponseTime
			}
			if result.ResponseTime < minTime {
				minTime = result.ResponseTime
			}
		}
		avgTime = sum / float64(len(pm.pingResults))
	}

	unreachableCount := len(pm.unreachableTimes)

	// Determine color based on success rate
	color := 0x00ff00 // Green
	if successRate < 99 {
		color = 0xff9900 // Orange
	}
	if successRate < 95 {
		color = 0xff0000 // Red
	}

	// Create Discord embed
	embed := DiscordEmbed{
		Title:       "🌐 Ping Monitor 日次レポート",
		Description: fmt.Sprintf("**日付**: %s\n**対象**: Google (8.8.8.8)\n**送信元**: %s", reportDate, pm.localIP),
		Color:       color,
		Fields: []EmbedField{
			{
				Name:   "📊 応答時間統計",
				Value:  fmt.Sprintf("**平均**: %.1fms\n**最大**: %.1fms\n**最小**: %.1fms", avgTime, maxTime, minTime),
				Inline: true,
			},
			{
				Name:   "📈 到達性統計",
				Value:  fmt.Sprintf("**成功率**: %.2f%%\n**成功回数**: %d\n**失敗回数**: %d", successRate, len(pm.pingResults), unreachableCount),
				Inline: true,
			},
			{
				Name:   "⏱️ 監視情報",
				Value:  fmt.Sprintf("**総ping回数**: %d\n**監視間隔**: %v", totalPings, pm.pingInterval),
				Inline: true,
			},
		},
		Timestamp: time.Now().Format(time.RFC3339),
		Footer: EmbedFooter{
			Text: "Ping Monitor by Go",
		},
	}

	if unreachableCount > 0 {
		unreachablePeriods := pm.formatUnreachablePeriods()
		embed.Fields = append(embed.Fields, EmbedField{
			Name:   "⚠️ 到達不能期間",
			Value:  unreachablePeriods,
			Inline: false,
		})
	}

	message := DiscordMessage{
		Embeds: []DiscordEmbed{embed},
	}

	// Send to Discord
	if err := pm.sendToDiscord(message); err != nil {
		fmt.Printf("❌ Discord送信エラー: %v\n", err)
		pm.printDailyReport(reportDate)
	} else {
		fmt.Printf("✅ %sの日次レポートをDiscordに送信しました\n", reportDate)
	}
}

// formatUnreachablePeriods formats unreachable periods
func (pm *PingMonitor) formatUnreachablePeriods() string {
	if len(pm.unreachableTimes) == 0 {
		return "なし"
	}

	var periods []string
	maxDisplay := 10
	for i, t := range pm.unreachableTimes {
		if i >= maxDisplay {
			break
		}
		periods = append(periods, t.Format("15:04:05"))
	}

	result := strings.Join(periods, "\n")
	if len(pm.unreachableTimes) > maxDisplay {
		result += fmt.Sprintf("\n... 他%d件", len(pm.unreachableTimes)-maxDisplay)
	}

	// Discord field value limit is 1024 characters
	if len(result) > 1024 {
		result = result[:1020] + "..."
	}

	return result
}

// sendToDiscord sends message to Discord webhook
func (pm *PingMonitor) sendToDiscord(message DiscordMessage) error {
	jsonData, err := json.Marshal(message)
	if err != nil {
		return err
	}

	resp, err := http.Post(pm.config.DiscordWebhookURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Discord API error: %d - %s", resp.StatusCode, string(body))
	}

	return nil
}

// printDailyReport prints daily report to console
func (pm *PingMonitor) printDailyReport(reportDate string) {
	fmt.Printf("\n%s\n", strings.Repeat("=", 50))
	fmt.Printf("📊 Ping Monitor 日次レポート - %s\n", reportDate)
	fmt.Printf("%s\n", strings.Repeat("=", 50))
	fmt.Printf("対象: Google (8.8.8.8)\n")
	fmt.Printf("送信元: %s\n", pm.localIP)

	totalPings := len(pm.pingResults) + len(pm.unreachableTimes)
	successRate := 0.0
	if totalPings > 0 {
		successRate = float64(len(pm.pingResults)) / float64(totalPings) * 100
	}

	if len(pm.pingResults) > 0 {
		var sum float64
		maxTime := pm.pingResults[0].ResponseTime
		minTime := pm.pingResults[0].ResponseTime

		for _, result := range pm.pingResults {
			sum += result.ResponseTime
			if result.ResponseTime > maxTime {
				maxTime = result.ResponseTime
			}
			if result.ResponseTime < minTime {
				minTime = result.ResponseTime
			}
		}
		avgTime := sum / float64(len(pm.pingResults))

		fmt.Printf("\n📊 応答時間統計:\n")
		fmt.Printf("  平均: %.1fms\n", avgTime)
		fmt.Printf("  最大: %.1fms\n", maxTime)
		fmt.Printf("  最小: %.1fms\n", minTime)
	}

	fmt.Printf("\n📈 到達性統計:\n")
	fmt.Printf("  成功率: %.2f%%\n", successRate)
	fmt.Printf("  成功回数: %d\n", len(pm.pingResults))
	fmt.Printf("  失敗回数: %d\n", len(pm.unreachableTimes))
	fmt.Printf("  総ping回数: %d\n", totalPings)

	if len(pm.unreachableTimes) > 0 {
		fmt.Printf("\n⚠️ 到達不能時間:\n")
		for i, t := range pm.unreachableTimes {
			if i >= 10 {
				fmt.Printf("  ... 他%d件\n", len(pm.unreachableTimes)-10)
				break
			}
			fmt.Printf("  %s\n", t.Format("15:04:05"))
		}
	}

	fmt.Printf("%s\n\n", strings.Repeat("=", 50))
}

// Stop stops the ping monitor
func (pm *PingMonitor) Stop() {
	pm.mutex.Lock()
	pm.running = false
	pm.mutex.Unlock()
	close(pm.stopChan)

	// Send current statistics if any
	if len(pm.pingResults) > 0 || len(pm.unreachableTimes) > 0 {
		fmt.Println("現在の統計を送信中...")
		pm.sendDailyReport(time.Now().Format("2006-01-02"))
	}
}

// Run starts the ping monitor
func (pm *PingMonitor) Run() {
	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start ping loop in goroutine
	go pm.pingLoop()

	// Wait for signal
	sig := <-sigChan
	fmt.Printf("\n終了シグナル(%v)を受信しました。停止中...\n", sig)
	pm.Stop()
}

func main() {
	fmt.Println("🌐 Google Ping Monitor")
	fmt.Println(strings.Repeat("=", 30))

	// Check if config file exists
	configPath := "config.json"
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		log.Fatalf("設定ファイル %s が見つかりません。", configPath)
	}

	// Create and start monitor
	monitor, err := NewPingMonitor(configPath)
	if err != nil {
		log.Fatalf("モニター初期化エラー: %v", err)
	}

	monitor.Run()
}
