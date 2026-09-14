"""Small validated adapter around the pinned PyModbus simulator datastore."""
import asyncio
from pymodbus.constants import ExcCodes
from pymodbus.exceptions import NoSuchIdException
from pymodbus.simulator.simcore import SimCore
from .device import Device
from .register import encode

FUNCTION_AREA = {1: 'coil', 2: 'discrete_input', 3: 'holding_register',
                 4: 'input_register', 5: 'coil', 6: 'holding_register',
                 15: 'coil', 16: 'holding_register'}
AREA_FUNCTION = {'coil': 1, 'discrete_input': 2, 'holding_register': 3, 'input_register': 4}


class Datastore:
    def __init__(self, configs):
        self.devices = {c['slave_id']: Device(c) for c in configs}
        self.models = [device.model for device in self.devices.values()]
        # SimCore is the 3.15 server's own backend, isolated here for upgrades.
        self.core = SimCore(self.models)

    def device_ids(self):
        return list(self.devices)

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
        if error := await self.prepare(device_id, func_code, address, count):
            return error
        return await self.core.async_getValues(device_id, func_code, address, count)

    async def async_setValues(self, device_id, func_code, address, values):
        if error := await self.prepare(device_id, func_code, address, len(values)):
            return error
        return await self.core.async_setValues(device_id, func_code, address, values)
