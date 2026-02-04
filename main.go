package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Config holds all settings loaded from .env
type Config struct {
	TelegramBotToken string
	TelegramChatID   string
	SessionToken     string
	FuelThreshold    int
	CO2Threshold     int
	Timezone         *time.Location
}

// PriceSlot represents a single price entry from the API
type PriceSlot struct {
	FuelPrice int    `json:"fuel_price"`
	CO2Price  int    `json:"co2_price"`
	Time      string `json:"time"`
	Day       int    `json:"day"`
}

// PriceResponse is the API response structure
type PriceResponse struct {
	Data struct {
		Prices []PriceSlot `json:"prices"`
	} `json:"data"`
}

// TelegramResponse is the Telegram Bot API response
type TelegramResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
}

// cooldownState persists which price slot was last alerted
type cooldownState struct {
	LastFuelSlot string `json:"last_fuel_slot"`
	LastCO2Slot  string `json:"last_co2_slot"`
	LastCheck    string `json:"last_check"`
}

// cooldown tracks which price slot was last alerted per type
type cooldown struct {
	lastFuelSlot string
	lastCO2Slot  string
	lastCheck    time.Time
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime)
	log.Println("Shipping Manager Price Alert Bot starting...")

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("Config error: %s", err)
	}

	log.Printf("Config loaded - Fuel threshold: $%d/t, CO2 threshold: $%d/t, Timezone: %s", cfg.FuelThreshold, cfg.CO2Threshold, cfg.Timezone)
	log.Printf("Telegram chat ID: %s", cfg.TelegramChatID)

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	cd := loadCooldown()
	log.Printf("Cooldown state loaded - last check: %s, last fuel slot: %s, last CO2 slot: %s",
		formatCooldownTime(cd.lastCheck, cfg.Timezone),
		formatSlot(cd.lastFuelSlot), formatSlot(cd.lastCO2Slot))

	// Run immediate check on startup
	log.Println("Running initial price check...")
	checkPrices(client, cfg, cd)

	// Calculate time until next :01 or :31 (UTC-based, prices change on UTC boundaries)
	now := time.Now().UTC()
	minute := now.Minute()
	var nextCheck time.Time

	if minute < 1 {
		nextCheck = time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 1, 0, 0, time.UTC)
	} else if minute < 31 {
		nextCheck = time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 31, 0, 0, time.UTC)
	} else {
		// Next hour :01
		next := now.Add(time.Hour)
		nextCheck = time.Date(next.Year(), next.Month(), next.Day(), next.Hour(), 1, 0, 0, time.UTC)
	}

	waitDuration := time.Until(nextCheck)
	log.Printf("Next check at %s (%s) (in %s)",
		nextCheck.In(cfg.Timezone).Format("15:04"), cfg.Timezone,
		waitDuration.Truncate(time.Second))

	// Wait for first scheduled check or shutdown
	select {
	case <-time.After(waitDuration):
	case sig := <-sigChan:
		log.Printf("Received %s, shutting down", sig)
		return
	}

	// Run the scheduled check
	checkPrices(client, cfg, cd)

	// Then tick every 30 minutes
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			checkPrices(client, cfg, cd)
		case sig := <-sigChan:
			log.Printf("Received %s, shutting down", sig)
			return
		}
	}
}

// loadConfig reads .env file from the same directory as the executable
func loadConfig() (*Config, error) {
	envPath := findEnvFile()
	if envPath == "" {
		return nil, fmt.Errorf(".env file not found (checked executable dir and working dir)")
	}

	log.Printf("Loading config from: %s", envPath)

	f, err := os.Open(envPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open .env: %w", err)
	}
	defer f.Close()

	vars := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+1:])
		vars[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read .env: %w", err)
	}

	// Validate required fields
	required := []string{"TELEGRAM_BOT_TOKEN", "TELEGRAM_CHAT_ID", "SESSION_TOKEN", "FUEL_THRESHOLD", "CO2_THRESHOLD"}
	for _, key := range required {
		if vars[key] == "" {
			return nil, fmt.Errorf("missing required .env value: %s", key)
		}
	}

	fuelThreshold, err := strconv.Atoi(vars["FUEL_THRESHOLD"])
	if err != nil {
		return nil, fmt.Errorf("FUEL_THRESHOLD must be a number: %w", err)
	}

	co2Threshold, err := strconv.Atoi(vars["CO2_THRESHOLD"])
	if err != nil {
		return nil, fmt.Errorf("CO2_THRESHOLD must be a number: %w", err)
	}

	tz := resolveTimezone(vars["TIMEZONE"])

	return &Config{
		TelegramBotToken: vars["TELEGRAM_BOT_TOKEN"],
		TelegramChatID:   vars["TELEGRAM_CHAT_ID"],
		SessionToken:     vars["SESSION_TOKEN"],
		FuelThreshold:    fuelThreshold,
		CO2Threshold:     co2Threshold,
		Timezone:         tz,
	}, nil
}

