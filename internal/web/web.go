package web

import (
	"crypto/subtle"
	"embed"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"

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

func checkAuth(r *http.Request) bool {
	cfg, err := ini.Load(configPath)
	if err != nil {
		return false
	}
	user := cfg.Section("WEBUI").Key("USERNAME").String()
	pass := cfg.Section("WEBUI").Key("PASSWORD").String()

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
		ensureConfig()
		cfg, _ := ini.Load(configPath)
		pass := cfg.Section("WEBUI").Key("PASSWORD").String()

		if pass == "" {
			http.Redirect(w, r, "/setup", http.StatusFound)
			return
		}

		if !checkAuth(r) {
			w.Header().Set("WWW-Authenticate", `Basic realm="Login Required"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func SetupHandler(w http.ResponseWriter, r *http.Request) {
	ensureConfig()
	cfg, _ := ini.Load(configPath)
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
			cfg.SaveTo(configPath)
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
	
	config, _ := scraper.LoadConfig(configPath)
	msgs := getFlash(w, r)
	
	tpls.ExecuteTemplate(w, "index.html", TemplateData{Messages: msgs, Config: config})
}

func SaveHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
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
	cfg.Section("ONU").Key("IP").SetValue(r.FormValue("onu_ip"))
	cfg.Section("ONU").Key("USERNAME").SetValue(r.FormValue("onu_username"))
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
		// Restart service
		cmd := exec.Command("sudo", "/bin/systemctl", "restart", "onu_monitor.timer")
		if err := cmd.Run(); err != nil {
			setFlash(w, "warning", "Saved config, but failed to restart service: "+err.Error())
		} else {
			setFlash(w, "success", "Configuration saved and monitor timer restarted successfully!")
		}
	}

	http.Redirect(w, r, "/", http.StatusFound)
}

func StartServer(port string) {
	http.HandleFunc("/setup", SetupHandler)
	http.HandleFunc("/", authMiddleware(IndexHandler))
	http.HandleFunc("/save", authMiddleware(SaveHandler))
	
	log.Printf("Starting Web GUI on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
