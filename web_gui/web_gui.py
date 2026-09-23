from flask import Flask, render_template, request, flash, redirect, url_for
import configparser
import os
import subprocess

app = Flask(__name__)
app.secret_key = os.urandom(24)

CONFIG_FILE = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'onu_config.ini')

@app.route('/', methods=['GET'])
def index():
    config = configparser.ConfigParser()
    if not os.path.exists(CONFIG_FILE):
        open(CONFIG_FILE, 'a').close()
    config.read(CONFIG_FILE)
    return render_template('index.html', config=config)

@app.route('/save', methods=['POST'])
def save():
    config = configparser.ConfigParser()
    config.read(CONFIG_FILE)
    
    if 'ONU' not in config: config['ONU'] = {}
    config['ONU']['IP'] = request.form.get('onu_ip', '')
    config['ONU']['USERNAME'] = request.form.get('onu_username', '')
    config['ONU']['PASSWORD'] = request.form.get('onu_password', '')
    
    if 'MQTT' not in config: config['MQTT'] = {}
    config['MQTT']['ENABLE'] = 'True' if request.form.get('mqtt_enable') else 'False'
    config['MQTT']['BROKER'] = request.form.get('mqtt_broker', '')
    config['MQTT']['PORT'] = request.form.get('mqtt_port', '1883')
    config['MQTT']['USER'] = request.form.get('mqtt_user', '')
    config['MQTT']['PASSWORD'] = request.form.get('mqtt_password', '')
    config['MQTT']['TOPIC'] = request.form.get('mqtt_topic', 'tele/onu/gpon_stats')
    config['MQTT']['CLIENT_ID'] = request.form.get('mqtt_client_id', 'onu_monitor')
    
    if 'INFLUXDB' not in config: config['INFLUXDB'] = {}
    config['INFLUXDB']['ENABLE'] = 'True' if request.form.get('influx_enable') else 'False'
    config['INFLUXDB']['URL'] = request.form.get('influx_url', '')
    config['INFLUXDB']['TOKEN'] = request.form.get('influx_token', '')
    config['INFLUXDB']['ORG'] = request.form.get('influx_org', '')
    config['INFLUXDB']['BUCKET'] = request.form.get('influx_bucket', '')

    with open(CONFIG_FILE, 'w') as configfile:
        config.write(configfile)
    
    try:
        subprocess.run(['systemctl', 'restart', 'onu_monitor.timer'], check=True)
        flash('Configuration saved and monitor timer restarted successfully!', 'success')
    except Exception as e:
        flash(f'Saved config, but failed to restart service: {e}. Are you running as root?', 'warning')

    return redirect(url_for('index'))

if __name__ == '__main__':
    app.run(host='0.0.0.0', port=8991)
