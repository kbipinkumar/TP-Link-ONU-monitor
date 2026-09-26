package web

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/subtle"
	"crypto/x509"
	"crypto/x509/pkix"
	"embed"
	"encoding/json"
	"encoding/pem"
	"html/template"
	"math/big"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/ini.v1"
	"github.com/kbipinkumar/TP-Link-ONU-monitor/internal/scraper"
)

//go:embed templates/*
var templateFS embed.FS

var tpls *template.Template

func init() {
	tpls = template.Must(template.ParseFS(templateFS, "templates/*.html"))
}

type FlashMessage struct {
	Category string
	Message  string
}

type TemplateData struct {
	Messages []FlashMessage
	Config   *scraper.Config
	Status   *scraper.SystemStatus
}

var configPath string

func Init(cfgPath string) {
	configPath = cfgPath
}

func getFlash(w http.ResponseWriter, r *http.Request) []FlashMessage {
	cookie, err := r.Cookie("flash")
	if err != nil {
		return nil
	}
	
	http.SetCookie(w, &http.Cookie{
		Name:   "flash",
		Value:  "",
		MaxAge: -1,
		Path:   "/",
	})

	// Simple decode (cookie value format: category|message)
	// For multiple, we could use JSON, but for this app it's usually 1 message.
	parts := strings.SplitN(cookie.Value, "|", 2)
	if len(parts) == 2 {
		return []FlashMessage{{Category: parts[0], Message: parts[1]}}
	}
	return nil
}

func setFlash(w http.ResponseWriter, category, message string) {
	http.SetCookie(w, &http.Cookie{
		Name:  "flash",
		Value: category + "|" + message,
		Path:  "/",
	})
}

// Ensure config file exists
func ensureConfig() error {
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		file, err := os.Create(configPath)
		if err != nil {
			return err
		}
		file.Close()
	}
	return nil
}

func ensureTLSCert(certPath, keyPath string) error {
	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			return nil // Certs exist
		}
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	var ips []net.IP
	ips = append(ips, net.ParseIP("127.0.0.1"), net.ParseIP("::1"))
	
	ifaces, _ := net.Interfaces()
	for _, i := range ifaces {
		addrs, _ := i.Addrs()
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil {
				ips = append(ips, ip)
			}
		}
	}

	certTemplate := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"ONU Monitor"},
		},
		IPAddresses:           ips,
		DNSNames:              []string{"localhost"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, &certTemplate, &certTemplate, &priv.PublicKey, priv)
	if err != nil {
		return err
	}
	certOut, err := os.Create(certPath)
	if err != nil {
		return err
	}
	defer certOut.Close()
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		return err
	}
	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer keyOut.Close()
	privBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return err
	}
	if err := pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: privBytes}); err != nil {
		return err
	}
	return nil
}

func checkAuth(r *http.Request, user, pass string) bool {
	if user == "" || pass == "" {
		return false
	}

	u, p, ok := r.BasicAuth()
	if !ok {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(u), []byte(user)) == 1 &&
		subtle.ConstantTimeCompare([]byte(p), []byte(pass)) == 1
}

