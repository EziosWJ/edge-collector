import unittest
from pathlib import Path

from pymodbus.constants import ExcCodes

from modbus_simulator.config import load_config
from modbus_simulator.datastore import make_datastore
from modbus_simulator.fixtures.rtc_clock import RtcClockFixture

ROOT = Path(__file__).resolve().parents[1]


class TimeRegisterDeviceTest(unittest.IsolatedAsyncioTestCase):
    def rtc_device(self):
        config = load_config(ROOT / 'config/simulator.yaml')
        rtc = next(channel for channel in config['channels'] if channel['name'] == 'rtc0')
        return rtc['devices'][0]

    async def test_make_datastore_selects_rtc_fixture(self):
        store = make_datastore([self.rtc_device()])
        self.assertIsInstance(store, RtcClockFixture)
        self.assertEqual(store.device_ids(), [3])

    async def test_rtc_runs_after_fc16_clock_adjustment(self):
        tick = [1000.0]
        store = RtcClockFixture(config=self.rtc_device(), clock=lambda: tick[0])

        self.assertEqual(await store.read_time(), [0, 0, 0])
        self.assertIsNone(await store.async_setValues(3, 16, 100, [23, 59, 58]))
        self.assertEqual(await store.read_time(), [23, 59, 58])

        tick[0] += 3.2
        self.assertEqual(await store.read_time(), [0, 0, 1])

        self.assertEqual(
            [(record.function, record.address, record.values)
             for record in store.journal.writes],
            [(16, 100, (23, 59, 58))],
        )

    async def test_rtc_rejects_invalid_or_partial_time_writes(self):
        tick = [2000.0]
        store = RtcClockFixture(config=self.rtc_device(), clock=lambda: tick[0])

        self.assertEqual(
            await store.async_setValues(3, 16, 100, [24, 0, 0]),
            ExcCodes.ILLEGAL_VALUE,
        )
        self.assertEqual(
            await store.async_setValues(3, 16, 100, [12]),
            ExcCodes.ILLEGAL_VALUE,
        )
        self.assertEqual(
            await store.async_setValues(3, 6, 100, [12]),
            ExcCodes.ILLEGAL_VALUE,
        )
        self.assertEqual(len(store.journal.writes), 0)
        self.assertEqual(len(store.journal.exceptions), 3)


if __name__ == '__main__':
    unittest.main()
