"""Engineering value -> raw Modbus words; addresses are zero based."""
import math
import struct

FORMATS = {'bool': '?', 'uint16': 'H', 'int16': 'h', 'uint32': 'I',
           'int32': 'i', 'float32': 'f', 'float64': 'd'}
AREAS = ('coil', 'discrete_input', 'holding_register', 'input_register')


def encode(register, value=None):
    value = register['value'] if value is None else value
    kind = register['type']
    if kind == 'bool':
        if type(value) is not bool:
            raise ValueError('bool value 必须是 true/false')
        return [value]
    raw = value / register.get('scale', 1)
    if not math.isfinite(raw):
        raise ValueError('value 必须为有限数值')
    if kind not in ('float32', 'float64'):
        rounded = round(raw)
        if not math.isclose(raw, rounded, abs_tol=1e-7):
            raise ValueError('value/scale 必须能精确表示为整数')
        raw = rounded
    try:
        data = struct.pack('>' + FORMATS[kind], raw)
    except (struct.error, OverflowError) as exc:
        raise ValueError(f'{kind} 数值越界: {value}') from exc
    words = [data[i:i+2] for i in range(0, len(data), 2)]
    if register.get('byte_order', 'big') == 'little':
        words = [word[::-1] for word in words]
    if register.get('word_order', 'big') == 'little':
        words.reverse()
    return [int.from_bytes(word, 'big') for word in words]
