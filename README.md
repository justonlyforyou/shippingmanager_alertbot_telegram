# Shipping Manager - Telegram Price Alert Bot

A standalone price alert bot for [Shipping Manager](https://shippingmanager.cc) that monitors fuel and CO2 prices and sends Telegram notifications when prices drop below your configured thresholds.

- Zero external dependencies (Go standard library only)
- Single binary, no installation required
- Runs on Windows, Linux, and macOS (Intel + Apple Silicon)
- Persistent cooldown state survives restarts
- 130+ timezone abbreviations supported

## Community Discord & Support

Join our [Discord Community](https://discord.gg/2wvtPz6k89) for questions, issues, or feature requests!

---

## How It Works

The bot checks fuel and CO2 prices every 30 minutes (at :01 and :31 UTC, right after prices change at :00 and :30). When a price drops to or below your threshold, it sends a Telegram message. It will only alert once per price slot to avoid spamming.

---

## Download

Pre-built binaries for Windows, Linux, and macOS are available on the [Releases](https://github.com/justonlyforyou/shippingmanager_alertbot_telegram/releases/latest) page.

---

## Setup

### 1. Create a Telegram Bot

1. Open Telegram and search for **@BotFather**
2. Send `/newbot`
3. Choose a display name (e.g. "SM Price Alert")
4. Choose a username (must end in `bot`, e.g. `sm_price_alert_bot`)
5. BotFather will reply with your **bot token** - save this for `TELEGRAM_BOT_TOKEN`

The token looks like: `123456789:ABCdefGHIjklMNOpqrsTUVwxyz`

### 2. Get Your Chat ID

The chat ID tells the bot where to send alerts. This can be a private chat (just you) or a group.

**Option A: Private chat (just you and the bot)**

1. Open a chat with your new bot in Telegram and send any message (e.g. `/start`)
2. Open this URL in your browser (replace `YOUR_BOT_TOKEN` with your actual token):
   ```
   https://api.telegram.org/botYOUR_BOT_TOKEN/getUpdates
   ```
3. Look for `"chat":{"id":123456789}` in the response - that number is your chat ID
4. Use this number as `TELEGRAM_CHAT_ID`

**Option B: Group chat**

1. Create a group in Telegram (or use an existing one)
2. Add your bot to the group
3. Send any message in the group
4. Open the same `getUpdates` URL as above
5. Look for the chat ID - group IDs are negative numbers (e.g. `-1001234567890`)
6. Use the full number including the minus sign as `TELEGRAM_CHAT_ID`

**Important:** If you enter just the numeric part without the minus sign (e.g. `1001234567890`), the bot will automatically add the `-` prefix for group chats.

### Telegram Chat Type Compatibility

| Chat Type | Supported | Notes |
|-----------|-----------|-------|
| Private chat (1-on-1 with bot) | Yes | Send `/start` to the bot first |
| Regular group | Yes | Bot must be a member |
| Supergroup | Yes | Bot must be a member |
| Private/invite-only group | Yes | Works like any group, bot just needs to be a member |
| Channel | Yes | Bot must be added as admin with "Post Messages" permission |
| Secret chat (E2E encrypted) | No | Telegram bots cannot participate in secret chats |

### 3. Get Your Session Token

The bot needs your Shipping Manager session cookie to access the game API.

**Using any browser:**

1. Log in to [shippingmanager.cc](https://shippingmanager.cc) in your browser
2. Open Developer Tools (F12 or Ctrl+Shift+I)
3. Go to the **Application** tab (Chrome/Edge) or **Storage** tab (Firefox)
4. Under **Cookies**, click on `https://shippingmanager.cc`
5. Find the cookie named `shipping_manager_session`
6. Copy its **Value** - this is your session token

**Using RebelShip Browser:**

If you use the [RebelShip Browser](https://github.com/justonlyforyou/RebelShipBrowser), the session cookie is automatically managed. You can extract it from the app's cookie storage.

**Note:** Session tokens expire periodically. When you get API errors or HTTP 401/419 responses in the bot log, you need to log in again and update the token in your `.env` file.

### 4. Configure the Bot

1. Copy `.env.example` to `.env`:
   ```
   cp .env.example .env
   ```
2. Edit `.env` with your values:
   ```
   TELEGRAM_BOT_TOKEN=123456789:ABCdefGHIjklMNOpqrsTUVwxyz
   TELEGRAM_CHAT_ID=-1001234567890
   SESSION_TOKEN=eyJpdiI6...
   FUEL_THRESHOLD=500
   CO2_THRESHOLD=10
   TIMEZONE=CET
   ```

- `FUEL_THRESHOLD` - Alert when fuel price drops to or below this value ($/t)
- `CO2_THRESHOLD` - Alert when CO2 price drops to or below this value ($/t)
- `TIMEZONE` - Optional. Used for log output timestamps. Supports 130+ abbreviations (CET, EST, PST, JST, etc.) or full IANA names (Europe/Berlin, America/New_York). Falls back to system timezone if empty.

---

## Running the Bot

### Direct Start

Place the `.env` file in the same directory as the binary, then run:

**Windows:**
```
alertbot.exe
```

**Linux:**
```
chmod +x alertbot
./alertbot
```

**macOS:**
```
chmod +x alertbot-mac-intel   # or alertbot-mac-arm for Apple Silicon
./alertbot-mac-intel
```

The bot will run an immediate price check on startup, then schedule checks every 30 minutes at :01 and :31 UTC. Press Ctrl+C to stop.

---

### Running as a Service

To keep the bot running in the background and auto-start on boot:

#### Windows (Task Scheduler)

1. Open **Task Scheduler** (search for it in the Start menu)
2. Click **Create Basic Task**
3. Name: `SM Price Alert Bot`
4. Trigger: **When the computer starts**
5. Action: **Start a program**
6. Program/script: Browse to `alertbot.exe`
7. Start in: Set to the folder containing `alertbot.exe` and `.env`
8. Finish the wizard
9. Right-click the task, select **Properties**
10. Check **Run whether user is logged on or not**
11. Under **Settings**, uncheck **Stop the task if it runs longer than**

#### Windows (NSSM - Non-Sucking Service Manager)

For a proper Windows service, use [NSSM](https://nssm.cc):

```
nssm install SMPriceAlert C:\path\to\alertbot.exe
nssm set SMPriceAlert AppDirectory C:\path\to\
nssm set SMPriceAlert DisplayName "SM Price Alert Bot"
nssm set SMPriceAlert Start SERVICE_AUTO_START
nssm start SMPriceAlert
```

Manage with:
```
nssm status SMPriceAlert
nssm stop SMPriceAlert
nssm remove SMPriceAlert confirm
```

#### Linux (systemd)

Create `/etc/systemd/system/sm-price-alert.service`:

```ini
[Unit]
Description=Shipping Manager Price Alert Bot
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=/opt/alertbot
ExecStart=/opt/alertbot/alertbot
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

Then enable and start:
```bash
sudo systemctl daemon-reload
sudo systemctl enable sm-price-alert
sudo systemctl start sm-price-alert
sudo systemctl status sm-price-alert
```

View logs:
```bash
sudo journalctl -u sm-price-alert -f
```

#### macOS (launchd)

Create `~/Library/LaunchAgents/com.sm.pricealert.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.sm.pricealert</string>
    <key>ProgramArguments</key>
    <array>
        <string>/opt/alertbot/alertbot-mac-arm</string>
    </array>
    <key>WorkingDirectory</key>
    <string>/opt/alertbot</string>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/opt/alertbot/stdout.log</string>
    <key>StandardErrorPath</key>
    <string>/opt/alertbot/stderr.log</string>
</dict>
</plist>
```

Then load:
```bash
launchctl load ~/Library/LaunchAgents/com.sm.pricealert.plist
```

Unload:
```bash
launchctl unload ~/Library/LaunchAgents/com.sm.pricealert.plist
```

---

## Building from Source

Requires [Go 1.22+](https://go.dev/dl/).

```bash
# Windows
go build -o alertbot.exe .

# Linux
GOOS=linux GOARCH=amd64 go build -o alertbot .

# macOS Intel
GOOS=darwin GOARCH=amd64 go build -o alertbot-mac-intel .

# macOS Apple Silicon (M1/M2/M3/M4)
GOOS=darwin GOARCH=arm64 go build -o alertbot-mac-arm .
```

Pre-built binaries for all platforms are available on the [Releases](https://github.com/justonlyforyou/shippingmanager_alertbot_telegram/releases) page. Releases are created automatically by GitHub Actions whenever the `VERSION` file changes on `main`.

---

## License

This project is released under the [Unlicense](LICENSE) - public domain.
