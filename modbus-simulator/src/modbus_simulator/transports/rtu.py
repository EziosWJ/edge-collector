"""RTU master-FD transport. Native PTY slave is opened by the Go client."""
import asyncio
import os
from ..protocol import Protocol
from ..pty import Pty


class Rtu:
    def __init__(self, channel, store, log):
        self.pty = Pty(channel['alias']).open()
        self.protocol = Protocol(store, log, True)
        self.loop = asyncio.get_running_loop()
        self.buffer = bytearray()
        self.timer = None
        self.queue = asyncio.Queue(maxsize=64)
        self.loop.add_reader(self.pty.master, self.readable)
        self.worker = asyncio.create_task(self.run())

    def readable(self):
        try:
            data = os.read(self.pty.master, 4096)
            self.buffer.extend(data)
            if self.timer:
                self.timer.cancel()
            # PTY has no baud timing. An idle gap bounds unknown/invalid frames;
            # known requests can be extracted as soon as complete.
            self.extract()
            if len(self.buffer) > 256:
                self.protocol.log.logger.warning("RTU oversized frame dropped")
                self.buffer.clear()
            self.timer = self.loop.call_later(0.03, self.flush)
        except BlockingIOError:
            pass
        except OSError:
            self.protocol.log.logger.exception('PTY read failed')

    def enqueue(self, data):
        try:
            self.queue.put_nowait(data)
        except asyncio.QueueFull:
            self.protocol.log.logger.warning('RTU request queue full, dropping frame')

    def extract(self):
        while len(self.buffer) >= 2:
            cls = self.protocol.decoder.lookupPduClass(self.buffer)
            if cls is None:
                return
            try:
                length = cls.calculateRtuFrameSize(self.buffer)
            except (IndexError, ValueError):
                return
            if not length or len(self.buffer) < length:
                return
            self.enqueue(bytes(self.buffer[:length]))
            del self.buffer[:length]

    def flush(self):
        if self.buffer:
            self.enqueue(bytes(self.buffer))
            self.buffer.clear()

    async def run(self):
        while True:
            data = await self.queue.get()
            try:
                response = await self.protocol.handle(data)
                if response:
                    view = memoryview(response)
                    while view:
                        try:
                            written = os.write(self.pty.master, view)
                            view = view[written:]
                        except BlockingIOError:
                            await asyncio.sleep(0.001)
            except Exception:
                self.protocol.log.logger.exception('非法 RTU 请求，服务继续运行')

    async def shutdown(self):
        self.loop.remove_reader(self.pty.master)
        if self.timer:
            self.timer.cancel()
        self.worker.cancel()
        await asyncio.gather(self.worker, return_exceptions=True)
        self.pty.close()


async def start(channel, store, log):
    return Rtu(channel, store, log)
