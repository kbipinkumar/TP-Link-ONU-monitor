from flask import Flask, render_template, request, flash, redirect, url_for, Response, session
import configparser
import hmac
import os
import secrets
import subprocess
from functools import wraps

app = Flask(__name__)
app.secret_key = os.urandom(24)
app.config.update(
    SESSION_COOKIE_HTTPONLY=True,
    SESSION_COOKIE_SAMESITE='Strict',
)

# The monitor runs on a headless host and is configured from another device
# on the same LAN. Listen on every interface; HTTP Basic auth gates access.
BIND_HOST = '0.0.0.0'
BIND_PORT = 8991

CONFIG_FILE = os.environ.get(
    'ONU_CONFIG_FILE',
    os.path.join(os.path.dirname(os.path.abspath(__file__)), 'onu_config.ini'),
)

# GUI login is a shared secret injected by systemd from
# /etc/onu-monitor/web.env (root:root, mode 600). It is not stored in
# onu_config.ini and is never written back into the page.
WEB_USER_ENV = 'ONU_WEB_USER'
WEB_PASSWORD_ENV = 'ONU_WEB_PASSWORD'

SECRET_FIELDS = (
    ('ONU', 'PASSWORD', 'onu_password'),
    ('MQTT', 'PASSWORD', 'mqtt_password'),
    ('INFLUXDB', 'TOKEN', 'influx_token'),
)


def _constant_time_equals(left, right):
    left_bytes = left.encode('utf-8')
    right_bytes = right.encode('utf-8')
    if len(left_bytes) != len(right_bytes):
        hmac.compare_digest(left_bytes, left_bytes)
        return False
    return hmac.compare_digest(left_bytes, right_bytes)


def _web_credentials():
    return os.environ.get(WEB_USER_ENV, ''), os.environ.get(WEB_PASSWORD_ENV, '')


def requires_auth(view):
    @wraps(view)
    def wrapped(*args, **kwargs):
        username, password = _web_credentials()
        if not username or not password:
            return Response(
                'Web GUI authentication is not configured. '
                'Set ONU_WEB_USER and ONU_WEB_PASSWORD (see /etc/onu-monitor/web.env).',
                status=503,
                mimetype='text/plain',
            )
        auth = request.authorization
        if (
            auth is None
            or auth.type != 'basic'
            or auth.username is None
            or auth.password is None
            or not _constant_time_equals(auth.username, username)
            or not _constant_time_equals(auth.password, password)
        ):
            return Response(
                'Authentication required',
                401,
                {'WWW-Authenticate': 'Basic realm="ONU Monitor"'},
                mimetype='text/plain',
            )
        return view(*args, **kwargs)

    return wrapped


def _load_config():
    config = configparser.ConfigParser()
    if not os.path.exists(CONFIG_FILE):
        try:
            open(CONFIG_FILE, 'a').close()
            os.chmod(CONFIG_FILE, 0o640)
        except OSError:
            return config
    config.read(CONFIG_FILE)
    return config


def _apply_secret(config, section, key, form_value):
    """Keep the stored secret when the form field is left blank."""
    if form_value != '':
        config[section][key] = form_value
    elif key not in config[section]:
        config[section][key] = ''


def restart_monitor_timer():
    command = ['/usr/bin/systemctl', 'restart', 'onu_monitor.timer']
    if hasattr(os, 'geteuid') and os.geteuid() != 0:
        command = ['/usr/bin/sudo', '-n', *command]
    subprocess.run(command, check=True)


def _csrf_token():
    token = session.get('csrf_token')
    if not token:
        token = secrets.token_urlsafe(32)
        session['csrf_token'] = token
    return token


def _csrf_ok():
    expected = session.get('csrf_token', '')
    supplied = request.form.get('csrf_token', '')
    if not expected or not supplied:
        return False
    return _constant_time_equals(supplied, expected)


def _config_for_template(config):
    """Drop stored secrets so the page cannot echo them back."""
    public = configparser.ConfigParser()
    public.read_dict({section: dict(config[section]) for section in config.sections()})
    for section, key, _form_key in SECRET_FIELDS:
        if public.has_option(section, key):
            public.remove_option(section, key)
    return public


@app.route('/', methods=['GET'])
@requires_auth
def index():
    config = _config_for_template(_load_config())
    return render_template('index.html', config=config, csrf_token=_csrf_token())


@app.route('/save', methods=['POST'])
@requires_auth
def save():
    if not _csrf_ok():
        flash('Rejected the save: reload the page and try again.', 'warning')
        return redirect(url_for('index'))

    config = _load_config()

    if 'ONU' not in config:
        config['ONU'] = {}
    config['ONU']['IP'] = request.form.get('onu_ip', '')
    config['ONU']['USERNAME'] = request.form.get('onu_username', '')

    if 'MQTT' not in config:
        config['MQTT'] = {}
    config['MQTT']['ENABLE'] = 'True' if request.form.get('mqtt_enable') else 'False'
    config['MQTT']['BROKER'] = request.form.get('mqtt_broker', '')
    config['MQTT']['PORT'] = request.form.get('mqtt_port', '1883')
    config['MQTT']['USER'] = request.form.get('mqtt_user', '')
    config['MQTT']['TOPIC'] = request.form.get('mqtt_topic', 'tele/onu/gpon_stats')
    config['MQTT']['CLIENT_ID'] = request.form.get('mqtt_client_id', 'onu_monitor')

    if 'INFLUXDB' not in config:
        config['INFLUXDB'] = {}
    config['INFLUXDB']['ENABLE'] = 'True' if request.form.get('influx_enable') else 'False'
    config['INFLUXDB']['URL'] = request.form.get('influx_url', '')
    config['INFLUXDB']['ORG'] = request.form.get('influx_org', '')
    config['INFLUXDB']['BUCKET'] = request.form.get('influx_bucket', '')

    for section, key, form_key in SECRET_FIELDS:
        _apply_secret(config, section, key, request.form.get(form_key, ''))

    with open(CONFIG_FILE, 'w') as configfile:
        config.write(configfile)
    try:
        os.chmod(CONFIG_FILE, 0o640)
    except OSError:
        pass

    try:
        restart_monitor_timer()
        flash('Configuration saved and monitor timer restarted successfully!', 'success')
    except Exception as e:
        flash(
            f'Saved config, but failed to restart monitor timer: {e}. '
            'The service user needs the packaged sudoers rule for '
            '"systemctl restart onu_monitor.timer".',
            'warning',
        )

    return redirect(url_for('index'))


if __name__ == '__main__':
    app.run(host=BIND_HOST, port=BIND_PORT)
