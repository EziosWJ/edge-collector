"""MBAP + PDU over UDP. Each datagram is independently framed."""
import asyncio
from ..protocol import Protocol


class Datagram(asyncio.DatagramProtocol):
    def __init__(self, protocol):
        self.protocol = protocol
        self.tasks = set()
        self.transport = None

    def connection_made(self, transport):
        self.transport = transport

    def datagram_received(self, data, addr):
        if len(self.tasks) >= 128:
            self.protocol.log.logger.warning('UDP pending request limit reached')
            return
        task = asyncio.create_task(self.respond(data, addr))
        self.tasks.add(task)
        task.add_done_callback(self.tasks.discard)

    async def respond(self, data, addr):
        try:
            response = await self.protocol.handle(data)
            if response:
                self.transport.sendto(response, addr)
        except Exception:
            self.protocol.log.logger.exception('非法 UDP Datagram，服务继续运行')

    def error_received(self, exc):
        self.protocol.log.logger.warning('UDP error: %s', exc)

    async def shutdown(self):
        self.transport.close()
        for task in list(self.tasks):
            task.cancel()
        await asyncio.gather(*self.tasks, return_exceptions=True)


async def start(channel, store, log, *, rtu=False):
    protocol = Datagram(Protocol(store, log, rtu))
    await asyncio.get_running_loop().create_datagram_endpoint(
        lambda: protocol, local_addr=(channel.get('host', '127.0.0.1'), channel['port']))
    return protocol
