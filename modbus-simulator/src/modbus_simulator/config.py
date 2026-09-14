"""Validate all files before opening ports or creating PTYs."""
import logging
import math
from pathlib import Path
import yaml
from .register import AREAS, FORMATS, encode


def mapping(value, label):
    if not isinstance(value, dict):
        raise ValueError(f'{label} 必须为 mapping')
    return value


def integer(value, lo, hi, label):
    if type(value) is not int or not lo <= value <= hi:
        raise ValueError(f'{label} 必须为 {lo}..{hi} 整数')


def read_yaml(path):
    try:
        return mapping(yaml.safe_load(Path(path).read_text()), str(path))
    except (OSError, yaml.YAMLError) as exc:
        raise ValueError(f'{path}: {exc}') from exc


def validate_device(device):
    mapping(device, 'device')
    if not isinstance(device.get('name'), str) or not device['name']:
        raise ValueError('device.name 不能为空')
    integer(device.get('slave_id'), 1, 247, 'slave_id')
    behavior = mapping(device.get('behavior', {}), 'behavior')
    integer(behavior.get('response_delay_ms', 0), 0, 60000, 'response_delay_ms')
    if set(behavior) - {'response_delay_ms'}:
        raise ValueError('behavior 当前仅支持 response_delay_ms')
    registers = device.get('registers')
    if not isinstance(registers, list) or not registers:
        raise ValueError('registers 必须是非空列表')
    occupied = set()
    names = set()
    for reg in registers:
        mapping(reg, 'register')
        if not isinstance(reg.get('name'), str) or reg['name'] in names:
            raise ValueError('寄存器 name 缺失或重复')
        names.add(reg['name'])
        if reg.get('area') not in AREAS or reg.get('type') not in FORMATS:
            raise ValueError('未知 area/type')
        if (reg['area'] in AREAS[:2]) != (reg['type'] == 'bool'):
            raise ValueError('coil/discrete_input 使用 bool，其余区域使用数值类型')
        integer(reg.get('address'), 0, 65535, 'address')
        for key in ('byte_order', 'word_order'):
            if reg.get(key, 'big') not in ('big', 'little'):
                raise ValueError(f'{key} 必须是 big/little')
        scale = reg.get('scale', 1)
        if type(scale) not in (int, float) or not math.isfinite(scale) or scale <= 0:
            raise ValueError('scale 必须为正有限数值')
        values = encode(reg)
        integer(reg.get('length', len(values)), len(values), len(values), 'length')
        if reg['address'] + len(values) > 65536:
            raise ValueError('寄存器越界')
        for address in range(reg['address'], reg['address'] + len(values)):
            key = (reg['area'], address)
            if key in occupied:
                raise ValueError(f'寄存器重叠: {key}')
            occupied.add(key)
        for bit, label in mapping(reg.get('bits', {}), 'bits').items():
            integer(bit, 0, len(values) * 16 - 1, 'bit')
            if not isinstance(label, str):
                raise ValueError('bits 语义必须为字符串')
        sim = mapping(reg.get('simulation', {}), 'simulation')
        if sim.get('type', 'fixed') not in ('fixed', 'increment', 'decrement', 'random'):
            raise ValueError('未知 simulation.type')
        interval = sim.get('interval', 1)
        if type(interval) not in (int, float) or not math.isfinite(interval) or interval <= 0:
            raise ValueError('simulation.interval 必须为正有限数值')
        if sim.get('type', 'fixed') != 'fixed':
            if reg['type'] == 'bool' or sim['min'] > sim['max']:
                raise ValueError('动态模拟要求数值类型和 min <= max')
            for value in (sim['min'], sim['max'], reg['value']):
                encode(reg, value)
            if not sim['min'] <= reg['value'] <= sim['max']:
                raise ValueError('初始 value 必须在 min/max 范围内')
            step = sim.get('step', 1)
            if type(step) not in (int, float) or not math.isfinite(step) or step <= 0:
                raise ValueError('simulation.step 必须为正有限数值')
            if sim['type'] != 'random':
                encode(reg, sim['min'] + step)
    return device


def load_config(path):
    path = Path(path)
    config = read_yaml(path)
    logs = mapping(config.get('logging', {}), 'logging')
    if logs.get('level', 'INFO') not in logging.getLevelNamesMapping():
        raise ValueError('未知日志级别')
    if type(logs.get('hex', False)) is not bool:
        raise ValueError('logging.hex 必须为 bool')
    channels = config.get('channels')
    if not isinstance(channels, list) or not channels:
        raise ValueError('channels 必须是非空列表')
    names, aliases, endpoints = set(), set(), set()
    for channel in channels:
        mapping(channel, 'channel')
        name = channel.get('name')
        if not isinstance(name, str) or not name or name in names:
            raise ValueError('channel.name 缺失或重复')
        names.add(name)
        protocol = channel.get('protocol')
        if protocol not in ('tcp', 'udp', 'rtu', 'rtu_over_udp'):
            raise ValueError(f'未知 protocol: {protocol}')
        if protocol == 'rtu':
            alias = channel.get('alias', '')
            if not isinstance(alias, str) or not Path(alias).is_absolute() or alias in aliases:
                raise ValueError('PTY alias 必须是唯一绝对路径')
            aliases.add(alias)
            integer(channel.get('baudrate', 9600), 1, 4000000, 'baudrate')
            integer(channel.get('bytesize', 8), 5, 8, 'bytesize')
            if channel.get('parity', 'N') not in ('N', 'E', 'O') or channel.get('stopbits', 1) not in (1, 2):
                raise ValueError('非法串口参数')
        else:
            integer(channel.get('port'), 1, 65535, 'port')
            if not isinstance(channel.get('host', '127.0.0.1'), str):
                raise ValueError('host 必须为字符串')
            endpoint = ('tcp' if protocol == 'tcp' else 'udp', channel.get('host', '127.0.0.1'), channel['port'])
            if endpoint in endpoints:
                raise ValueError('监听地址重复')
            endpoints.add(endpoint)
        files = channel.get('devices')
        if not isinstance(files, list) or not files:
            raise ValueError('channel.devices 必须是非空文件列表')
        channel['devices'] = [validate_device(read_yaml(path.parent / file)) for file in files]
        ids = [device['slave_id'] for device in channel['devices']]
        if len(ids) != len(set(ids)):
            raise ValueError(f'{name}: 重复 Slave ID / Unit ID')
    return config
