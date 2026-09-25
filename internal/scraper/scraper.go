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
	if config.MQTT.Topic == "" {
		config.MQTT.Topic = "tele/onu/gpon_stats"
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
	
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch keys: %w", err)
	}
	defer resp.Body.Close()
	
	bodyBytes, _ := io.ReadAll(resp.Body)
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
	
	resp, err = client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()
	
	bodyBytes, _ = io.ReadAll(resp.Body)
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
				if resp, err := client.Do(req); err == nil {
					bodyBytes, _ := io.ReadAll(resp.Body)
					resp.Body.Close()
					re := regexp.MustCompile(`var token="([^"]+)";`)
					match := re.FindStringSubmatch(string(bodyBytes))
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
			resp, err := client.Do(logoutReq)
			if err != nil {
				log.Printf("Logout request failed: %v", err)
				return
			}
			defer resp.Body.Close()
			bodyBytes, _ := io.ReadAll(resp.Body)
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
	resp, err = client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("root request failed: %w", err)
	}
	defer resp.Body.Close()
	bodyBytes, _ = io.ReadAll(resp.Body)
	
	re := regexp.MustCompile(`var token="([^"]+)";`)
	match := re.FindStringSubmatch(string(bodyBytes))
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
	
	resp, err = client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch gpon stats: %w", err)
	}
	defer resp.Body.Close()
	
	var statsResp struct {
		Data []map[string]interface{} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&statsResp); err != nil {
		return nil, fmt.Errorf("failed to decode stats: %w", err)
	}

	if len(statsResp.Data) == 0 {
		return nil, fmt.Errorf("no data found in GPON stats response")
	}

	gponData := statsResp.Data[0]
	
	parseFloat := func(v interface{}) float64 {
		switch i := v.(type) {
		case float64: return i
		case string:
			f, _ := strconv.ParseFloat(i, 64)
			return f
		default: return 0
		}
	}
	parseString := func(v interface{}) string {
		if s, ok := v.(string); ok {
			return s
		}
		return "unknown"
	}

	rawRx := parseFloat(gponData["RXPower"])
	rawTx := parseFloat(gponData["TXPower"])
	
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
		TemperatureC:  math.Round((parseFloat(gponData["transceiverTemperature"])/256.0)*100) / 100,
		VoltageV:      math.Round((parseFloat(gponData["supplyVottage"])/1000.0)*1000) / 1000,
		VoltageMV:     parseFloat(gponData["supplyVottage"]),
		BiasCurrentMA: math.Round((parseFloat(gponData["biasCurrent"])*2/1000.0)*100) / 100,
		Status:        parseString(gponData["status"]),
		PonType:       parseString(gponData["ponType"]),
		XPonStatus:    parseString(gponData["xponStatus"]),
		RawRxPower:    gponData["RXPower"],
		RawTxPower:    gponData["TXPower"],
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

	sensors := map[string]map[string]string{
		"rx_power":     {"name": "ONU RX Power", "unit": "dBm", "class": "signal_strength", "val": "rx_power_dbm"},
		"tx_power":     {"name": "ONU TX Power", "unit": "dBm", "class": "signal_strength", "val": "tx_power_dbm"},
		"temperature":  {"name": "ONU Temperature", "unit": "°C", "class": "temperature", "val": "temperature_c"},
		"voltage":      {"name": "ONU Supply Voltage", "unit": "mV", "class": "voltage", "val": "voltage_mv"},
		"bias_current": {"name": "ONU Bias Current", "unit": "mA", "class": "current", "val": "bias_current_ma"},
	}

	for key, info := range sensors {
		configTopic := fmt.Sprintf("%s/%s/config", baseTopic, key)
		configPayload := map[string]interface{}{
			"name":                info["name"],
			"state_topic":         stateTopic,
			"unit_of_measurement": info["unit"],
			"device_class":        info["class"],
			"value_template":      fmt.Sprintf("{{ value_json.%s }}", info["val"]),
			"unique_id":           fmt.Sprintf("tp_link_onu_%s", key),
			"device": map[string]interface{}{
				"identifiers":  []string{"tp_link_xz000_g7"},
				"name":         "TP-Link XZ000-G7 ONU",
				"manufacturer": "TP-Link",
			},
		}
		payloadBytes, _ := json.Marshal(configPayload)
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

	statsBytes, _ := json.Marshal(stats)
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
