"""Shared human-readable request and optional raw-frame logging."""
import logging


class ChannelLog:
    def __init__(self, channel, hex_enabled=False):
        self.channel = channel
        self.hex_enabled = hex_enabled
        self.logger = logging.getLogger('modbus_simulator')
        self.names = {d['slave_id']: d['name'] for d in channel['devices']}

    def packet(self, sending, data):
        if self.hex_enabled:
            self.logger.info('[%s %s HEX] channel=%s %s', self.channel['protocol'],
                             'TX' if sending else 'RX', self.channel['name'], data.hex(' ').upper())
        return data

    def result(self, slave, function, address, count, result):
        self.logger.info('[%s] channel=%s device=%s slave=%s function=%02X address=%s count=%s result=%s',
                         self.channel['protocol'], self.channel['name'], self.names.get(slave, 'unknown'),
                         slave, function, address, count, result)

    def pdu_tracer(self):
        pending = {}

        def trace(sending, pdu):
            key = (pdu.transaction_id, pdu.dev_id)
            if not sending:
                details = (pdu.function_code, getattr(pdu, 'address', None),
                           1 if pdu.function_code in (5, 6) else getattr(pdu, 'count', None))
                if len(pending) >= 128:
                    pending.clear()
                pending[key] = details
                result = 'RX'
            else:
                details = pending.pop(key, (pdu.function_code, None, None))
                result = f'EXCEPTION:{pdu.exception_code}' if pdu.isError() else 'OK'
            self.result(pdu.dev_id, *details, result)
            return pdu

        return trace


class HideFrameDump(logging.Filter):
    """Keep native error text while honoring hex:false for PyModbus dumps."""
    def filter(self, record):
        record.msg = record.getMessage().split('\n>>>>>', 1)[0]
        record.args = ()
        return not str(record.msg).startswith(('send:', 'recv:', 'extra:'))
