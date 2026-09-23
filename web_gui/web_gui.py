from flask import Flask, render_template, request, flash, redirect, url_for, Response
from functools import wraps
import configparser
import os
import subprocess

app = Flask(__name__)
app.secret_key = os.urandom(24)

CONFIG_FILE = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'onu_config.ini')

def check_auth(username, password):
    config = configparser.ConfigParser()
    config.read(CONFIG_FILE)
    stored_user = config.get('WEBUI', 'USERNAME', fallback=None)
    stored_pass = config.get('WEBUI', 'PASSWORD', fallback=None)
    return username == stored_user and password == stored_pass

def authenticate():
    return Response(
    'Could not verify your access level for that URL.\n'
    'You have to login with proper credentials', 401,
    {'WWW-Authenticate': 'Basic realm="Login Required"'})

def requires_auth(f):
    @wraps(f)
    def decorated(*args, **kwargs):
        config = configparser.ConfigParser()
        config.read(CONFIG_FILE)
        if not config.has_section('WEBUI') or not config.get('WEBUI', 'PASSWORD', fallback=''):
            return redirect(url_for('setup'))
            
        auth = request.authorization
        if not auth or not check_auth(auth.username, auth.password):
            return authenticate()
        return f(*args, **kwargs)
    return decorated

@app.route('/setup', methods=['GET', 'POST'])
def setup():
    config = configparser.ConfigParser()
    if not os.path.exists(CONFIG_FILE):
        open(CONFIG_FILE, 'a').close()
    config.read(CONFIG_FILE)
    
    if config.has_section('WEBUI') and config.get('WEBUI', 'PASSWORD', fallback=''):
        return redirect(url_for('index'))
        
    if request.method == 'POST':
        username = request.form.get('admin_username')
        password = request.form.get('admin_password')
        if username and password:
            if 'WEBUI' not in config: config['WEBUI'] = {}
            config['WEBUI']['USERNAME'] = username
            config['WEBUI']['PASSWORD'] = password
            with open(CONFIG_FILE, 'w') as configfile:
                config.write(configfile)
            flash('Web GUI secured successfully! Please login.', 'success')
            return redirect(url_for('index'))
        flash('Username and password are required.', 'warning')
        
    return render_template('setup.html')

@app.route('/', methods=['GET'])
@requires_auth
def index():
    config = configparser.ConfigParser()
    if not os.path.exists(CONFIG_FILE):
        open(CONFIG_FILE, 'a').close()
    config.read(CONFIG_FILE)
    return render_template('index.html', config=config)

@app.route('/save', methods=['POST'])
@requires_auth
def save():
    config = configparser.ConfigParser()
    config.read(CONFIG_FILE)
    
    if 'ONU' not in config: config['ONU'] = {}
    config['ONU']['IP'] = request.form.get('onu_ip', '')
    config['ONU']['USERNAME'] = request.form.get('onu_username', '')
    onu_pass = request.form.get('onu_password')
    if onu_pass:
        config['ONU']['PASSWORD'] = onu_pass
    
    if 'MQTT' not in config: config['MQTT'] = {}
    config['MQTT']['ENABLE'] = 'True' if request.form.get('mqtt_enable') else 'False'
    config['MQTT']['BROKER'] = request.form.get('mqtt_broker', '')
    config['MQTT']['PORT'] = request.form.get('mqtt_port', '1883')
    config['MQTT']['USER'] = request.form.get('mqtt_user', '')
    mqtt_pass = request.form.get('mqtt_password')
    if mqtt_pass:
        config['MQTT']['PASSWORD'] = mqtt_pass
    config['MQTT']['TOPIC'] = request.form.get('mqtt_topic', 'tele/onu/gpon_stats')
    config['MQTT']['CLIENT_ID'] = request.form.get('mqtt_client_id', 'onu_monitor')
    
    if 'INFLUXDB' not in config: config['INFLUXDB'] = {}
    config['INFLUXDB']['ENABLE'] = 'True' if request.form.get('influx_enable') else 'False'
    config['INFLUXDB']['URL'] = request.form.get('influx_url', '')
    influx_tok = request.form.get('influx_token')
    if influx_tok:
        config['INFLUXDB']['TOKEN'] = influx_tok
    config['INFLUXDB']['ORG'] = request.form.get('influx_org', '')
    config['INFLUXDB']['BUCKET'] = request.form.get('influx_bucket', '')

    with open(CONFIG_FILE, 'w') as configfile:
        config.write(configfile)
    
    try:
        subprocess.run(['sudo', '/bin/systemctl', 'restart', 'onu_monitor.timer'], check=True)
        flash('Configuration saved and monitor timer restarted successfully!', 'success')
    except Exception as e:
        flash(f'Saved config, but failed to restart service: {e}. Are you running as root?', 'warning')

    return redirect(url_for('index'))

if __name__ == '__main__':
    app.run(host='0.0.0.0', port=8991)
