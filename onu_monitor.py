#!/usr/bin/env python3
import requests
import rsa
import base64
import sys
import re
import json
import time
import logging
import os
import configparser
import paho.mqtt.client as mqtt
from influxdb_client import InfluxDBClient, Point, WritePrecision
from influxdb_client.client.write_api import SYNCHRONOUS

# --- Configuration ---
config = configparser.ConfigParser()
config_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'onu_config.ini')
if not config.read(config_path):
    print(f"[FATAL] Configuration file not found or unreadable: {config_path}")
    print("Please copy onu_config.example.ini to onu_config.ini and fill in your details.")
    sys.exit(1)

ONU_IP = config.get('ONU', 'IP', fallback="192.168.1.1")
USERNAME = config.get('ONU', 'USERNAME', fallback="user")
PASSWORD = config.get('ONU', 'PASSWORD', fallback="")

# MQTT Configuration
MQTT_ENABLE = config.getboolean('MQTT', 'ENABLE', fallback=False)
MQTT_BROKER = config.get('MQTT', 'BROKER', fallback="127.0.0.1")
MQTT_PORT = config.getint('MQTT', 'PORT', fallback=1883)
MQTT_USER = config.get('MQTT', 'USER', fallback="")
MQTT_PASSWORD = config.get('MQTT', 'PASSWORD', fallback="")
MQTT_TOPIC = config.get('MQTT', 'TOPIC', fallback="tele/onu/gpon_stats")
MQTT_CLIENT_ID = config.get('MQTT', 'CLIENT_ID', fallback="onu_monitor")

# InfluxDB Configuration
INFLUX_ENABLE = config.getboolean('INFLUXDB', 'ENABLE', fallback=False)
INFLUX_URL = config.get('INFLUXDB', 'URL', fallback="http://127.0.0.1:8086")
INFLUX_TOKEN = config.get('INFLUXDB', 'TOKEN', fallback="")
INFLUX_ORG = config.get('INFLUXDB', 'ORG', fallback="home")
INFLUX_BUCKET = config.get('INFLUXDB', 'BUCKET', fallback="network_stats")

# ---------------------

logging.basicConfig(level=logging.INFO, format='%(asctime)s [%(levelname)s] %(message)s')
logger = logging.getLogger("ONU_Monitor")

