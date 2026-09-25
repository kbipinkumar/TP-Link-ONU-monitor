# TP-Link GPON ONU Monitor (XZ000-G7)

A tool to scrape live GPON statistics (RX Power, TX Power, Temperature, Voltage, Bias Current) from a TP-Link XZ000-G7 GPON ONU. It natively publishes to MQTT with Home Assistant Auto-Discovery, as well as InfluxDB v2. While this tool is primarily written for XZ000-G7 GPON ONU it should be able to scrape data from other TP-link ONUs as well(not tested).

## ⚠️ Network Routing Caveat

Because the ONU is typically plugged into the WAN port of your primary router, its management IP address is often isolated on the WAN side rather than your local LAN. To allow this script to successfully reach the ONU from your LAN, ensure that your network is correctly configured to route traffic to the ONU's subnet. 

Since routing and firewall configurations vary significantly between different router manufacturers and firmwares (e.g., OpenWrt, pfSense, UniFi, consumer routers), providing specific instructions for establishing this access is beyond the scope of this project.

## 📦 Easy Installation (Recommended)

The easiest way to install the TP-Link GPON ONU Monitor is to download the prepackaged Debian binary (`.deb`) from the [GitHub Releases](../../releases) page. 

Installing the `.deb` package automatically:
- Sets up a sleek Web GUI on port `8991` for easy configuration.
- Configures the background scraper daemon and `systemd` timers.

Simply download the latest `.deb` release and install it via `apt`:
```bash
sudo apt install ./tp-link-onu-monitor_*_$(dpkg --print-architecture).deb
```
Once installed, open a browser and navigate to `https://<your-device-ip>:8991` to configure credentials and monitoring settings. (Accept the self-signed certificate warning in your browser).

---

## 🛠️ Manual Deployment (From Source)

### 1. Clone the Repository and Compile
Ensure Go 1.23 or newer is installed on your system. If your distribution's package manager provides an older version, please install the official distribution from [go.dev/dl](https://go.dev/dl/).

```bash
git clone https://github.com/kbipinkumar/TP-Link-ONU-monitor.git
cd TP-Link-ONU-monitor
go build -ldflags="-s -w -X main.Version=$(git describe --tags --always --dirty)" -o onu-monitor ./cmd/onu-monitor
```

### 2. Configure the Script
Configuration is managed via an external `.ini` file. 
Copy `onu_config.example.ini` to `onu_config.ini`:
```bash
cp onu_config.example.ini onu_config.ini
```

Edit `onu_config.ini` to match your network settings:
- Under **[ONU]**, set `IP`, `USERNAME`, and `PASSWORD`.
  - ***Note***: *If ONU's web login page only asks for a password and does not ask for a username, set `USERNAME = admin` or `USERNAME = user`. The TP-Link frontend hardcodes this value in the background and dependent on model. For XZ000-G7 GPON ONU the correct value seems to be 'user'.*
- Under **[MQTT]**, set `ENABLE = True` and update `BROKER`. If broker requires authentication, fill in `USER` and `PASSWORD`.
- Under **[INFLUXDB]**, set `ENABLE = True` and update `URL`, `TOKEN`, `ORG`, and `BUCKET`.

### 3. Configure and Test the Scraper

You can seamlessly configure your credentials and monitoring settings using any of the following methods:

**Method A: Interactive CLI Wizard**
Run the built-in terminal UI wizard to be guided step-by-step through setting up your Web UI credentials, ONU login, MQTT, and InfluxDB:
```bash
./onu-monitor setup
```

**Method B: Web GUI**
Alternatively, launch the Web GUI to configure everything from your browser:
```bash
./onu-monitor web
```
*(Once running, navigate to `https://<your-device-ip>:8991` in a browser. Accept the self-signed certificate warning).*

**Method C: Import from Backup**
If you have an existing or backed-up `.ini` configuration file, you can import it directly:
```bash
./onu-monitor import /path/to/backup.ini
```

Once configured, you can manually run the scraper to ensure your settings are correct:
```bash
./onu-monitor scrape
```
### Web GUI Password Reset
If you forget your Web GUI username or password, you can reset it locally from the terminal. This will clear the credentials and prompt you to set them up again upon your next visit to the Web GUI:
```bash
./onu-monitor reset-password
```

### 4. Setup Systemd Timer (Run Periodically)
To run the script automatically every 5 minutes in the background, use a `systemd` timer.

Create the service file `/etc/systemd/system/onu_monitor.service`:
```ini
[Unit]
Description=TP-Link GPON ONU Monitor Service
Wants=network-online.target
After=network-online.target time-sync.target

[Service]
Type=oneshot
User=<user>
Group=<user>
WorkingDirectory=/<path to>/TP-Link-ONU-monitor
ExecStart=/<path to>/TP-Link-ONU-monitor/onu-monitor scrape
```

Create the timer file `/etc/systemd/system/onu_monitor.timer`:
```ini
[Unit]
Description=Timer for TP-Link GPON ONU Monitor Service

[Timer]
# Run every 5 minutes
OnBootSec=5min
OnUnitActiveSec=5min
Unit=onu_monitor.service

[Install]
WantedBy=timers.target
```

### 5. Enable and Start the Timer
```bash
sudo systemctl daemon-reload
sudo systemctl enable onu_monitor.timer
sudo systemctl start onu_monitor.timer
```

Check the logs for errors using:
```bash
sudo journalctl -u onu_monitor.service -f
```

## Home Assistant Integration
If MQTT is enabled, the script will automatically publish Home Assistant MQTT Discovery payloads to `homeassistant/sensor/onu_monitor/...`. 

Ensure that the **MQTT integration** is installed in Home Assistant. The sensors will automatically appear under the device **TP-Link XZ000-G7 ONU**, tracking:
- ONU RX Power (dBm)
- ONU TX Power (dBm)
- ONU Temperature (°C)
- ONU Supply Voltage (mV)
- ONU Bias Current (mA)

## Grafana Dashboard Integration
This repository includes an auto-generated Grafana dashboard designed for the GPON stats stored in InfluxDB. 

### How to Import the Dashboard:
1. Ensure script is actively pushing data to InfluxDB bucket.
2. In Grafana Web UI, navigate to **Dashboards** -> **New** -> **Import**.
3. Click **Upload JSON file** and select the `grafana_dashboard.json` file from this repository, or simply open the file in a text editor and copy/paste its contents into the **Import via panel json** box.
4. Click **Load**.
5. At the bottom, Grafana will ask you map the correct `InfluxDB` data source. Select the configured InfluxDB instance from the dropdown.
6. Click **Import**.

GPON Optical Power trends, Current RX/TX Gauges, and Temperature timeseries will instantly populate.


> **Disclaimer**: This is an LLM-generated project intended strictly for private/hobby use. It is provided "as is" without any warranties, guarantees, or official support. Please review the code and use it at your own risk before deploying it in any critical or production environments.
