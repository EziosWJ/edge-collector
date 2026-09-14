"""Validate PDU boundaries before the native decoder.

PyModbus 3.15 turns failed TCP PDU decoding into FC00/illegal-function.
Return an executable exception request to preserve the original function/id.
"""
from pymodbus.constants import ExcCodes
from pymodbus.pdu import DecodePDU, ExceptionResponse, ModbusPDU

SUPPORTED = {1, 2, 3, 4, 5, 6, 15, 16}


class RejectedRequest(ModbusPDU):
    def __init__(self, function, error):
        super().__init__()
        self.function_code = function
        self.error = error

    async def datastore_update(self, context, device_id):
        return ExceptionResponse(self.function_code, self.error)


class RequestDecoder(DecodePDU):
    def __init__(self):
        super().__init__(True)

    def decode(self, frame):
        if not frame:
            return None
        function = frame[0]
        if function not in SUPPORTED:
            return RejectedRequest(function, ExcCodes.ILLEGAL_FUNCTION)
        error = False
        if function in (1, 2, 3, 4, 5, 6):
            error = len(frame) != 5
        else:
            error = len(frame) < 6 or len(frame) != 6 + frame[5]
            if not error:
                count = int.from_bytes(frame[3:5], 'big')
                error = frame[5] != ((count + 7) // 8 if function == 15 else count * 2)
        if not error and function == 5:
            error = frame[3:5] not in (b'\0\0', b'\xff\0')
        if error:
            return RejectedRequest(function, ExcCodes.ILLEGAL_VALUE)
        return super().decode(frame) or RejectedRequest(function, ExcCodes.ILLEGAL_VALUE)
