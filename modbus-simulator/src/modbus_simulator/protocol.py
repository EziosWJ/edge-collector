"""Datagram/PTY framing adapter. PDU execution and CRC belong to PyModbus."""
from pymodbus.constants import ExcCodes
from pymodbus.exceptions import NoSuchIdException
from pymodbus.framer import FramerRTU, FramerSocket
from pymodbus.pdu import ExceptionResponse
from .decoder import RequestDecoder


class Protocol:
    def __init__(self, store, log, rtu):
        self.store, self.log, self.rtu = store, log, rtu
        self.decoder = RequestDecoder()
        self.framer = (FramerRTU if rtu else FramerSocket)(self.decoder)

    async def handle(self, data):
        self.log.packet(False, data)
        # A UDP datagram or PTY idle-delimited frame must contain one ADU.
        if self.rtu:
            if not 4 <= len(data) <= 256 or not FramerRTU.check_CRC(data[:-2], int.from_bytes(data[-2:], 'big')):
                self.log.result(data[0] if data else 0, data[1] if len(data) > 1 else 0, None, None, 'DROP:CRC/length')
                return None
            slave, tid, payload = data[0], 0, data[1:-2]
        else:
            if not 8 <= len(data) <= 260 or data[2:4] != b'\0\0' or int.from_bytes(data[4:6], 'big') != len(data) - 6:
                self.log.result(0, 0, None, None, 'DROP:MBAP/length')
                return None
            _, slave, tid, payload = self.framer.decode(data)
        function = payload[0]
        address = int.from_bytes(payload[1:3], 'big') if len(payload) >= 3 else None
        count = int.from_bytes(payload[3:5], 'big') if len(payload) >= 5 else None
        if function in (5, 6):
            count = 1
        # Serial unknown devices and broadcast do not respond. Broadcast writes
        # are intentionally outside this development tool's supported scope.
        if self.rtu and slave not in self.store.devices:
            self.log.result(slave, function, address, count, 'DROP:unknown/broadcast')
            return None
        request = self.decoder.decode(payload)
        try:
            response = await request.datastore_update(self.store, slave)
        except NoSuchIdException:
            response = ExceptionResponse(function, ExcCodes.GATEWAY_NO_RESPONSE)
        except Exception:
            self.log.logger.exception('请求处理失败，保留服务运行')
            response = ExceptionResponse(function, ExcCodes.DEVICE_FAILURE)
        self.log.result(slave, function, address, count,
                        f'EXCEPTION:{response.exception_code}' if response.isError() else 'OK')
        frame = self.framer.encode(bytes([response.function_code]) + response.encode(), slave, tid)
        self.log.packet(True, frame)
        return frame
