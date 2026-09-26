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
- The codebase reads configuration from `onu_config.ini` using Go's `gopkg.in/ini.v1`. 
- NEVER hardcode IPs, tokens, or passwords into the codebase.
- If testing locally, duplicate `onu_config.example.ini` to `onu_config.ini` and populate the fields.
- The project is written in Go (requires Go 1.23+). It compiles to a single binary `onu-monitor` and can be built into a `.deb` package using `build_deb.sh`.

## Data Payloads
- The GPON stats API is at `/cgi?9`. It expects an HTTP POST with the exact JSON payload: `{"operation":"gl","oid":"DEV2_OPTC_GPON_CFG","data":{"stack":"0,0,0,0,0,0","pstack":"0,0,0,0,0,0"}}`

Please read `analysis_report.md` for a deeper dive into the reverse-engineering process that led to this architecture.
