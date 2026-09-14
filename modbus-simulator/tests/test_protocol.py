"""Real sockets and native PTYs, including all eight requested functions."""
import asyncio
import os
from pathlib import Path
import select
import socket
import struct
import tempfile
import time
import tty
import unittest
from pymodbus.framer import FramerRTU
from modbus_simulator.config import validate_device
from modbus_simulator.datastore import Datastore
from modbus_simulator.logging import ChannelLog
from modbus_simulator.transports import tcp, udp, rtu, rtu_over_udp


def device(slave=1):
    # Explicit generic transport fixture, not a claimed vendor register map.
    return validate_device(dict(name=f'test-{slave}', slave_id=slave, registers=[
        dict(name='holding', area='holding_register', address=0, type='uint32', value=0x006400C8),
        dict(name='input', area='input_register', address=0, type='uint16', value=250),
        dict(name='coil', area='coil', address=0, type='bool', value=True),
        dict(name='discrete', area='discrete_input', address=0, type='bool', value=False)]))


def frame(pdu, slave=1, rtu_mode=False):
    if rtu_mode:
        body = bytes([slave]) + pdu
        return body + FramerRTU.compute_CRC(body).to_bytes(2, 'big')
    return struct.pack('>HHHB', 42, 0, len(pdu) + 1, slave) + pdu


def exchange(mode, endpoint, request, timeout=1, split=False):
    if mode == 'rtu':
        fd = os.open(endpoint, os.O_RDWR | os.O_NOCTTY | os.O_NONBLOCK)
        try:
            tty.setraw(fd)
            if split:
                os.write(fd, request[:3]); time.sleep(0.005); os.write(fd, request[3:])
            else:
                os.write(fd, request)
            if not select.select([fd], [], [], timeout)[0]:
                raise TimeoutError('PTY timeout')
            result = os.read(fd, 512)
        finally:
            os.close(fd)
    else:
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM if mode == 'tcp' else socket.SOCK_DGRAM) as sock:
            sock.settimeout(timeout)
            sock.connect(endpoint)
            sock.sendall(request)
            result = sock.recv(512)
            if mode == 'tcp':
                while len(result) < 6 or len(result) < 6 + int.from_bytes(result[4:6], 'big'):
                    part = sock.recv(512)
                    if not part:
                        raise ValueError('TCP closed before complete ADU')
                    result += part
    if mode in ('rtu', 'rtu_over_udp'):
        if not FramerRTU.check_CRC(result[:-2], int.from_bytes(result[-2:], 'big')):
            raise ValueError('bad response CRC')
        if result[0] != request[0]:
            raise ValueError('wrong slave')
        return result[1:-2]
    if result[:4] != request[:4] or result[6] != request[6] or len(result) != int.from_bytes(result[4:6], 'big') + 6:
        raise ValueError('wrong MBAP')
    return result[7:]


