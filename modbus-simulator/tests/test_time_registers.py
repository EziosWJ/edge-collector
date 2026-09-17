import unittest
from pathlib import Path

from modbus_simulator.config import load_config
from modbus_simulator.datastore import make_datastore

ROOT = Path(__file__).resolve().parents[1]


class TimeRegisterDeviceTest(unittest.IsolatedAsyncioTestCase):
    async def test_unit3_holding_registers_accept_fc16_and_read_back(self):
        config = load_config(ROOT / 'config/simulator.yaml')
        rtu0 = config['channels'][0]
        device = next(device for device in rtu0['devices'] if device['slave_id'] == 3)
        store = make_datastore([device])

        self.assertEqual(await store.async_getValues(3, 3, 100, 3), [0, 0, 0])
        self.assertIsNone(await store.async_setValues(3, 16, 100, [12, 34, 56]))
        self.assertEqual(await store.async_getValues(3, 3, 100, 3), [12, 34, 56])

        self.assertEqual(
            [(record.function, record.address, record.values)
             for record in store.journal.writes],
            [(16, 100, (12, 34, 56))],
        )


if __name__ == '__main__':
    unittest.main()
