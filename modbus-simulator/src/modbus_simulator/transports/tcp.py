"""Native PyModbus TCP server; no local TCP protocol implementation."""
from pymodbus.server import ModbusTcpServer
from ..decoder import RequestDecoder


class TcpServer(ModbusTcpServer):
    def callback_new_connection(self):
        handler = super().callback_new_connection()
        handler.trace_pdu = self.channel_log.pdu_tracer()
        return handler


async def start(channel, store, log):
    server = TcpServer(store.models,
                             address=(channel.get('host', '127.0.0.1'), channel['port']),
                             trace_packet=log.packet)
    server.channel_log = log
    server.context = store
    server.decoder = RequestDecoder()
    try:
        await server.serve_forever(background=True)
    except BaseException:
        await server.shutdown()
        raise
    return server
