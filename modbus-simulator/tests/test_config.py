import copy
import tempfile
import unittest
from pathlib import Path
import yaml
from modbus_simulator.config import load_config, validate_device
from modbus_simulator.pty import Pty

ROOT = Path(__file__).resolve().parents[1]


class ConfigTest(unittest.TestCase):
    def test_default_mapping(self):
        config = load_config(ROOT / 'config/simulator.yaml')
        self.assertEqual(len(config['channels']), 6)
        self.assertEqual([d['slave_id'] for d in config['channels'][0]['devices']], [1, 2])
        rtc = next(channel for channel in config['channels'] if channel['name'] == 'rtc0')
        self.assertEqual(rtc['alias'], '/tmp/modbus-rtc0')
        self.assertEqual([d['slave_id'] for d in rtc['devices']], [3])
        time_device = rtc['devices'][0]
        self.assertEqual(time_device['name'], '时间寄存器测试设备-03')
        self.assertEqual(time_device.get('fixture'), 'rtc_clock')
        self.assertEqual(
            [(register['area'], register['address'], register['value'])
             for register in time_device['registers']],
            [('holding_register', 100, 0),
             ('holding_register', 101, 0),
             ('holding_register', 102, 0)],
        )

    def test_invalid_device(self):
        base = load_config(ROOT / 'config/simulator.yaml')['channels'][0]['devices'][0]
        for change in [{'slave_id': 0}, {'slave_id': 1.5}, {'registers': []},
                       {'behavior': {'response_delay_ms': -1}}, {'behavior': {'drop_rate': 1}}]:
            with self.subTest(change=change), self.assertRaises(ValueError):
                validate_device(base | change)
        for change in [{'address': 65536}, {'scale': 0}, {'length': 2},
                       {'byte_order': 'oops'}, {'simulation': {'type': 'increment', 'interval': 0}}]:
            device = copy.deepcopy(base)
            device['registers'][0].update(change)
            with self.subTest(change=change), self.assertRaises(ValueError):
                validate_device(device)
        duplicate = copy.deepcopy(base)
        duplicate['registers'][1]['address'] = 0
        with self.assertRaises(ValueError):
            validate_device(duplicate)

    def test_duplicate_ids_and_bad_yaml(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            device = load_config(ROOT / 'config/simulator.yaml')['channels'][0]['devices'][0]
            (root / 'device.yaml').write_text(yaml.safe_dump(device))
            (root / 'main.yaml').write_text(yaml.safe_dump({'channels': [dict(
                name='a', protocol='tcp', port=1502, devices=['device.yaml', 'device.yaml'])]}))
            with self.assertRaisesRegex(ValueError, '重复 Slave'):
                load_config(root / 'main.yaml')
            (root / 'main.yaml').write_text('invalid: [')
            with self.assertRaises(ValueError):
                load_config(root / 'main.yaml')

    def test_pty_ownership(self):
        with tempfile.TemporaryDirectory() as directory:
            alias = Path(directory) / 'serial'
            alias.write_text('keep')
            with self.assertRaises(ValueError):
                Pty(alias).open()
            self.assertEqual(alias.read_text(), 'keep')
            alias.unlink()
            alias.symlink_to('/dev/pts/nonexistent')
            first = Pty(alias).open()
            try:
                self.assertEqual(str(alias.resolve()), first.slave_name)
                with self.assertRaises(BlockingIOError):
                    Pty(alias).open()
                self.assertTrue(alias.exists())
            finally:
                first.close()
            self.assertFalse(alias.is_symlink())
            second = Pty(alias).open()
            second.close()
