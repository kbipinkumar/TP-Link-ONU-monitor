# Agent System Prompt & Context

Hello fellow AI Assistant! If you are reading this file, you have been tasked with modifying, maintaining, or debugging the TP-Link GPON ONU Monitor project.

Here is the critical system context you need to know to avoid wasting time going down dead ends:

## Project Overview
This project scrapes optical statistics (RX/TX Power, Temperature, Voltage, Bias) from a TP-Link XZ000-G7 GPON ONU (default IP: 192.168.1.1) and pushes them to MQTT (for Home Assistant Auto-Discovery) and InfluxDB (for Grafana dashboards). 

## Architectural Quirks (CRITICAL)
- **No SSH / Restricted Telnet**: Do NOT attempt to script interactions via SSH or Telnet. Telnet exists, but the GalaChip GC1601 CLI is hard-locked to "normal mode" by the factory. There is no `enable` or shell access. 
- **Authentication Flow**: The web login is complex. It requires fetching RSA public keys (`n`, `e`) from `/cgi/getParm`, encrypting *both* the Username and Password, and sending them via an HTTP POST to `/cgi/login`.
- **406 Not Acceptable Error**: The router will block requests to `/cgi/getParm` with a 406 Error unless a valid `Referer: http://192.168.1.1/` header is provided.
- **Session Tokens**: Authentication requires a `TokenID`. After login, the script must perform a GET request to the root `/` page, parse the HTML for `var token="..."`, and send this token in the `TokenID` header for all subsequent API calls.
- **Error 71233 (Session Lockout)**: If the script fails with `$.ret=71233;`, do not assume the password is wrong! This is a TP-Link security feature meaning the session limit is reached (e.g. the user has a browser tab open), or a temporary 5-minute brute-force lockout is active. Close browser tabs and wait 5 minutes before retrying.

## Configuration
- The codebase reads configuration from `onu_config.ini` using Python's `configparser`. 
- NEVER hardcode IPs, tokens, or passwords into `onu_monitor.py`.
- If testing locally, duplicate `onu_config.example.ini` to `onu_config.ini` and populate the fields.
- The virtual environment is located at `.venv/`. If executing the script via systemd, it must be invoked with `.venv/bin/python`.

## Web GUI security
- `web_gui/web_gui.py` binds to `0.0.0.0:8991` so another device on the same LAN can configure a headless host. Keep HTTP Basic auth in front of that.
- HTTP Basic auth uses `ONU_WEB_USER` and `ONU_WEB_PASSWORD`. The `.deb` stores them in `/etc/onu-monitor/web.env` (mode 600). Do not hardcode them and do not put them in `onu_config.ini`.
- Password and token inputs must render empty (`value=""`). A blank form field keeps the stored secret.
- The packaged services run as the system user `onu-monitor`, not root. `onu_config.ini` is mode 640 and owned by that user. The only sudoers grant is `systemctl restart onu_monitor.timer`.

## Data Payloads
- The GPON stats API is at `/cgi?9`. It expects an HTTP POST with the exact JSON payload: `{"operation":"gl","oid":"DEV2_OPTC_GPON_CFG","data":{"stack":"0,0,0,0,0,0","pstack":"0,0,0,0,0,0"}}`

Please read `analysis_report.md` for a deeper dive into the reverse-engineering process that led to this architecture.
