package scraper

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"gopkg.in/ini.v1"
)

type Config struct {
	ONU struct {
		IP       string `ini:"IP"`
		Username string `ini:"USERNAME"`
		Password string `ini:"PASSWORD"`
	} `ini:"ONU"`
	MQTT struct {
		Enable   bool   `ini:"ENABLE"`
		Broker   string `ini:"BROKER"`
		Port     int    `ini:"PORT"`
		User     string `ini:"USER"`
		Password string `ini:"PASSWORD"`
		Topic    string `ini:"TOPIC"`
		ClientID string `ini:"CLIENT_ID"`
	} `ini:"MQTT"`
	INFLUXDB struct {
		Enable   bool   `ini:"ENABLE"`
		URL      string `ini:"URL"`
		Token    string `ini:"TOKEN"`
		Org      string `ini:"ORG"`
		Bucket   string `ini:"BUCKET"`
	} `ini:"INFLUXDB"`
}

type GPONStats struct {
	RxPowerDBm    float64     `json:"rx_power_dbm"`
	TxPowerDBm    float64     `json:"tx_power_dbm"`
	TemperatureC  float64     `json:"temperature_c"`
	VoltageV      float64     `json:"voltage_v"`
	VoltageMV     float64     `json:"voltage_mv"`
	BiasCurrentMA float64     `json:"bias_current_ma"`
	Status        string      `json:"status"`
	PonType       string      `json:"pon_type"`
	XPonStatus    string      `json:"xpon_status"`
	RawRxPower    interface{} `json:"raw_rx_power"`
	RawTxPower    interface{} `json:"raw_tx_power"`
	CPUUsage      float64     `json:"cpu_usage"`
	MemUsage      float64     `json:"mem_usage"`
	Uptime        float64     `json:"uptime"`
	ModelName     string      `json:"model_name"`
	SerialNumber  string      `json:"serial_number"`
}

var (
	tokenRegex      = regexp.MustCompile(`var token="([^"]+)";`)
	lastKnownSerial string
)

func doRequest(client *http.Client, req *http.Request) ([]byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
}

type SystemStatus struct {
	LastScrapeTime   string     `json:"last_scrape_time"`
	LastMqttTime     string     `json:"last_mqtt_time"`
	LastInfluxTime   string     `json:"last_influx_time"`
	LastError        string     `json:"last_error"`
	Stats            *GPONStats `json:"stats"`
}

func ReadStatus(path string) (*SystemStatus, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return &SystemStatus{}, err
	}
	var status SystemStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return &SystemStatus{}, err
	}
	return &status, nil
}