def get_gpon_stats():
    session = requests.Session()
    
    # 1. Fetch RSA keys
    logger.debug("Fetching RSA keys from /cgi/getParm...")
    try:
        headers = {"Referer": f"http://{ONU_IP}/"}
        resp = session.post(f"http://{ONU_IP}/cgi/getParm", headers=headers, timeout=5)
        resp.raise_for_status()
        text = resp.text
        n, e = None, None
        for line in text.split(';'):
            if 'nn=' in line:
                n = line.split('=')[1].strip().strip('"').strip("'")
            if 'ee=' in line:
                e = line.split('=')[1].strip().strip('"').strip("'")
                
        if n == "(null)":
            n = None
            e = None
    except Exception as exc:
        logger.error(f"Failed to fetch keys: {exc}")
        return None
        
    # 2. Login
    url = f"http://{ONU_IP}/cgi/login"
    
    if n and e:
        pubkey = rsa.PublicKey(int(n, 16), int(e, 16))
        b64_user = base64.b64encode(USERNAME.encode()).decode()
        encrypted_user = rsa.encrypt(b64_user.encode(), pubkey)
        hex_user = encrypted_user.hex()
        
        b64_pass = base64.b64encode(PASSWORD.encode()).decode()
        encrypted_pass = rsa.encrypt(b64_pass.encode(), pubkey)
        hex_pass = encrypted_pass.hex()
    else:
        hex_user = USERNAME
        hex_pass = PASSWORD
        
    params = {
        "UserName": hex_user,
        "Passwd": hex_pass,
        "Action": "1",
        "LoginStatus": "0"
    }
    
    headers = {
        "Referer": f"http://{ONU_IP}/"
    }
    
    try:
        resp = session.post(url, params=params, headers=headers, timeout=5)
        resp.raise_for_status()
        if "$.ret=0;" not in resp.text:
            if "$.ret=71233;" in resp.text:
                logger.error("Login failed (71233): Session limit reached or locked out. Try again later.")
            else:
                logger.error(f"Login failed: {resp.text}")
            return None
    except Exception as exc:
        logger.error(f"Login request failed: {exc}")
        return None
        
    # 3. Fetch root HTML to get the dynamically injected TokenID
    try:
        resp_root = session.get(f"http://{ONU_IP}/", headers={"Referer": f"http://{ONU_IP}/"}, timeout=5)
        resp_root.raise_for_status()
        token_match = re.search(r'var token="([^"]+)";', resp_root.text)
        if token_match:
            token_id = token_match.group(1)
        else:
            logger.error("Could not find var token in root HTML!")
            return None
    except Exception as exc:
        logger.error(f"Root request failed: {exc}")
        return None
        
    # 4. Fetch GPON stats
    url = f"http://{ONU_IP}/cgi?9"
    headers = {
        "TokenID": token_id,
        "Referer": f"http://{ONU_IP}/",
        "X-Requested-With": "XMLHttpRequest",
        "Accept": "application/json, text/javascript, */*; q=0.01"
    }
    payload = {
        "operation": "gl",
        "oid": "DEV2_OPTC_GPON_CFG",
        "data": {"stack": "0,0,0,0,0,0", "pstack": "0,0,0,0,0,0"}
    }
    compact_json = json.dumps(payload, separators=(',', ':')) + "\r\n"
    
    try:
        resp = session.post(url, data=compact_json, headers=headers, timeout=5)
        resp.raise_for_status()
        stats = resp.json()
    except Exception as exc:
        logger.error(f"Failed to fetch GPON stats: {exc}")
        return None
    finally:
        # 5. Logout
        logger.debug("Logging out to release session...")
        logout_payload = {
            "operation": "cgi",
            "oid": "/cgi/logout",
            "data": {"stack": "0,0,0,0,0,0", "pstack": "0,0,0,0,0,0"}
        }
        compact_logout = json.dumps(logout_payload, separators=(',', ':')) + "\r\n"
        try:
            logout_resp = session.post(url, data=compact_logout, headers=headers, timeout=5)
            logout_resp.raise_for_status()
        except Exception as e:
            logger.error(f"Logout request failed: {e}")

    # Process and return the stats
    if stats and 'data' in stats and len(stats['data']) > 0:
        gpon_data = stats['data'][0]
        try:
            import math
            raw_rx = float(gpon_data.get("RXPower", 0))
            raw_tx = float(gpon_data.get("TXPower", 0))
            
            rx_dbm = 10 * math.log10(raw_rx / 10000.0) if raw_rx > 0 else -40.0
            tx_dbm = 10 * math.log10(raw_tx / 10000.0) if raw_tx > 0 else -40.0
            
            parsed = {
                "rx_power_dbm": round(rx_dbm, 2),
                "tx_power_dbm": round(tx_dbm, 2),
                "temperature_c": round(float(gpon_data.get("transceiverTemperature", 0)) / 256.0, 2),
                "voltage_v": round(float(gpon_data.get("supplyVottage", 0)) / 1000.0, 3), # Kept in V for consistency or could just be raw mV
                "voltage_mv": float(gpon_data.get("supplyVottage", 0)),
                "bias_current_ma": round(float(gpon_data.get("biasCurrent", 0)) * 2 / 1000.0, 2),
                "status": gpon_data.get("status", "unknown"),
                "pon_type": gpon_data.get("ponType", "unknown"),
                "xpon_status": gpon_data.get("xponStatus", "unknown")
            }
            parsed["raw_rx_power"] = gpon_data.get("RXPower")
            parsed["raw_tx_power"] = gpon_data.get("TXPower")
            return parsed
        except Exception as e:
            logger.error(f"Failed to parse GPON stats: {e}")
            return None
    else:
        logger.error("No data found in GPON stats response.")
        return None