func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := ensureConfig(); err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		cfg, err := ini.Load(configPath)
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		user := cfg.Section("WEBUI").Key("USERNAME").String()
		pass := cfg.Section("WEBUI").Key("PASSWORD").String()

		if pass == "" {
			http.Redirect(w, r, "/setup", http.StatusFound)
			return
		}

		if !checkAuth(r, user, pass) {
			w.Header().Set("WWW-Authenticate", `Basic realm="Login Required"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func SetupHandler(w http.ResponseWriter, r *http.Request) {
	if err := ensureConfig(); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	cfg, err := ini.Load(configPath)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	pass := cfg.Section("WEBUI").Key("PASSWORD").String()

	if pass != "" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	if r.Method == http.MethodPost {
		r.ParseForm()
		username := r.FormValue("admin_username")
		password := r.FormValue("admin_password")

		if username != "" && password != "" {
			cfg.Section("WEBUI").Key("USERNAME").SetValue(username)
			cfg.Section("WEBUI").Key("PASSWORD").SetValue(password)
			if err := cfg.SaveTo(configPath); err != nil {
				setFlash(w, "warning", "Failed to save configuration.")
				http.Redirect(w, r, "/setup", http.StatusFound)
				return
			}
			setFlash(w, "success", "Web GUI secured successfully! Please login.")
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		setFlash(w, "warning", "Username and password are required.")
		http.Redirect(w, r, "/setup", http.StatusFound)
		return
	}

	msgs := getFlash(w, r)
	tpls.ExecuteTemplate(w, "setup.html", TemplateData{Messages: msgs})
}

func IndexHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	
	config, err := scraper.LoadConfig(configPath)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	statusPath := filepath.Join(filepath.Dir(configPath), "status.json")
	status, err := scraper.ReadStatus(statusPath)
	if err != nil {
		status = nil
	}
	msgs := getFlash(w, r)
	
	tpls.ExecuteTemplate(w, "index.html", TemplateData{
		Messages: msgs,
		Config:   config,
		Status:   status,
	})
}

func TestConnectionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// CSRF / Same-Origin validation
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		http.Error(w, "Forbidden: Missing Origin/Referer header", http.StatusForbidden)
		return
	}
	originURL, err := url.Parse(origin)
	if err != nil || originURL.Host != r.Host {
		http.Error(w, "Forbidden: Cross-Origin Request", http.StatusForbidden)
		return
	}

	r.ParseForm()
	
	ip := r.FormValue("onu_ip")
	user := r.FormValue("onu_username")
	pass := r.FormValue("onu_password")
	
	if pass == "" {
		cfg, err := ini.Load(configPath)
		if err == nil {
			pass = cfg.Section("ONU").Key("PASSWORD").String()
		}
	}
	
	testCfg := &scraper.Config{}
	testCfg.ONU.IP = ip
	testCfg.ONU.Username = user
	testCfg.ONU.Password = pass
	
	stats, err := scraper.GetGPONStats(testCfg)
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}
	
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"stats":   stats,
	})
}

func SaveHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	// CSRF / Same-Origin validation
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		http.Error(w, "Forbidden: Missing Origin/Referer header", http.StatusForbidden)
		return
	}
	originURL, err := url.Parse(origin)
	if err != nil || originURL.Host != r.Host {
		http.Error(w, "Forbidden: Cross-Origin Request", http.StatusForbidden)
		return
	}

	r.ParseForm()
	
	cfg, err := ini.Load(configPath)
	if err != nil {
		setFlash(w, "warning", "Failed to load config file")
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	// ONU
	onuIP := r.FormValue("onu_ip")
	onuUsername := r.FormValue("onu_username")
	onuPassword := r.FormValue("onu_password")
	if onuPassword == "" {
		onuPassword = cfg.Section("ONU").Key("PASSWORD").String()
	}

	testCfg := &scraper.Config{}
	testCfg.ONU.IP = onuIP
	testCfg.ONU.Username = onuUsername
	testCfg.ONU.Password = onuPassword

	if _, err := scraper.GetGPONStats(testCfg); err != nil {
		setFlash(w, "warning", "Test connection failed: " + err.Error() + ". Configuration NOT saved.")
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	cfg.Section("ONU").Key("IP").SetValue(onuIP)
	cfg.Section("ONU").Key("USERNAME").SetValue(onuUsername)
	if p := r.FormValue("onu_password"); p != "" {
		cfg.Section("ONU").Key("PASSWORD").SetValue(p)
	}

	// MQTT
	enableMqtt := "False"
	if r.FormValue("mqtt_enable") != "" {
		enableMqtt = "True"
	}
	cfg.Section("MQTT").Key("ENABLE").SetValue(enableMqtt)
	cfg.Section("MQTT").Key("BROKER").SetValue(r.FormValue("mqtt_broker"))
	cfg.Section("MQTT").Key("PORT").SetValue(r.FormValue("mqtt_port"))
	cfg.Section("MQTT").Key("USER").SetValue(r.FormValue("mqtt_user"))
	if p := r.FormValue("mqtt_password"); p != "" {
		cfg.Section("MQTT").Key("PASSWORD").SetValue(p)
	}
	cfg.Section("MQTT").Key("TOPIC").SetValue(r.FormValue("mqtt_topic"))
	cfg.Section("MQTT").Key("CLIENT_ID").SetValue(r.FormValue("mqtt_client_id"))

	// INFLUXDB
	enableInflux := "False"
	if r.FormValue("influx_enable") != "" {
		enableInflux = "True"
	}
	cfg.Section("INFLUXDB").Key("ENABLE").SetValue(enableInflux)
	cfg.Section("INFLUXDB").Key("URL").SetValue(r.FormValue("influx_url"))
	if p := r.FormValue("influx_token"); p != "" {
		cfg.Section("INFLUXDB").Key("TOKEN").SetValue(p)
	}
	cfg.Section("INFLUXDB").Key("ORG").SetValue(r.FormValue("influx_org"))
	cfg.Section("INFLUXDB").Key("BUCKET").SetValue(r.FormValue("influx_bucket"))

	if err := cfg.SaveTo(configPath); err != nil {
		setFlash(w, "warning", "Failed to save config: "+err.Error())
	} else {
		// Attempt to restart service with a bounded wait
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		
		cmd := exec.CommandContext(ctx, "sudo", "/bin/systemctl", "restart", "onu_monitor.timer")
		err := cmd.Run()
		
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				log.Printf("Restarting onu_monitor.timer is taking longer than expected; continuing in background.")
				setFlash(w, "success", "Configuration saved. Monitor timer restart requested but not confirmed.")
			} else {
				log.Printf("Failed to restart onu_monitor.timer: %v", err)
				setFlash(w, "warning", "Configuration saved, but failed to restart monitor timer: "+err.Error())
			}
		} else {
			setFlash(w, "success", "Configuration saved and monitor timer restarted successfully!")
		}
	}

	http.Redirect(w, r, "/", http.StatusFound)
}

func UpdateWebUIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	// CSRF / Same-Origin validation
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		http.Error(w, "Forbidden: Missing Origin/Referer header", http.StatusForbidden)
		return
	}
	originURL, err := url.Parse(origin)
	if err != nil || originURL.Host != r.Host {
		http.Error(w, "Forbidden: Cross-Origin Request", http.StatusForbidden)
		return
	}

	r.ParseForm()
	
	cfg, err := ini.Load(configPath)
	if err != nil {
		setFlash(w, "warning", "Failed to load config file")
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	username := r.FormValue("webui_username")
	password := r.FormValue("webui_password")

	if username != "" && password != "" {
		cfg.Section("WEBUI").Key("USERNAME").SetValue(username)
		cfg.Section("WEBUI").Key("PASSWORD").SetValue(password)
		if err := cfg.SaveTo(configPath); err != nil {
			setFlash(w, "warning", "Failed to save configuration.")
		} else {
			setFlash(w, "success", "Web GUI credentials updated! Please log in again.")
			// Invalidate current Basic Auth session by sending a 401
			w.Header().Set("WWW-Authenticate", `Basic realm="Login Required"`)
			http.Error(w, "Credentials updated. Please log in again.", http.StatusUnauthorized)
			return
		}
	} else {
		setFlash(w, "warning", "Username and password cannot be empty.")
	}

	http.Redirect(w, r, "/", http.StatusFound)
}

func StartServer(port string) {
	http.HandleFunc("/setup", SetupHandler)
	http.HandleFunc("/", authMiddleware(IndexHandler))
	http.HandleFunc("/save", authMiddleware(SaveHandler))
	http.HandleFunc("/test-connection", authMiddleware(TestConnectionHandler))
	http.HandleFunc("/update-webui", authMiddleware(UpdateWebUIHandler))
	
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      nil,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	certPath := filepath.Join(filepath.Dir(configPath), "cert.pem")
	keyPath := filepath.Join(filepath.Dir(configPath), "key.pem")
	if err := ensureTLSCert(certPath, keyPath); err != nil {
		log.Fatalf("Failed to generate TLS certs: %v", err)
	}

	// Start HTTP redirect server on port-1
	httpPort := 8990
	if p, err := strconv.Atoi(port); err == nil {
		httpPort = p - 1
	}
	
	go func() {
		redirectSrv := &http.Server{
			Addr:              ":" + strconv.Itoa(httpPort),
			ReadHeaderTimeout: srv.ReadTimeout,
			IdleTimeout:       srv.IdleTimeout,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				host, _, err := net.SplitHostPort(r.Host)
				if err != nil {
					host = r.Host
					if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
						host = host[1 : len(host)-1]
					}
				}
				target := "https://" + net.JoinHostPort(host, port) + r.RequestURI
				http.Redirect(w, r, target, http.StatusMovedPermanently)
			}),
		}
		log.Printf("Starting HTTP-to-HTTPS redirect server on http://0.0.0.0:%d", httpPort)
		if err := redirectSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP Redirect server failed: %v", err)
		}
	}()

	log.Printf("Starting secure Web GUI on https://0.0.0.0:%s", port)
	if err := srv.ListenAndServeTLS(certPath, keyPath); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
