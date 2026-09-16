import struct
import unittest
from pathlib import Path

from pymodbus.constants import ExcCodes
from pymodbus.framer import FramerRTU

from modbus_simulator.config import load_config
from modbus_simulator.datastore import make_datastore
from modbus_simulator.fixtures.znck_i import (
    DEFAULT_DETAIL_REGISTERS,
    DETAIL_QUANTITY,
    DETAIL_START_ADDRESS,
    FAULT_CODE_ADDRESS,
    QUERY_INDEX_ADDRESS,
    UNIT_ID,
    ZnckIFixture,
)
from modbus_simulator.logging import ChannelLog
from modbus_simulator.protocol import Protocol

ROOT = Path(__file__).resolve().parents[1]


def read_pdu(address, count):
    return struct.pack('>BHH', 3, address, count)


def write_query_pdu(value):
    return struct.pack('>BHHBH', 16, QUERY_INDEX_ADDRESS, 1, 2, value)


def frame(pdu, rtu):
    if rtu:
        body = bytes([UNIT_ID]) + pdu
        return body + FramerRTU.compute_CRC(body).to_bytes(2, 'big')
    return struct.pack('>HHHB', 7, 0, len(pdu) + 1, UNIT_ID) + pdu


def pdu_from_response(response, rtu):
    if rtu:
        if not FramerRTU.check_CRC(response[:-2], int.from_bytes(response[-2:], 'big')):
            raise AssertionError('fixture response has an invalid CRC')
        return response[1:-2]
    return response[7:]


class ZnckIFixtureTest(unittest.IsolatedAsyncioTestCase):
    def make_protocol(self, fixture, rtu=False):
        channel = {
            'name': 'znck-i-test',
            'protocol': 'rtu' if rtu else 'tcp',
            'devices': [fixture.devices[UNIT_ID].config],
        }
        return Protocol(fixture, ChannelLog(channel), rtu)

    async def request(self, protocol, pdu, rtu=False):
        response = await protocol.handle(frame(pdu, rtu))
        return pdu_from_response(response, rtu)

    async def test_initial_state_and_index_zero_refresh(self):
        fixture = ZnckIFixture()

        self.assertEqual(await fixture.read_fault_code(), [0])
        self.assertEqual(await fixture.read_details(), [0] * DETAIL_QUANTITY)
        fixture.journal.clear()

        self.assertIsNone(await fixture.async_setValues(
            UNIT_ID, 16, QUERY_INDEX_ADDRESS, [0]))
        self.assertEqual(await fixture.read_details(), list(DEFAULT_DETAIL_REGISTERS))
        self.assertEqual(fixture.query_indices, [0])
        self.assertEqual(
            [(record.function, record.address, record.count)
             for record in fixture.journal.requests],
            [(16, QUERY_INDEX_ADDRESS, 1),
             (3, DETAIL_START_ADDRESS, DETAIL_QUANTITY)],
        )
        self.assertEqual(
            [(record.function, record.address, record.values)
             for record in fixture.journal.writes],
            [(16, QUERY_INDEX_ADDRESS, (0,))],
        )
        self.assertEqual(fixture.journal.exceptions, [])

    async def test_fault_code_state_changes_are_raw_and_repeatable(self):
        fixture = ZnckIFixture()

        for code in (1, 1, 2, 0, 1):
            with self.subTest(code=code):
                self.assertIsNone(await fixture.set_fault_code(code))
                self.assertEqual(await fixture.read_fault_code(), [code])

        self.assertEqual(
            [record.values for record in fixture.journal.writes],
            [(1,), (1,), (2,), (0,), (1,)],
        )

    async def test_protocol_records_static_then_dynamic_sequence(self):
        for rtu in (False, True):
            with self.subTest(rtu=rtu):
                fixture = ZnckIFixture()
                await fixture.set_fault_code(7)
                fixture.journal.clear()
                protocol = self.make_protocol(fixture, rtu)

                self.assertEqual(
                    await self.request(protocol, read_pdu(FAULT_CODE_ADDRESS, 1), rtu),
                    b'\x03\x02\x00\x07',
                )
                self.assertEqual(
                    await self.request(protocol, write_query_pdu(0), rtu),
                    struct.pack('>BHH', 16, QUERY_INDEX_ADDRESS, 1),
                )
                detail_pdu = await self.request(
                    protocol, read_pdu(DETAIL_START_ADDRESS, DETAIL_QUANTITY), rtu)
                expected_detail = b'\x03\x22' + b''.join(
                    value.to_bytes(2, 'big') for value in DEFAULT_DETAIL_REGISTERS)
                self.assertEqual(detail_pdu, expected_detail)

                self.assertEqual(
                    [(record.function, record.address, record.count)
                     for record in fixture.journal.requests],
                    [(3, FAULT_CODE_ADDRESS, 1),
                     (16, QUERY_INDEX_ADDRESS, 1),
                     (3, DETAIL_START_ADDRESS, DETAIL_QUANTITY)],
                )
                self.assertEqual(
                    [(record.function, record.address, record.values)
                     for record in fixture.journal.writes],
                    [(16, QUERY_INDEX_ADDRESS, (0,))],
                )
                self.assertEqual(fixture.journal.exceptions, [])

    async def test_invalid_index_and_address_are_recorded_as_exceptions(self):
        fixture = ZnckIFixture()
        protocol = self.make_protocol(fixture)

        self.assertEqual(
            await self.request(protocol, write_query_pdu(1)), b'\x90\x03')
        self.assertEqual(
            await self.request(protocol, read_pdu(QUERY_INDEX_ADDRESS - 1, 1)),
            b'\x83\x02',
        )
        self.assertEqual(
            await self.request(protocol, bytes.fromhex('41 0000 0001')),
            b'\xc1\x01',
        )

        self.assertEqual(
            [(record.function, record.address, record.count, record.code)
             for record in fixture.journal.exceptions],
            [(16, QUERY_INDEX_ADDRESS, 1, int(ExcCodes.ILLEGAL_VALUE)),
             (3, QUERY_INDEX_ADDRESS - 1, 1, int(ExcCodes.ILLEGAL_ADDRESS)),
             (0x41, 0, 1, int(ExcCodes.ILLEGAL_FUNCTION))],
        )

    def test_yaml_and_datastore_factory_expose_reusable_fixture(self):
        config = load_config(ROOT / 'config/znck-i-fixture.yaml')
        self.assertEqual(len(config['channels']), 1)
        device = config['channels'][0]['devices'][0]
        self.assertEqual(device['slave_id'], UNIT_ID)
        self.assertEqual(device['fixture'], 'znck_i')
        self.assertEqual(device['registers'][1]['address'], DETAIL_START_ADDRESS)
        self.assertEqual(device['registers'][1]['value'], 0)

        store = make_datastore([device])
        self.assertIsInstance(store, ZnckIFixture)

    def test_runtime_e2e_config_can_start_with_a_nonzero_raw_fault(self):
        config = load_config(ROOT / 'config/znck-i-runtime-e2e.yaml')
        self.assertEqual(len(config['channels']), 2)
        for channel in config['channels']:
            self.assertEqual(channel['fixture_options']['initial_fault_code'], 7)
            store = make_datastore(channel['devices'], fixture_options=channel['fixture_options'])
            self.assertIsInstance(store, ZnckIFixture)
            self.assertEqual(store.devices[UNIT_ID].config['registers'][-1]['value'], 7)


if __name__ == '__main__':
    unittest.main()