// timezoneAbbreviations maps abbreviations to IANA timezone names.
// Where abbreviations are ambiguous (e.g. IST, CST, GST), the most
// populous region wins. Users needing the other meaning should use
// the full IANA name (e.g. Asia/Kolkata, America/Chicago, Asia/Dubai).
var timezoneAbbreviations = map[string]string{
	// Universal
	"UTC":  "UTC",
	"GMT":  "Europe/London",

	// Europe
	"WET":  "Europe/Lisbon",
	"WEST": "Europe/Lisbon",
	"CET":  "Europe/Berlin",
	"CEST": "Europe/Berlin",
	"MET":  "Europe/Berlin",
	"MEST": "Europe/Berlin",
	"EET":  "Europe/Bucharest",
	"EEST": "Europe/Bucharest",
	"BST":  "Europe/London",
	"IST":  "Asia/Kolkata",
	"MSK":  "Europe/Moscow",
	"SAMT": "Europe/Samara",
	"YEKT": "Asia/Yekaterinburg",
	"GET":  "Asia/Tbilisi",
	"AZT":  "Asia/Baku",
	"AMT":  "Asia/Yerevan",
	"FET":  "Europe/Minsk",
	"TRT":  "Europe/Istanbul",

	// North America
	"NST":  "America/St_Johns",
	"NDT":  "America/St_Johns",
	"AST":  "America/Halifax",
	"ADT":  "America/Halifax",
	"EST":  "America/New_York",
	"EDT":  "America/New_York",
	"CST":  "America/Chicago",
	"CDT":  "America/Chicago",
	"MST":  "America/Denver",
	"MDT":  "America/Denver",
	"PST":  "America/Los_Angeles",
	"PDT":  "America/Los_Angeles",
	"AKST": "America/Anchorage",
	"AKDT": "America/Anchorage",
	"HST":  "Pacific/Honolulu",
	"HAST": "Pacific/Honolulu",
	"HADT": "America/Adak",

	// Central America / Caribbean
	"CST6": "America/Costa_Rica",
	"ECT":  "America/Guayaquil",
	"COT":  "America/Bogota",
	"VET":  "America/Caracas",
	"PET":  "America/Lima",
	"CIDST":"America/Cayman",
	"CUT":  "America/Havana",

	// South America
	"BRT":  "America/Sao_Paulo",
	"BRST": "America/Sao_Paulo",
	"ART":  "America/Argentina/Buenos_Aires",
	"CLT":  "America/Santiago",
	"CLST": "America/Santiago",
	"UYT":  "America/Montevideo",
	"PYT":  "America/Asuncion",
	"PYST": "America/Asuncion",
	"BOT":  "America/La_Paz",
	"GFT":  "America/Cayenne",
	"SRT":  "America/Paramaribo",
	"GYT":  "America/Guyana",
	"FKT":  "Atlantic/Stanley",

	// East Asia
	"JST":  "Asia/Tokyo",
	"KST":  "Asia/Seoul",
	"CST8": "Asia/Shanghai",
	"HKT":  "Asia/Hong_Kong",
	"TWT":  "Asia/Taipei",
	"PHT":  "Asia/Manila",
	"PHST": "Asia/Manila",
	"MYT":  "Asia/Kuala_Lumpur",
	"SGT":  "Asia/Singapore",
	"BNT":  "Asia/Brunei",

	// Southeast Asia
	"ICT":  "Asia/Bangkok",
	"WIB":  "Asia/Jakarta",
	"WITA": "Asia/Makassar",
	"WIT":  "Asia/Jayapura",
	"MMT":  "Asia/Yangon",

	// South Asia
	"PKT":  "Asia/Karachi",
	"NPT":  "Asia/Kathmandu",
	"BST5": "Asia/Dhaka",
	"MVT":  "Indian/Maldives",
	"LKT":  "Asia/Colombo",

	// Central Asia
	"ALMT": "Asia/Almaty",
	"QYZT": "Asia/Qyzylorda",
	"ORAT": "Asia/Oral",
	"UZT":  "Asia/Tashkent",
	"TMT":  "Asia/Ashgabat",
	"TJT":  "Asia/Dushanbe",
	"KGT":  "Asia/Bishkek",

	// West / Central Asia
	"AFT":  "Asia/Kabul",
	"IRST": "Asia/Tehran",
	"IRDT": "Asia/Tehran",
	"GST":  "Asia/Dubai",

	// Middle East
	"AST3": "Asia/Riyadh",
	"IDT":  "Asia/Jerusalem",

	// Australia
	"AEST": "Australia/Sydney",
	"AEDT": "Australia/Sydney",
	"ACST": "Australia/Adelaide",
	"ACDT": "Australia/Adelaide",
	"AWST": "Australia/Perth",
	"LHST": "Australia/Lord_Howe",
	"LHDT": "Australia/Lord_Howe",
	"NFDT": "Pacific/Norfolk",
	"CXT":  "Indian/Christmas",
	"CCT":  "Indian/Cocos",

	// Pacific
	"NZST":  "Pacific/Auckland",
	"NZDT":  "Pacific/Auckland",
	"CHAST": "Pacific/Chatham",
	"CHADT": "Pacific/Chatham",
	"FJT":   "Pacific/Fiji",
	"FJST":  "Pacific/Fiji",
	"TVT":   "Pacific/Funafuti",
	"WST":   "Pacific/Apia",
	"TOT":   "Pacific/Tongatapu",
	"GILT":  "Pacific/Tarawa",
	"MHT":   "Pacific/Majuro",
	"PONT":  "Pacific/Pohnpei",
	"KOST":  "Pacific/Kosrae",
	"CHUT":  "Pacific/Chuuk",
	"VUT":   "Pacific/Efate",
	"SBT":   "Pacific/Guadalcanal",
	"NCT":   "Pacific/Noumea",
	"PGT":   "Pacific/Port_Moresby",
	"NRT":   "Pacific/Nauru",
	"SST":   "Pacific/Pago_Pago",
	"TAHT":  "Pacific/Tahiti",
	"CKT":   "Pacific/Rarotonga",
	"NUT":   "Pacific/Niue",
	"TKT":   "Pacific/Fakaofo",
	"GALT":  "Pacific/Galapagos",
	"MART":  "Pacific/Marquesas",
	"GAMT":  "Pacific/Gambier",
	"WAKT":  "Pacific/Wake",

	// Africa
	"CAT":  "Africa/Johannesburg",
	"SAST": "Africa/Johannesburg",
	"EAT":  "Africa/Nairobi",
	"WAT":  "Africa/Lagos",
	"WAST": "Africa/Windhoek",
	"MUT":  "Indian/Mauritius",
	"RET":  "Indian/Reunion",
	"SCT":  "Indian/Mahe",
	"CVT":  "Atlantic/Cape_Verde",

	// Atlantic
	"AZOT":  "Atlantic/Azores",
	"AZOST": "Atlantic/Azores",
	"FNT":   "America/Noronha",
	"PMST":  "America/Miquelon",
	"PMDT":  "America/Miquelon",
	"WGT":   "America/Godthab",
	"WGST":  "America/Godthab",
	"EGT":   "America/Scoresbysund",
	"EGST":  "America/Scoresbysund",
}