func WriteStatus(path string, status *SystemStatus) error {
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func LoadConfig(path string) (*Config, error) {
	cfg, err := ini.Load(path)
	if err != nil {
		return nil, err
	}
	var config Config
	if err := cfg.MapTo(&config); err != nil {
		return nil, err
	}
	// Defaults
	if config.ONU.IP == "" {
		config.ONU.IP = "192.168.1.1"
	}
	if config.ONU.Username == "" {
		config.ONU.Username = "user"
	}
	if config.MQTT.Broker == "" {
		config.MQTT.Broker = "127.0.0.1"
	}
	if config.MQTT.Port == 0 {
		config.MQTT.Port = 1883
	}
	if config.MQTT.Topic == "tele/onu/gpon_stats" || config.MQTT.Topic == "homeassistant/sensor/onu_monitor/state" {
		config.MQTT.Topic = ""
	}
	if config.MQTT.ClientID == "" {
		config.MQTT.ClientID = "onu_monitor"
	}
	if config.INFLUXDB.URL == "" {
		config.INFLUXDB.URL = "http://127.0.0.1:8086"
	}
	return &config, nil
}

func GetGPONStats(cfg *Config) (*GPONStats, error) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Timeout: 5 * time.Second,
		Jar:     jar,
	}

	// 1. Fetch RSA keys
	log.Println("Fetching RSA keys from /cgi/getParm...")
	req, err := http.NewRequest("POST", "http://"+cfg.ONU.IP+"/cgi/getParm", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create RSA keys request: %w", err)
	}
	req.Header.Set("Referer", "http://"+cfg.ONU.IP+"/")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "*/*")
	
	bodyBytes, err := doRequest(client, req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch keys: %w", err)
	}
	
	text := string(bodyBytes)
	
	var nHex, eHex string
	for _, line := range strings.Split(text, ";") {
		if strings.Contains(line, "nn=") {
			parts := strings.Split(line, "=")
			if len(parts) > 1 {
				nHex = strings.Trim(parts[1], " \r\n\"'")
			}
		}
		if strings.Contains(line, "ee=") {
			parts := strings.Split(line, "=")
			if len(parts) > 1 {
				eHex = strings.Trim(parts[1], " \r\n\"'")
			}
		}
	}

	hexUser, hexPass := cfg.ONU.Username, cfg.ONU.Password
	if nHex != "" && nHex != "(null)" && eHex != "" {
		nBig := new(big.Int)
		nBig.SetString(nHex, 16)
		eBig := new(big.Int)
		eBig.SetString(eHex, 16)
		
		pubKey := &rsa.PublicKey{
			N: nBig,
			E: int(eBig.Int64()),
		}

		b64User := base64.StdEncoding.EncodeToString([]byte(cfg.ONU.Username))
		b64Pass := base64.StdEncoding.EncodeToString([]byte(cfg.ONU.Password))

		encUser, err := rsa.EncryptPKCS1v15(rand.Reader, pubKey, []byte(b64User))
		if err == nil {
			hexUser = hex.EncodeToString(encUser)
		}
		encPass, err := rsa.EncryptPKCS1v15(rand.Reader, pubKey, []byte(b64Pass))
		if err == nil {
			hexPass = hex.EncodeToString(encPass)
		}
	}

	// 2. Login
	loginURL := fmt.Sprintf("http://%s/cgi/login?UserName=%s&Passwd=%s&Action=1&LoginStatus=0", 
		cfg.ONU.IP, url.QueryEscape(hexUser), url.QueryEscape(hexPass))
	
	req, err = http.NewRequest("POST", loginURL, strings.NewReader(""))
	if err != nil {
		return nil, fmt.Errorf("failed to create login request: %w", err)
	}
	req.Header.Set("Referer", "http://"+cfg.ONU.IP+"/")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "*/*")
	
	bodyBytes, err = doRequest(client, req)
	if err != nil {
		return nil, fmt.Errorf("login request failed: %w", err)
	}
	
	respText := string(bodyBytes)
	
	if !strings.Contains(respText, "$.ret=0;") {
		if strings.Contains(respText, "$.ret=71233;") {
			return nil, fmt.Errorf("login failed (71233): session limit reached or locked out")
		}
		return nil, fmt.Errorf("login failed: %s", respText)
	}

	var tokenID string

	// Setup robust defer to always log out and prevent 71233 session limit errors
	defer func() {
		// If token parsing failed earlier, we need to fetch it now for logout
		if tokenID == "" {
			req, err := http.NewRequest("GET", "http://"+cfg.ONU.IP+"/", nil)
			if err == nil {
				req.Header.Set("Referer", "http://"+cfg.ONU.IP+"/")
				req.Header.Set("User-Agent", "Mozilla/5.0")
				req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
				if bodyBytes, err := doRequest(client, req); err == nil {
					match := tokenRegex.FindStringSubmatch(string(bodyBytes))
					if len(match) >= 2 {
						tokenID = match[1]
					}
				}
			}
		}

		if tokenID != "" {
			logoutPayload := `{"operation":"cgi","oid":"/cgi/logout","data":{"stack":"0,0,0,0,0,0","pstack":"0,0,0,0,0,0"}}` + "\r\n"
			logoutReq, err := http.NewRequest("POST", "http://"+cfg.ONU.IP+"/cgi?9", bytes.NewBufferString(logoutPayload))
			if err != nil {
				log.Printf("Failed to create logout request: %v", err)
				return
			}
			logoutReq.Header.Set("TokenID", tokenID)
			logoutReq.Header.Set("Referer", "http://"+cfg.ONU.IP+"/")
			logoutReq.Header.Set("X-Requested-With", "XMLHttpRequest")
			bodyBytes, err := doRequest(client, logoutReq)
			if err != nil {
				log.Printf("Logout request failed: %v", err)
				return
			}
			if strings.Contains(string(bodyBytes), "$.ret=0;") {
				log.Println("Session logged out successfully.")
			} else {
				log.Printf("Logout response unexpected: %s", string(bodyBytes))
			}
		} else {
			log.Println("Could not obtain TokenID for logout; session may remain open.")
		}
	}()

	// 3. Fetch root HTML to get TokenID
	req, err = http.NewRequest("GET", "http://"+cfg.ONU.IP+"/", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create root HTML request: %w", err)
	}
	req.Header.Set("Referer", "http://"+cfg.ONU.IP+"/")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	
	bodyBytes, err = doRequest(client, req)
	if err != nil {
		return nil, fmt.Errorf("root request failed: %w", err)
	}
	
	match := tokenRegex.FindStringSubmatch(string(bodyBytes))
	if len(match) < 2 {
		log.Printf("ERROR: Could not find var token. Login Response was: %s", respText)
		snippet := string(bodyBytes)
		if len(snippet) > 500 {
			snippet = snippet[:500]
		}
		log.Printf("ERROR: Root HTML snippet: %s", snippet)
		return nil, fmt.Errorf("could not find var token in root HTML")
	}
	tokenID = match[1]

	// 4. Fetch GPON stats
	statsURL := "http://" + cfg.ONU.IP + "/cgi?9"
	payload := `{"operation":"gl","oid":"DEV2_OPTC_GPON_CFG","data":{"stack":"0,0,0,0,0,0","pstack":"0,0,0,0,0,0"}}` + "\r\n"
	req, err = http.NewRequest("POST", statsURL, bytes.NewBufferString(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create stats request: %w", err)
	}
	req.Header.Set("TokenID", tokenID)
	req.Header.Set("Referer", "http://"+cfg.ONU.IP+"/")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	
	resp, err := client.Do(req) // We can't use doRequest here because we decode directly from the body stream
	if err != nil {
		return nil, fmt.Errorf("failed to fetch gpon stats: %w", err)
	}
	defer resp.Body.Close()
	
	var statsResp struct {
		Data []map[string]interface{} `json:"data"`
	}
	
	// Apply LimitReader here as well to protect against large json responses
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&statsResp); err != nil {
		return nil, fmt.Errorf("failed to decode stats: %w", err)
	}

	if len(statsResp.Data) == 0 {
		return nil, fmt.Errorf("no data found in GPON stats response")
	}

	gponData := statsResp.Data[0]
	
	parseAnyFloat := func(m map[string]interface{}, key string) (float64, error) {
		v, ok := m[key]
		if !ok {
			return 0, fmt.Errorf("missing key %s", key) // distinguish from zero value
		}
		if v == nil {
			return 0, fmt.Errorf("field %s is null", key)
		}
		switch i := v.(type) {
		case float64:
			if math.IsNaN(i) || math.IsInf(i, 0) {
				return 0, fmt.Errorf("invalid numeric value for %s: NaN or Infinity", key)
			}
			return i, nil
		case string:
			f, err := strconv.ParseFloat(i, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid numeric string for %s: %w", key, err)
			}
			if math.IsNaN(f) || math.IsInf(f, 0) {
				return 0, fmt.Errorf("invalid numeric value for %s: NaN or Infinity", key)
			}
			return f, nil
		default:
			return 0, fmt.Errorf("unsupported type for %s", key)
		}
	}
	
	parseFloat := func(key string) (float64, error) {
		val, err := parseAnyFloat(gponData, key)
		if err != nil && strings.HasPrefix(err.Error(), "missing key") {
			return 0, nil // preserve existing behavior for absent fields in gponData
		}
		return val, err
	}
	parseString := func(v interface{}) string {
		if s, ok := v.(string); ok {
			return s
		}
		return "unknown"
	}

	rawRx, err := parseFloat("RXPower")
	if err != nil {
		return nil, err
	}
	rawTx, err := parseFloat("TXPower")
	if err != nil {
		return nil, err
	}
	temp, err := parseFloat("transceiverTemperature")
	if err != nil {
		return nil, err
	}
	volt, err := parseFloat("supplyVottage")
	if err != nil {
		return nil, err
	}
	bias, err := parseFloat("biasCurrent")
	if err != nil {
		return nil, err
	}

	rxDbm := -40.0
	if rawRx > 0 {
		rxDbm = 10 * math.Log10(rawRx/10000.0)
	}
	txDbm := -40.0
	if rawTx > 0 {
		txDbm = 10 * math.Log10(rawTx/10000.0)
	}

	stats := &GPONStats{
		RxPowerDBm:    math.Round(rxDbm*100) / 100,
		TxPowerDBm:    math.Round(txDbm*100) / 100,
		TemperatureC:  math.Round((temp/256.0)*100) / 100,
		VoltageV:      math.Round((volt/1000.0)*1000) / 1000,
		VoltageMV:     volt,
		BiasCurrentMA: math.Round((bias*2/1000.0)*100) / 100,
		Status:        parseString(gponData["status"]),
		PonType:       parseString(gponData["ponType"]),
		XPonStatus:    parseString(gponData["xponStatus"]),
		RawRxPower:    gponData["RXPower"],
		RawTxPower:    gponData["TXPower"],
	}

	fetchOID := func(payload string) (map[string]interface{}, error) {
		req, err := http.NewRequest("POST", statsURL, bytes.NewBufferString(payload+"\r\n"))
		if err != nil {
			return nil, err
		}
		req.Header.Set("TokenID", tokenID)
		req.Header.Set("Referer", "http://"+cfg.ONU.IP+"/")
		req.Header.Set("User-Agent", "Mozilla/5.0")
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		var res struct {
			Data map[string]interface{} `json:"data"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&res); err != nil {
			return nil, err
		}
		return res.Data, nil
	}

	if devInfo, err := fetchOID(`{"operation":"go","oid":"DEV2_DEV_INFO","data":{"hardwareVersion":"","softwareVersion":"","serialNumber":"","modelName":"","upTime":"","stack":"0,0,0,0,0,0","pstack":"0,0,0,0,0,0"}}`); err == nil {
		if val, ok := devInfo["modelName"]; ok && val != nil {
			stats.ModelName = parseString(val)
		}
		if val, ok := devInfo["serialNumber"]; ok && val != nil {
			stats.SerialNumber = parseString(val)
			if stats.SerialNumber != "" {
				lastKnownSerial = stats.SerialNumber
			}
		}
		if uptime, err := parseAnyFloat(devInfo, "upTime"); err == nil {
			stats.Uptime = uptime
		}
	}

	if stats.SerialNumber == "" && lastKnownSerial != "" {
		stats.SerialNumber = lastKnownSerial
	}

	if procStatus, err := fetchOID(`{"operation":"go","oid":"DEV2_PROC_STATUS","data":{"CPUUsage":"","stack":"0,0,0,0,0,0","pstack":"0,0,0,0,0,0"}}`); err == nil {
		if cpu, err := parseAnyFloat(procStatus, "CPUUsage"); err == nil {
			stats.CPUUsage = cpu
		}
	}

	if memStatus, err := fetchOID(`{"operation":"go","oid":"DEV2_MEM_STATUS","data":{"total":"","free":"","stack":"0,0,0,0,0,0","pstack":"0,0,0,0,0,0"}}`); err == nil {
		total, errTotal := parseAnyFloat(memStatus, "total")
		free, errFree := parseAnyFloat(memStatus, "free")
		if errTotal == nil && errFree == nil && total > 0 {
			stats.MemUsage = math.Round(((total-free)/total)*10000) / 100
		}
	}
	
	return stats, nil
}

func PublishMQTT(stats *GPONStats, cfg *Config) error {
	opts := mqtt.NewClientOptions().AddBroker(fmt.Sprintf("tcp://%s:%d", cfg.MQTT.Broker, cfg.MQTT.Port))
	opts.SetClientID(cfg.MQTT.ClientID)
	if cfg.MQTT.User != "" {
		opts.SetUsername(cfg.MQTT.User)
		opts.SetPassword(cfg.MQTT.Password)
	}

	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		log.Printf("MQTT connection failed: %v", token.Error())
		return fmt.Errorf("connection failed: %w", token.Error())
	}
	defer client.Disconnect(250)

	baseTopic := "homeassistant/sensor/onu_monitor"
	stateTopic := cfg.MQTT.Topic
	if stateTopic == "" {
		stateTopic = baseTopic + "/state"
	}

	type sensorConfig struct {
		Name     string
		Unit     string
		Class    string
		Val      string
		Category string
	}

	sensors := map[string]sensorConfig{
		"rx_power":      {"ONU RX Power", "dBm", "signal_strength", "rx_power_dbm", ""},
		"tx_power":      {"ONU TX Power", "dBm", "signal_strength", "tx_power_dbm", ""},
		"temperature":   {"ONU Temperature", "°C", "temperature", "temperature_c", ""},
		"voltage":       {"ONU Supply Voltage", "mV", "voltage", "voltage_mv", ""},
		"bias_current":  {"ONU Bias Current", "mA", "current", "bias_current_ma", ""},
		"cpu_usage":     {"ONU CPU Usage", "%", "", "cpu_usage", ""},
		"mem_usage":     {"ONU Memory Usage", "%", "", "mem_usage", ""},
		"pon_type":      {"ONU PON Type", "", "", "pon_type", "diagnostic"},
		"xpon_status":   {"ONU xPON Status", "", "", "xpon_status", "diagnostic"},
		"uptime":        {"ONU Uptime", "s", "duration", "uptime", "diagnostic"},
		"model_name":    {"ONU Model Name", "", "", "model_name", "diagnostic"},
		"serial_number": {"ONU Serial Number", "", "", "serial_number", "diagnostic"},
	}

	identity := stats.SerialNumber
	if identity == "" {
		identity = "tp_link_xz000_g7"
	}
	newBaseTopic := fmt.Sprintf("homeassistant/sensor/onu_%s", identity)
	
	// If the stateTopic was implicitly set based on the old baseTopic, we should update it
	// Only if the user didn't override it in config.
	if cfg.MQTT.Topic == "" {
		stateTopic = newBaseTopic + "/state"
	}

	// Account for existing retained discovery records during this migration
	if identity != "tp_link_xz000_g7" {
		oldBaseTopic := "homeassistant/sensor/onu_monitor"
		for key := range sensors {
			oldConfigTopic := fmt.Sprintf("%s/%s/config", oldBaseTopic, key)
			if token := client.Publish(oldConfigTopic, 0, true, []byte("")); token.WaitTimeout(5 * time.Second) {
				if token.Error() != nil {
					log.Printf("Failed to clear old retained config for %s: %v", key, token.Error())
				}
			}
		}
	}

	identifiers := []string{identity}
	model := "XZ000-G7"
	if stats.ModelName != "" {
		model = stats.ModelName
	}
	deviceName := "TP-Link " + model

	for key, info := range sensors {
		configTopic := fmt.Sprintf("%s/%s/config", newBaseTopic, key)
		configPayload := map[string]interface{}{
			"name":                info.Name,
			"state_topic":         stateTopic,
			"value_template":      fmt.Sprintf("{{ value_json.%s }}", info.Val),
			"unique_id":           fmt.Sprintf("tp_link_onu_%s_%s", identity, key),
			"device": map[string]interface{}{
				"identifiers":  identifiers,
				"name":         deviceName,
				"manufacturer": "TP-Link",
				"model":        model,
			},
		}
		if info.Unit != "" {
			configPayload["unit_of_measurement"] = info.Unit
		}
		if info.Class != "" {
			configPayload["device_class"] = info.Class
		}
		if info.Category != "" {
			configPayload["entity_category"] = info.Category
		}
		
		payloadBytes, err := json.Marshal(configPayload)
		if err != nil {
			log.Printf("MQTT marshal config error for %s: %v", key, err)
			continue
		}
		
		if token := client.Publish(configTopic, 0, true, payloadBytes); token.WaitTimeout(5*time.Second) {
			if token.Error() != nil {
				log.Printf("MQTT publish config error: %v", token.Error())
				return fmt.Errorf("publish config error: %w", token.Error())
			}
		} else {
			log.Printf("MQTT publish config timed out")
			return fmt.Errorf("publish config timed out")
		}
	}

	statsBytes, err := json.Marshal(stats)
	if err != nil {
		return fmt.Errorf("MQTT marshal state error: %w", err)
	}
	
	if token := client.Publish(stateTopic, 0, true, statsBytes); token.WaitTimeout(5*time.Second) {
		if token.Error() != nil {
			log.Printf("MQTT publish state error: %v", token.Error())
			return fmt.Errorf("publish state error: %w", token.Error())
		} else {
			log.Printf("Published to MQTT state topic %s", stateTopic)
		}
	} else {
		log.Printf("MQTT publish state timed out")
		return fmt.Errorf("publish state timed out")
	}
	
	return nil
}

func PublishInfluxDB(stats *GPONStats, cfg *Config) error {
	client := influxdb2.NewClient(cfg.INFLUXDB.URL, cfg.INFLUXDB.Token)
	defer client.Close()
	
	writeAPI := client.WriteAPIBlocking(cfg.INFLUXDB.Org, cfg.INFLUXDB.Bucket)
	
	parseRaw := func(v interface{}) int64 {
		switch i := v.(type) {
		case float64: return int64(i)
		case string:
			i = strings.TrimLeft(i, "-")
			f, _ := strconv.ParseInt(i, 10, 64)
			return f
		default: return 0
		}
	}
	
	p := influxdb2.NewPointWithMeasurement("onu_stats").
		AddTag("device", "xz000_g7").
		AddTag("pon_type", stats.PonType).
		AddTag("xpon_status", stats.XPonStatus).
		AddField("temperature_c", stats.TemperatureC).
		AddField("voltage_v", stats.VoltageV).
		AddField("bias_current_ma", stats.BiasCurrentMA).
		AddField("rx_power_dbm", stats.RxPowerDBm).
		AddField("tx_power_dbm", stats.TxPowerDBm).
		AddField("raw_rx_power", parseRaw(stats.RawRxPower)).
		AddField("raw_tx_power", parseRaw(stats.RawTxPower)).
		SetTime(time.Now())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	err := writeAPI.WritePoint(ctx, p)
	if err != nil {
		log.Printf("InfluxDB write error: %v", err)
		return fmt.Errorf("write error: %w", err)
	}
	log.Printf("Written to InfluxDB bucket %s", cfg.INFLUXDB.Bucket)
	
	return nil
}
