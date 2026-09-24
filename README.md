# TP-Link GPON ONU Monitor (XZ000-G7)

A Python script to scrape live GPON statistics (RX Power, TX Power, Temperature, Voltage, Bias Current) from a TP-Link XZ000-G7 GPON ONU. It natively publishes to MQTT with Home Assistant Auto-Discovery, as well as InfluxDB v2.

## ⚠️ Network Routing Caveat

Because the ONU is typically plugged into the WAN port of your primary router, its management IP address is often isolated on the WAN side rather than your local LAN. To allow this script to successfully reach the ONU from your LAN, ensure that network is correctly configured to route traffic to the ONU's subnet. 

Since routing and firewall configurations vary significantly between different router manufacturers and firmwares (e.g., OpenWrt, pfSense, UniFi, consumer routers), providing specific instructions for establishing this access is beyond the scope of this project.

## 📦 Easy Installation (Recommended)

The easiest way to install the TP-Link GPON ONU Monitor is to download the prepackaged Debian binary (`.deb`) from the [GitHub Releases](../../releases) page. 

Installing the `.deb` package automatically:
- Installs all required Python dependencies.
- Sets up a sleek Web GUI on port `8991` for easy configuration.
- Configures the background scraper daemon and `systemd` timers.

Simply download the latest `.deb` release and install it via `apt`:
```bash
sudo apt install ./tp-link-onu-monitor_*_all.deb
```
Once installed, open browser and navigate to `http://<your-device-ip>:8991` to configure credentials and monitoring settings!

---

## 🛠️ Manual Deployment (From Source)

Because the project is now written in Go, there are no Python dependency or virtual environment (PEP-668) headaches. You can compile a single static binary that runs anywhere.

### 1. Clone the Repository and Compile
Ensure you have Go installed on your system (`sudo apt install golang` or from golang.org).

```bash
git clone https://github.com/kbipinkumar/TP-Link-ONU-monitor.git
cd TP-Link-ONU-monitor
go build -ldflags="-s -w" -o onu-monitor ./cmd/onu-monitor
```

### 2. Configure the Script
Configuration is managed via an external `.ini` file. 
Copy `onu_config.example.ini` to `onu_config.ini`:
```bash
cp onu_config.example.ini onu_config.ini
```

Edit `onu_config.ini` to match your network settings:
- Under **[ONU]**, set `IP`, `USERNAME`, and `PASSWORD`.
  - ***Note***: *If ONU's web login page only asks for a password and does not ask for a username, set `USERNAME = admin` or `USERNAME = user`. The TP-Link frontend hardcodes this value in the background.*
- Under **[MQTT]**, set `ENABLE = True` and update `BROKER`. If broker requires authentication, fill in `USER` and `PASSWORD`.
- Under **[INFLUXDB]**, set `ENABLE = True` and update `URL`, `TOKEN`, `ORG`, and `BUCKET`.

### 3. Test the Scraper
You can manually run the scraper to ensure your configuration is correct:
```bash
./onu-monitor scrape
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
User=pi
Group=pi
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