// resolveTimezone resolves a timezone string (abbreviation or IANA name) to a *time.Location.
// Returns local timezone if input is empty.
func resolveTimezone(input string) *time.Location {
	if input == "" {
		return time.Now().Location()
	}

	upper := strings.ToUpper(input)
	if iana, ok := timezoneAbbreviations[upper]; ok {
		loc, err := time.LoadLocation(iana)
		if err == nil {
			return loc
		}
	}

	loc, err := time.LoadLocation(input)
	if err == nil {
		return loc
	}

	log.Printf("WARNING: Unknown timezone '%s', falling back to local system timezone", input)
	return time.Now().Location()
}

// findEnvFile looks for .env in executable dir first, then working dir
func findEnvFile() string {
	// Try executable directory first
	exe, err := os.Executable()
	if err == nil {
		p := filepath.Join(filepath.Dir(exe), ".env")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	// Try working directory
	p := ".env"
	if _, err := os.Stat(p); err == nil {
		return p
	}

	return ""
}

// checkPrices fetches current prices and sends alerts if below threshold
func checkPrices(client *http.Client, cfg *Config, cd *cooldown) {
	now := time.Now().UTC()
	log.Printf("Checking prices at %s (%s)...",
		now.In(cfg.Timezone).Format("15:04:05"), cfg.Timezone)

	prices, err := fetchPrices(client, cfg)
	if err != nil {
		log.Printf("ERROR fetching prices: %s", err)
		return
	}

	if len(prices) == 0 {
		log.Println("WARNING: API returned empty price list")
		return
	}

	// Find current time slot
	hour := now.Hour()
	var slotMinute string
	if now.Minute() < 30 {
		slotMinute = "00"
	} else {
		slotMinute = "30"
	}
	currentSlot := fmt.Sprintf("%02d:%s", hour, slotMinute)

	var matched *PriceSlot
	for i := range prices {
		if prices[i].Time == currentSlot {
			matched = &prices[i]
			break
		}
	}

	if matched == nil {
		log.Printf("WARNING: No price found for time slot %s, using first available slot", currentSlot)
		// Fall back to the last slot in the list (most recent)
		matched = &prices[len(prices)-1]
		log.Printf("Using slot: %s (day %d)", matched.Time, matched.Day)
	}

	log.Printf("Current prices - Fuel: $%d/t, CO2: $%d/t (slot: %s, day: %d)",
		matched.FuelPrice, matched.CO2Price, matched.Time, matched.Day)

	// Check thresholds
	fuelGreen := matched.FuelPrice > 0 && matched.FuelPrice <= cfg.FuelThreshold
	co2Green := matched.CO2Price > 0 && matched.CO2Price <= cfg.CO2Threshold

	// Always persist check timestamp
	cd.lastCheck = time.Now()
	defer saveCooldown(cd)

	if !fuelGreen && !co2Green {
		log.Println("Prices above threshold, no alert needed")
		return
	}

	// Check if already alerted for this price slot (slot = time + day)
	slotKey := fmt.Sprintf("%s-d%d", matched.Time, matched.Day)
	canAlertFuel := fuelGreen && cd.lastFuelSlot != slotKey
	canAlertCO2 := co2Green && cd.lastCO2Slot != slotKey

	if !canAlertFuel && !canAlertCO2 {
		log.Printf("Prices are green but already alerted for slot %s", slotKey)
		return
	}

	// Build message (matching existing Node.js format)
	var message string
	if canAlertFuel && canAlertCO2 {
		message = fmt.Sprintf("*Great news, Captain!*\n\nBoth fuel and CO2 prices are looking fantastic right now!\n\nFuel: *$%d/t*\nCO2: *$%d/t*\n\nTime to stock up!",
			matched.FuelPrice, matched.CO2Price)
	} else if canAlertFuel {
		message = fmt.Sprintf("*Ahoy, Captain!*\n\nFuel prices have dropped to a great level!\n\nFuel: *$%d/t*\n\nMight be a good time to fill up your tanks!",
			matched.FuelPrice)
	} else if canAlertCO2 {
		message = fmt.Sprintf("*Ahoy, Captain!*\n\nCO2 certificate prices are looking good!\n\nCO2: *$%d/t*\n\nA fine opportunity to stock up on certificates!",
			matched.CO2Price)
	}

	// Send Telegram alert
	err = sendTelegram(client, cfg, message)
	if err != nil {
		log.Printf("ERROR sending Telegram alert: %s", err)
		return
	}

	// Mark slot as alerted
	if canAlertFuel {
		cd.lastFuelSlot = slotKey
		log.Printf("Fuel alert sent ($%d/t <= $%d/t threshold, slot %s)", matched.FuelPrice, cfg.FuelThreshold, slotKey)
	}
	if canAlertCO2 {
		cd.lastCO2Slot = slotKey
		log.Printf("CO2 alert sent ($%d/t <= $%d/t threshold, slot %s)", matched.CO2Price, cfg.CO2Threshold, slotKey)
	}
}

// fetchPrices calls the game API and returns price slots
func fetchPrices(client *http.Client, cfg *Config) ([]PriceSlot, error) {
	req, err := http.NewRequest("POST", "https://shippingmanager.cc/api/bunker/get-prices", strings.NewReader(""))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Game-Version", "1.0.313")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36")
	req.Header.Set("Origin", "https://shippingmanager.cc")
	req.Header.Set("Referer", "https://shippingmanager.cc/loading")
	req.Header.Set("Cookie", fmt.Sprintf("shipping_manager_session=%s", cfg.SessionToken))

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var priceResp PriceResponse
	if err := json.Unmarshal(body, &priceResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w (body: %s)", err, string(body))
	}

	return priceResp.Data.Prices, nil
}

