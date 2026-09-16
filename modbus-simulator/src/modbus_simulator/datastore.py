"""Small validated adapter around the pinned PyModbus simulator datastore."""
import asyncio
import copy
from dataclasses import dataclass
from pymodbus.constants import ExcCodes
from pymodbus.exceptions import NoSuchIdException
from pymodbus.simulator.simcore import SimCore
from .device import Device
from .register import encode

FUNCTION_AREA = {1: 'coil', 2: 'discrete_input', 3: 'holding_register',
                 4: 'input_register', 5: 'coil', 6: 'holding_register',
                 15: 'coil', 16: 'holding_register'}
AREA_FUNCTION = {'coil': 1, 'discrete_input': 2, 'holding_register': 3, 'input_register': 4}


@dataclass(frozen=True)
class RequestRecord:
    """One datastore request, retained for deterministic fixture assertions."""

    device_id: int
    function: int
    address: int | None
    count: int | None
    values: tuple[int | bool, ...] | None = None


@dataclass(frozen=True)
class WriteRecord:
    """One successful write accepted by the simulated device."""

    device_id: int
    function: int
    address: int
    values: tuple[int | bool, ...]


@dataclass(frozen=True)
class ExceptionRecord:
    """One Modbus exception produced while processing a request."""

    device_id: int
    function: int
    address: int | None
    count: int | None
    code: int


class RequestJournal:
    """In-memory request journal used by protocol fixtures and acceptance tests."""

    def __init__(self):
        self.requests = []
        self.writes = []
        self.exceptions = []

    def record_request(self, device_id, function, address, count, values=None):
        self.requests.append(RequestRecord(device_id, function, address, count,
                                            None if values is None else tuple(values)))

    def record_write(self, device_id, function, address, values):
        self.writes.append(WriteRecord(device_id, function, address, tuple(values)))

    def record_exception(self, device_id, function, address, count, code):
        self.exceptions.append(ExceptionRecord(device_id, function, address, count,
                                                int(code)))

    def clear(self):
        self.requests.clear()
        self.writes.clear()
        self.exceptions.clear()


class Datastore:
    def __init__(self, configs, *, journal=None):
        self.devices = {c['slave_id']: Device(c) for c in configs}
        self.models = [device.model for device in self.devices.values()]
        self.journal = journal or RequestJournal()
        # SimCore is the 3.15 server's own backend, isolated here for upgrades.
        self.core = SimCore(self.models)

    def device_ids(self):
        return list(self.devices)

    def record_request(self, device_id, function, address, count, values=None):
        self.journal.record_request(device_id, function, address, count, values)

    def record_exception(self, device_id, function, address, count, code):
        self.journal.record_exception(device_id, function, address, count, code)

    async def prepare(self, device_id, function, address, count):
        if device_id not in self.devices:
            raise NoSuchIdException(f'unknown slave/unit {device_id}')
        device = self.devices[device_id]
        area = FUNCTION_AREA.get(function)
        if area is None:
            return ExcCodes.ILLEGAL_FUNCTION
        if count < 1 or not all(a in device.addresses[area] for a in range(address, address + count)):
            return ExcCodes.ILLEGAL_ADDRESS
        delay = device.config.get('behavior', {}).get('response_delay_ms', 0)
        if delay:
            await asyncio.sleep(delay / 1000)
        for simulation in device.simulations:
            if simulation.advance():
                reg = simulation.register
                await self.core.async_setValues(device_id, AREA_FUNCTION[reg['area']],
                                                reg['address'], encode(reg, simulation.value))
        return None

    async def async_getValues(self, device_id, func_code, address, count=1):
        self.record_request(device_id, func_code, address, count)
        if error := await self.prepare(device_id, func_code, address, count):
            self.record_exception(device_id, func_code, address, count, error)
            return error
        values = await self.core.async_getValues(device_id, func_code, address, count)
        if isinstance(values, ExcCodes):
            self.record_exception(device_id, func_code, address, count, values)
        return values

    async def async_setValues(self, device_id, func_code, address, values):
        values = tuple(values)
        self.record_request(device_id, func_code, address, len(values), values)
        if error := await self.prepare(device_id, func_code, address, len(values)):
            self.record_exception(device_id, func_code, address, len(values), error)
            return error
        result = await self.core.async_setValues(device_id, func_code, address, list(values))
        if result:
            self.record_exception(device_id, func_code, address, len(values), result)
        else:
            self.journal.record_write(device_id, func_code, address, values)
        return result


def make_datastore(configs, *, journal=None, fixture_options=None):
    """Build a generic store or a named reusable fixture store from device config."""
    if len(configs) == 1 and configs[0].get('fixture') == 'znck_i':
        from .fixtures.znck_i import ZnckIFixture
        config = copy.deepcopy(configs[0])
        options = fixture_options or {}
        if 'initial_fault_code' in options:
            value = options['initial_fault_code']
            if type(value) is not int or not 0 <= value <= 65535:
                raise ValueError('initial_fault_code 必须为 0..65535 整数')
            for register in config['registers']:
                if register['address'] == 8166:
                    register['value'] = value
                    break
        return ZnckIFixture(journal=journal, config=config)
    return Datastore(configs, journal=journal)