def publish_mqtt(stats):
    try:
        client = mqtt.Client(mqtt.CallbackAPIVersion.VERSION2, MQTT_CLIENT_ID)
        
        if MQTT_USER:
            client.username_pw_set(MQTT_USER, MQTT_PASSWORD)
            
        client.connect(MQTT_BROKER, MQTT_PORT, 60)
        
        # Home Assistant Auto-Discovery
        base_topic = "homeassistant/sensor/onu_monitor"
        state_topic = f"{base_topic}/state"
        
        sensors = {
            "rx_power": {"name": "ONU RX Power", "unit": "dBm", "class": "signal_strength", "val": "rx_power_dbm"},
            "tx_power": {"name": "ONU TX Power", "unit": "dBm", "class": "signal_strength", "val": "tx_power_dbm"},
            "temperature": {"name": "ONU Temperature", "unit": "°C", "class": "temperature", "val": "temperature_c"},
            "voltage": {"name": "ONU Supply Voltage", "unit": "mV", "class": "voltage", "val": "voltage_mv"},
            "bias_current": {"name": "ONU Bias Current", "unit": "mA", "class": "current", "val": "bias_current_ma"}
        }
        
        for key, info in sensors.items():
            config_topic = f"{base_topic}/{key}/config"
            config_payload = {
                "name": info["name"],
                "state_topic": state_topic,
                "unit_of_measurement": info["unit"],
                "device_class": info["class"],
                "value_template": f"{{{{ value_json.{info['val']} }}}}",
                "unique_id": f"tp_link_onu_{key}",
                "device": {
                    "identifiers": ["tp_link_xz000_g7"],
                    "name": "TP-Link XZ000-G7 ONU",
                    "manufacturer": "TP-Link"
                }
            }
            client.publish(config_topic, json.dumps(config_payload), retain=True)
            
        # Publish actual state with retain=True so HA sees it after creating the sensor
        payload = json.dumps(stats)
        state_msg = client.publish(state_topic, payload, retain=True)
        
        # Paho-MQTT needs a brief moment to process the network buffer before disconnecting
        client.loop_start()
        state_msg.wait_for_publish(timeout=2.0)
        client.loop_stop()
        
        client.disconnect()
        logger.info(f"Published to MQTT state topic {state_topic}")
    except Exception as e:
        logger.error(f"MQTT publish failed: {e}")

def publish_influxdb(stats):
    try:
        client = InfluxDBClient(url=INFLUX_URL, token=INFLUX_TOKEN, org=INFLUX_ORG)
        write_api = client.write_api(write_options=SYNCHRONOUS)
        
        p = Point("onu_stats") \
            .tag("device", "xz000_g7") \
            .tag("pon_type", stats["pon_type"]) \
            .tag("xpon_status", stats["xpon_status"]) \
            .field("temperature_c", stats["temperature_c"]) \
            .field("voltage_v", stats["voltage_v"]) \
            .field("bias_current_ma", stats["bias_current_ma"]) \
            .field("rx_power_dbm", stats["rx_power_dbm"]) \
            .field("tx_power_dbm", stats["tx_power_dbm"]) \
            .field("raw_rx_power", int(stats["raw_rx_power"]) if isinstance(stats["raw_rx_power"], str) and stats["raw_rx_power"].lstrip('-').isdigit() else (int(stats["raw_rx_power"]) if isinstance(stats["raw_rx_power"], (int, float)) else 0)) \
            .field("raw_tx_power", int(stats["raw_tx_power"]) if isinstance(stats["raw_tx_power"], str) and stats["raw_tx_power"].lstrip('-').isdigit() else (int(stats["raw_tx_power"]) if isinstance(stats["raw_tx_power"], (int, float)) else 0))
            
        write_api.write(bucket=INFLUX_BUCKET, org=INFLUX_ORG, record=p)
        client.close()
        logger.info(f"Written to InfluxDB bucket {INFLUX_BUCKET}")
    except Exception as e:
        logger.error(f"InfluxDB write failed: {e}")

if __name__ == "__main__":
    logger.info("Starting ONU GPON Status Monitor...")
    stats = get_gpon_stats()
    
    if stats:
        logger.info(f"Parsed Stats: {json.dumps(stats, indent=2)}")
        if MQTT_ENABLE:
            publish_mqtt(stats)
        if INFLUX_ENABLE:
            publish_influxdb(stats)
    else:
        logger.warning("Failed to retrieve statistics from the ONU.")
