# TP-Link GPON ONU Monitor (XZ000-G7)

A Python script to scrape live GPON statistics (RX Power, TX Power, Temperature, Voltage, Bias Current) from a TP-Link XZ000-G7 GPON ONU. It natively publishes to MQTT with Home Assistant Auto-Discovery, as well as InfluxDB v2.

## Deployment on Raspberry Pi (Debian Trixie)

Debian Trixie (and Raspberry Pi OS based on it) enforces PEP-668, meaning system-wide `pip install` is disabled. Therefore, all the required dependencies need to be installed via the `apt` package manager as descibed below.

### 1. Clone or Copy the Repository
Place `onu_monitor.py` into a directory, e.g., `/home/pi/onu_monitor`.

```bash
mkdir -p /home/pi/onu_monitor
cd /home/pi/onu_monitor
# Copy the file here
```

### 2. Configure the Script
Configuration is managed via an external `.ini` file to avoid touching the actual code. 
Copy `onu_config.example.ini` to `onu_config.ini`:
```bash
cp onu_config.example.ini onu_config.ini
```

Edit `onu_config.ini` to match your network settings:
- Under **[ONU]**, set your `IP`, `USERNAME`, and `PASSWORD`.
  - ***Note***: *If ONU's web login page only asks for a password and does not ask for a username, set `USERNAME = admin`. The TP-Link frontend hardcodes this value in the background.*
- Under **[MQTT]**, set `ENABLE = True` and update `BROKER`. If your broker requires authentication, fill in `USER` and `PASSWORD`.
- Under **[INFLUXDB]**, set `ENABLE = True` and update `URL`, `TOKEN`, `ORG`, and `BUCKET`.

### 3. Install Dependencies
Install all required Python libraries via `apt`:

```bash
sudo apt update
sudo apt install python3-requests python3-rsa python3-paho-mqtt python3-influxdb-client
```

*(Alternatively, you can create a Python virtual environment and run `pip install -r requirements.txt`, but ensure your `systemd` service points to the python binary inside your `.venv`!)*

### 4. Setup Systemd Timer (Run Periodically)
To run the script automatically every 5 minutes in the background, we will use a `systemd` timer.

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
WorkingDirectory=/home/pi/onu_monitor
ExecStart=/usr/bin/python3 -u /home/pi/onu_monitor/onu_monitor.py
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

You can check the logs at any time using:
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
1. Ensure your script is actively pushing data to your InfluxDB bucket.
2. In your Grafana Web UI, navigate to **Dashboards** -> **New** -> **Import**.
3. Click **Upload JSON file** and select the `grafana_dashboard.json` file from this repository, or simply open the file in a text editor and copy/paste its contents into the **Import via panel json** box.
4. Click **Load**.
5. At the bottom, Grafana will ask you to map the `InfluxDB` data source. Select your configured InfluxDB instance from the dropdown.
6. Click **Import**.

Your GPON Optical Power trends, Current RX/TX Gauges, and Temperature timeseries will instantly populate.