class ProtocolTest(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.servers, self.channels, self.stores = {}, {}, {}
        for mode, module in [('tcp', tcp), ('udp', udp), ('rtu_over_udp', rtu_over_udp), ('rtu', rtu)]:
            channel = dict(name=mode, protocol=mode, host='127.0.0.1', port=0,
                           alias=str(Path(self.tmp.name) / 'modbus-rtu'), devices=[device(), device(2)])
            store = Datastore(channel['devices'])
            server = await module.start(channel, store, ChannelLog(channel))
            self.servers[mode] = server
            self.stores[mode] = store
            if mode == 'rtu':
                endpoint = channel['alias']
            elif mode == 'tcp':
                endpoint = server.transport.sockets[0].getsockname()
            else:
                endpoint = server.transport.get_extra_info('sockname')
            self.channels[mode] = endpoint

    async def asyncTearDown(self):
        for server in self.servers.values():
            await server.shutdown()
        self.tmp.cleanup()

    async def request(self, mode, pdu, slave=1, **kwargs):
        return await asyncio.to_thread(exchange, mode, self.channels[mode],
                                       frame(bytes.fromhex(pdu), slave, mode in ('rtu', 'rtu_over_udp')), **kwargs)

    async def test_all_modes_and_functions(self):
        for mode in self.servers:
            with self.subTest(mode=mode):
                self.assertEqual(await self.request(mode, '03 0000 0002'), bytes.fromhex('03 04 0064 00c8'))
                self.assertEqual(await self.request(mode, '04 0000 0001', slave=2), bytes.fromhex('04 02 00fa'))
                self.assertEqual(await self.request(mode, '01 0000 0001'), b'\x01\x01\x01')
                self.assertEqual(await self.request(mode, '02 0000 0001'), b'\x02\x01\x00')
                self.assertEqual(await self.request(mode, '06 0000 012c'), bytes.fromhex('06 0000 012c'))
                self.assertEqual(await self.request(mode, '03 0000 0001'), bytes.fromhex('03 02 012c'))
                self.assertEqual(await self.request(mode, '10 0000 0002 04 000a 0014'), bytes.fromhex('10 0000 0002'))
                self.assertEqual(await self.request(mode, '03 0000 0002'), bytes.fromhex('03 04 000a 0014'))
                self.assertEqual(await self.request(mode, '05 0000 0000'), bytes.fromhex('05 0000 0000'))
                self.assertEqual(await self.request(mode, '01 0000 0001'), b'\x01\x01\x00')
                self.assertEqual(await self.request(mode, '0f 0000 0001 01 01'), bytes.fromhex('0f 0000 0001'))
                self.assertEqual(await self.request(mode, '01 0000 0001'), b'\x01\x01\x01')

    async def test_exceptions_and_recovery(self):
        for mode in self.servers:
            with self.subTest(mode=mode):
                self.assertEqual(await self.request(mode, '03 ffff 0002'), b'\x83\x02')
                self.assertEqual(await self.request(mode, '01 0001 0001'), b'\x81\x02')
                self.assertEqual(await self.request(mode, '03 0000 0000'), b'\x83\x03')
                self.assertEqual(await self.request(mode, '41 0000 0001'), b'\xc1\x01')
                if mode in ('rtu', 'rtu_over_udp'):
                    with self.assertRaises(TimeoutError):
                        await self.request(mode, '03 0000 0001', slave=99, timeout=0.1)
                    bad = bytearray(frame(bytes.fromhex('03 0000 0001'), rtu_mode=True))
                    bad[-1] ^= 1
                    with self.assertRaises(TimeoutError):
                        await asyncio.to_thread(exchange, mode, self.channels[mode], bytes(bad), 0.1)
                else:
                    self.assertEqual(await self.request(mode, '03 0000 0001', slave=99), b'\x83\x0b')
                self.assertEqual(await self.request(mode, '03 0000 0001'), b'\x03\x02\x00\x64')

    async def test_bad_datagrams_and_fragmented_rtu(self):
        for mode in ('udp', 'rtu_over_udp'):
            for data in (b'', b'\x01', b'invalid-datagram', b'\0'*300):
                with self.assertRaises(TimeoutError):
                    await asyncio.to_thread(exchange, mode, self.channels[mode], data, 0.05)
            self.assertEqual(await self.request(mode, '03 0000 0001'), b'\x03\x02\x00\x64')
        self.assertEqual(await self.request('rtu', '04 0000 0001', split=True), b'\x04\x02\x00\xfa')

    async def test_delay_and_dynamic(self):
        for mode in self.servers:
            d = self.stores[mode].devices[1]
            d.config['behavior'] = {'response_delay_ms': 60}
            start = time.monotonic()
            await self.request(mode, '03 0000 0001')
            self.assertGreaterEqual(time.monotonic() - start, 0.05)
            d.config['behavior']['response_delay_ms'] = 0
            sim = d.simulations[1]
            sim.register['simulation'] = dict(type='increment', interval=1, step=1, min=0, max=300)
            sim.last -= 2
            self.assertEqual(await self.request(mode, '04 0000 0001'), b'\x04\x02\x00\xfc')

    async def test_invalid_pdu_lengths(self):
        for mode in self.servers:
            # RTU idle delimiting accepts only complete ADUs; use a variable
            # length write whose byte count defines the malformed full frame.
            self.assertEqual(await self.request(mode, '10 0000 0002 02 0001'), b'\x90\x03')
            self.assertEqual(await self.request(mode, '05 0000 1234'), b'\x85\x03')
            self.assertEqual(await self.request(mode, '03 0000 0001'), b'\x03\x02\x00\x64')

    async def test_startup_failure_cleans_pty(self):
        from modbus_simulator.main import Simulator
        alias = str(Path(self.tmp.name) / 'rollback-pty')
        config = {'channels': [
            dict(name='first', protocol='rtu', alias=alias, devices=[device()]),
            dict(name='busy', protocol='tcp', host='127.0.0.1',
                 port=self.channels['tcp'][1], devices=[device()])]}
        simulator = Simulator(config)
        with self.assertRaises(RuntimeError):
            await simulator.start()
        self.assertFalse(Path(alias).is_symlink())
        self.assertFalse(simulator.servers)
        self.assertEqual(await self.request('tcp', '03 0000 0001'), b'\x03\x02\x00\x64')
