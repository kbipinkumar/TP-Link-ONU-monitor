## Summary
The GPON status values are exposed directly via `/proc/pon_phy/` and read by device binaries (like `/bin/cli`), meaning direct programmatic access is possible if a shell is obtained. However, while a `telnetd` binary exists and has hardcoded credentials (`root`/`root`), it is not enabled by default at boot in the `rcS` init script. Therefore, unless telnet is manually enabled via the UI, authenticated HTTP scraping of the internal API is the most viable path for a monitor script.

## Web server architecture
The device uses `mini_httpd` (invoked at boot in `/etc/init.d/rcS` with config `/opt/www/mini_httpd.conf`) as the web server, backed by a custom C-based CGI framework (e.g., `libcgic.so` and various `/cgi/` endpoints). The frontend is a Single Page Application using jQuery; there are no Lua scripts or `.lua` files present in the web root.

## Status page data source
The file containing the status page layout is `/web/main/status.htm`.
It acts as a static template containing input fields for the values, such as:
```html
<input type="text" readonly="ture" class="tp-input-text s" id="rxPow" />
```
This template is populated at runtime by inline JavaScript using an internal data model API (`$.dm.getList`). The optical values are fetched using the TR-069-style Object ID `DEV2_OPTC_GPON_CFG`:
```javascript
$.dm.getList({oid:"DEV2_OPTC_GPON_CFG",data:{},callback:{success:function(data){
    optcGponCfgObj=data[0];
    // ...
    var txPower=parseInt(optcGponCfgObj.TXPower);
    var rxPower=parseInt(optcGponCfgObj.RXPower);
    // ...
```

## Live diagnostic value origin
Tracing the data source into the backend binaries reveals that the device driver exposes these values directly via the `procfs`. The `/bin/cli` binary contains literal strings shelling out to read these proc files:
- **RX Power**: `cat /proc/pon_phy/RSSICurrent`
- **TX Power / Bias**: `cat /proc/pon_phy/MPDCurrent`
- **Temperature**: `cat /proc/pon_phy/Temperature`

Additionally, there is `/proc/pon_phy/debug` for advanced transceiver control (e.g. `echo txctl 1 0 > /proc/pon_phy/debug`).

## Remote shell access findings
- **telnetd**: The binary exists at `/sbin/telnetd` (symlinked to busybox), but is **not invoked at boot** in `/etc/init.d/rcS` or `/etc/inittab`. However, `/etc/config/config.default.xml` contains hardcoded credentials for it: `<TelnetUserName ...>root</TelnetUserName>` and `<TelnetPassword ...>root</TelnetPassword>`. 
- **dropbear/sshd**: A dropbear profile user exists in `/etc/passwd.bak` and `/var/tmp/dropbear` is created in `rcS`, but the daemon is not started automatically.
- **Hidden/Undocumented CGI Endpoints**: Extracting from the frontend JS reveals these distinct CGI paths in use beyond standard navigation: `/cgi/ansi`, `/cgi/auth`, `/cgi/bnr`, `/cgi/clearBusy`, `/cgi/confup`, `/cgi/getBusy`, `/cgi/getParm`, `/cgi/getTokenc`, `/cgi/https`, `/cgi/info`, `/cgi/localAgentSoftup`, `/cgi/localMeshsoftburn`, `/cgi/localMeshUpgrade`, `/cgi/login`, `/cgi/logout`, `/cgi/setPwd`, `/cgi/softburn`, `/cgi/softup`, `/cgi/wanBlock`.

## Login/auth flow details
The login handler is located at `/cgi/login`, invoked via JavaScript in `/web/frame/login.htm`. 
- **Username**: Base64 encoded, then RSA encrypted using a public key (`n` and `e`) fetched from `/cgi/getParm`.
- **Password**: Sent in **cleartext** (for local device logins; cloud logins use RSA).
- **Request Shape**: The script makes an HTTP POST request, but awkwardly passes the parameters in the query string:
  ```http
  POST /cgi/login?UserName=[RSA_Encrypted_B64_User]&Passwd=[Cleartext_Password]&Action=1&LoginStatus=0 HTTP/1.1
  ```
Following this, the frontend calls `/cgi/getTokenc` to receive a token for further API requests (which subsequently manifests as the `stok=` session token pattern for authenticated endpoints).

## Recommended integration approach
**(b) Authenticated HTTP scrape of the status page API.**
While direct proc/sysfs reads are much cleaner (Option A), the `telnetd` daemon is highly restricted on this firmware build. By replicating the HTTP login flow, a script can call the `/cgi/` endpoints to retrieve the `DEV2_OPTC_GPON_CFG` JSON object directly, which is stable and avoids parsing raw HTML.

## Addendum: Script Implementation Logic & Pitfalls
During the implementation of `onu_monitor.py`, several critical discoveries superseded the initial static analysis:

1. **Authentication Corrections**: Both the Username *and* Password must be RSA-encrypted using the public keys (`n`, `e`) fetched from `/cgi/getParm`. The initial assumption that the password was sent in cleartext was incorrect for the final authentication schema. 
2. **Session Management (TokenID)**: The router does not rely solely on cookies for session persistence. After a successful POST to `/cgi/login`, the client must perform a GET request to the root `/` page to parse a dynamically injected session token (`var token="<TOKEN>";`). This token must be passed in the `TokenID` HTTP header for all subsequent API requests.
3. **Data Retrieval Payload**: The GPON stats are not fetched via a simple GET request. The client must send an HTTP POST to `/cgi?9` with a strict JSON payload:
   ```json
   {"operation":"gl","oid":"DEV2_OPTC_GPON_CFG","data":{"stack":"0,0,0,0,0,0","pstack":"0,0,0,0,0,0"}}
   ```
4. **Telnet Discoveries**: Telnet access *was* successfully achieved by forcing the username `admin` during connection (`telnet 192.168.1.1 -l admin`). This exposed the underlying GalaChip `GC1601` SoC prompt. However, even with valid credentials, the TP-Link CLI is crippled to a "normal mode" containing only `help`, `exit`, `history`, `clear`, and `logout`. Commands like `enable` or `/usr/bin/gc_omcicli` are strictly blocked ("Command not found"). This hardens the conclusion that the HTTP Python scraper is the *only* viable path forward without physical hardware exploits.
