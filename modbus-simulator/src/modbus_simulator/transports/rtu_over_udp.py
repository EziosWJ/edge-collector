"""Full RTU ADU (slave + PDU + CRC) in one UDP payload, without MBAP."""
from .udp import start as start_datagram


async def start(channel, store, log):
    return await start_datagram(channel, store, log, rtu=True)
