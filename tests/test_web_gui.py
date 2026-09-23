import importlib.util
import os
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

CONFIG_DIR = tempfile.mkdtemp(prefix='onu-web-gui-')
CONFIG_PATH = os.path.join(CONFIG_DIR, 'onu_config.ini')
os.environ['ONU_CONFIG_FILE'] = CONFIG_PATH
os.environ['ONU_WEB_USER'] = 'admin'
os.environ['ONU_WEB_PASSWORD'] = 'test-secret'

_SPEC = importlib.util.spec_from_file_location(
    'onu_web_gui',
    Path(__file__).resolve().parents[1] / 'web_gui' / 'web_gui.py',
)
gui = importlib.util.module_from_spec(_SPEC)
sys.modules[_SPEC.name] = gui
_SPEC.loader.exec_module(gui)

STORED_ONU_PASSWORD = 'router-secret'
STORED_MQTT_PASSWORD = 'mqtt-secret'
STORED_INFLUX_TOKEN = 'influx-token-value'


def _write_config():
    Path(CONFIG_PATH).write_text(
        "\n".join([
            "[ONU]",
            "IP = 192.168.1.1",
            "USERNAME = user",
            f"PASSWORD = {STORED_ONU_PASSWORD}",
            "",
            "[MQTT]",
            "ENABLE = True",
            "BROKER = 127.0.0.1",
            "PORT = 1883",
            "USER = mqttuser",
            f"PASSWORD = {STORED_MQTT_PASSWORD}",
            "TOPIC = tele/onu/gpon_stats",
            "CLIENT_ID = onu_monitor",
            "",
            "[INFLUXDB]",
            "ENABLE = False",
            "URL = http://127.0.0.1:8086",
            f"TOKEN = {STORED_INFLUX_TOKEN}",
            "ORG = home",
            "BUCKET = network_stats",
            "",
        ]),
        encoding='utf-8',
    )


class WebGuiSecurityTests(unittest.TestCase):
    def setUp(self):
        os.environ['ONU_WEB_USER'] = 'admin'
        os.environ['ONU_WEB_PASSWORD'] = 'test-secret'
        _write_config()
        self.client = gui.app.test_client()

    def test_bind_is_loopback_only(self):
        self.assertEqual(gui.BIND_HOST, '127.0.0.1')

    def test_missing_credentials_refuse_the_gui(self):
        os.environ['ONU_WEB_PASSWORD'] = ''
        response = self.client.get('/')
        self.assertEqual(response.status_code, 503)

    def test_basic_auth_is_required(self):
        anonymous = self.client.get('/')
        self.assertEqual(anonymous.status_code, 401)
        self.assertIn('Basic', anonymous.headers['WWW-Authenticate'])

        wrong = self.client.get('/', auth=('admin', 'nope'))
        self.assertEqual(wrong.status_code, 401)

    def test_page_does_not_echo_stored_secrets(self):
        response = self.client.get('/', auth=('admin', 'test-secret'))
        self.assertEqual(response.status_code, 200)
        body = response.get_data(as_text=True)
        self.assertNotIn(STORED_ONU_PASSWORD, body)
        self.assertNotIn(STORED_MQTT_PASSWORD, body)
        self.assertNotIn(STORED_INFLUX_TOKEN, body)
        self.assertEqual(body.count('placeholder="leave blank to keep unchanged"'), 3)
        self.assertIn('value=""', body)

    def _auth_post(self, **data):
        with self.client.session_transaction() as sess:
            sess['csrf_token'] = 'form-token'
        data['csrf_token'] = 'form-token'
        return self.client.post('/save', data=data, auth=('admin', 'test-secret'))

    def test_save_without_csrf_token_does_not_write(self):
        with mock.patch.object(gui, 'restart_monitor_timer') as restart:
            response = self.client.post('/save', data={
                'onu_ip': '10.9.9.9',
                'onu_username': 'attacker',
                'onu_password': 'stolen-overwrite',
            }, auth=('admin', 'test-secret'))
        self.assertEqual(response.status_code, 302)
        restart.assert_not_called()
        saved = gui._load_config()
        self.assertEqual(saved.get('ONU', 'IP'), '192.168.1.1')
        self.assertEqual(saved.get('ONU', 'PASSWORD'), STORED_ONU_PASSWORD)

    def test_blank_secret_fields_keep_stored_values(self):
        with mock.patch.object(gui, 'restart_monitor_timer') as restart:
            response = self._auth_post(
                onu_ip='10.0.0.2',
                onu_username='admin',
                onu_password='',
                mqtt_enable='on',
                mqtt_broker='10.0.0.3',
                mqtt_port='1883',
                mqtt_user='mqttuser',
                mqtt_password='',
                mqtt_topic='tele/onu/gpon_stats',
                mqtt_client_id='onu_monitor',
                influx_url='http://127.0.0.1:8086',
                influx_token='',
                influx_org='home',
                influx_bucket='network_stats',
            )

        self.assertEqual(response.status_code, 302)
        restart.assert_called_once_with()
        saved = gui._load_config()
        self.assertEqual(saved.get('ONU', 'IP'), '10.0.0.2')
        self.assertEqual(saved.get('ONU', 'PASSWORD'), STORED_ONU_PASSWORD)
        self.assertEqual(saved.get('MQTT', 'PASSWORD'), STORED_MQTT_PASSWORD)
        self.assertEqual(saved.get('INFLUXDB', 'TOKEN'), STORED_INFLUX_TOKEN)
        self.assertEqual(os.stat(CONFIG_PATH).st_mode & 0o777, 0o640)

    def test_new_secret_fields_replace_stored_values(self):
        with mock.patch.object(gui, 'restart_monitor_timer'):
            response = self._auth_post(
                onu_ip='192.168.1.1',
                onu_username='user',
                onu_password='new-router-secret',
                mqtt_broker='127.0.0.1',
                mqtt_port='1883',
                mqtt_user='mqttuser',
                mqtt_password='new-mqtt-secret',
                mqtt_topic='tele/onu/gpon_stats',
                mqtt_client_id='onu_monitor',
                influx_url='http://127.0.0.1:8086',
                influx_token='new-influx-token',
                influx_org='home',
                influx_bucket='network_stats',
            )

        self.assertEqual(response.status_code, 302)
        saved = gui._load_config()
        self.assertEqual(saved.get('ONU', 'PASSWORD'), 'new-router-secret')
        self.assertEqual(saved.get('MQTT', 'PASSWORD'), 'new-mqtt-secret')
        self.assertEqual(saved.get('INFLUXDB', 'TOKEN'), 'new-influx-token')

    def test_timer_restart_uses_narrow_sudo_when_not_root(self):
        with mock.patch.object(gui.os, 'geteuid', return_value=1000), \
                mock.patch.object(gui.subprocess, 'run') as run:
            gui.restart_monitor_timer()
        run.assert_called_once_with(
            ['/usr/bin/sudo', '-n', '/usr/bin/systemctl', 'restart', 'onu_monitor.timer'],
            check=True,
        )

    def test_timer_restart_skips_sudo_for_root(self):
        with mock.patch.object(gui.os, 'geteuid', return_value=0), \
                mock.patch.object(gui.subprocess, 'run') as run:
            gui.restart_monitor_timer()
        run.assert_called_once_with(
            ['/usr/bin/systemctl', 'restart', 'onu_monitor.timer'],
            check=True,
        )


if __name__ == '__main__':
    unittest.main()
