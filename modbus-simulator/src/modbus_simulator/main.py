"""Simulator lifecycle and command line."""
import argparse
import asyncio
import logging
import signal
from .config import load_config
from .datastore import Datastore
from .logging import ChannelLog, HideFrameDump
from .transports import tcp, udp, rtu, rtu_over_udp

TRANSPORTS = {'tcp': tcp, 'udp': udp, 'rtu': rtu, 'rtu_over_udp': rtu_over_udp}


class Simulator:
    def __init__(self, config):
        self.config = config
        self.servers = []

    async def start(self):
        try:
            for channel in self.config['channels']:
                log = ChannelLog(channel, self.config.get('logging', {}).get('hex', False))
                store = Datastore(channel['devices'])
                server = await TRANSPORTS[channel['protocol']].start(channel, store, log)
                self.servers.append(server)
                endpoint = (f"alias={channel['alias']} pty={server.pty.slave_name} "
                            f"baudrate={channel.get('baudrate', 9600)} bytesize={channel.get('bytesize', 8)} "
                            f"parity={channel.get('parity', 'N')} stopbits={channel.get('stopbits', 1)}"
                            if channel['protocol'] == 'rtu' else
                            f"listen={channel.get('host', '127.0.0.1')}:{channel['port']}")
                log.logger.info('[%s] channel=%s %s devices=%s', channel['protocol'], channel['name'],
                                endpoint, ', '.join(f"{d['slave_id']}:{d['name']}" for d in channel['devices']))
            logging.getLogger('modbus_simulator').info('Modbus Simulator Started')
        except BaseException:
            await self.stop()
            raise

    async def stop(self):
        for server in reversed(self.servers):
            await server.shutdown()
        self.servers.clear()


async def run(config):
    simulator = Simulator(config)
    stop = asyncio.Event()
    loop = asyncio.get_running_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(sig, stop.set)
    try:
        await simulator.start()
        await stop.wait()
    finally:
        await simulator.stop()


def main():
    parser = argparse.ArgumentParser(description='当前 Go 采集项目的开发用 Modbus 设备模拟器')
    parser.add_argument('--config', default='config/simulator.yaml')
    parser.add_argument('--check-config', action='store_true')
    args = parser.parse_args()
    try:
        config = load_config(args.config)
        logging.basicConfig(level=config.get('logging', {}).get('level', 'INFO'),
                            format='%(asctime)s %(levelname)s %(message)s')
        if not config.get('logging', {}).get('hex', False):
            logging.getLogger('pymodbus.logging').addFilter(HideFrameDump())
        if args.check_config:
            print('Configuration OK')
            return
        asyncio.run(run(config))
    except (ValueError, TypeError, KeyError, OSError, RuntimeError) as exc:
        parser.exit(1, f'启动失败: {exc}\n')