// sendTelegram sends a message via Telegram Bot API
func sendTelegram(client *http.Client, cfg *Config, message string) error {
	chatID := cfg.TelegramChatID
	// Auto-prefix numeric-only chat IDs with "-" for group chats
	if isNumericOnly(chatID) {
		chatID = "-" + chatID
	}

	payload := map[string]string{
		"chat_id":    chatID,
		"text":       message,
		"parse_mode": "Markdown",
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", cfg.TelegramBotToken)
	req, err := http.NewRequest("POST", url, strings.NewReader(string(jsonData)))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Telegram request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read Telegram response: %w", err)
	}

	var tgResp TelegramResponse
	if err := json.Unmarshal(body, &tgResp); err != nil {
		return fmt.Errorf("failed to parse Telegram response: %w", err)
	}

	if !tgResp.OK {
		return fmt.Errorf("Telegram API error: %s", tgResp.Description)
	}

	log.Println("Telegram message sent successfully")
	return nil
}

// cooldownFilePath returns the path to the .cooldown file next to the executable
func cooldownFilePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ".cooldown"
	}
	return filepath.Join(filepath.Dir(exe), ".cooldown")
}

// loadCooldown reads persisted cooldown timestamps from disk
func loadCooldown() *cooldown {
	cd := &cooldown{}
	p := cooldownFilePath()

	data, err := os.ReadFile(p)
	if err != nil {
		return cd
	}

	var state cooldownState
	if err := json.Unmarshal(data, &state); err != nil {
		log.Printf("WARNING: Failed to parse .cooldown file: %s", err)
		return cd
	}

	cd.lastFuelSlot = state.LastFuelSlot
	cd.lastCO2Slot = state.LastCO2Slot
	if state.LastCheck != "" {
		if t, err := time.Parse(time.RFC3339, state.LastCheck); err == nil {
			cd.lastCheck = t
		}
	}

	return cd
}

// saveCooldown writes cooldown timestamps to disk
func saveCooldown(cd *cooldown) {
	state := cooldownState{
		LastFuelSlot: cd.lastFuelSlot,
		LastCO2Slot:  cd.lastCO2Slot,
	}
	if !cd.lastCheck.IsZero() {
		state.LastCheck = cd.lastCheck.Format(time.RFC3339)
	}

	data, err := json.Marshal(state)
	if err != nil {
		log.Printf("WARNING: Failed to marshal cooldown state: %s", err)
		return
	}

	if err := os.WriteFile(cooldownFilePath(), data, 0644); err != nil {
		log.Printf("WARNING: Failed to save .cooldown file: %s", err)
	}
}

// formatSlot returns the slot key or "none" if empty
func formatSlot(slot string) string {
	if slot == "" {
		return "none"
	}
	return slot
}

// formatCooldownTime formats a cooldown time for logging, returns "never" if zero
func formatCooldownTime(t time.Time, tz *time.Location) string {
	if t.IsZero() {
		return "never"
	}
	return t.In(tz).Format("2006-01-02 15:04:05")
}

// isNumericOnly checks if a string contains only digits
func isNumericOnly(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}
