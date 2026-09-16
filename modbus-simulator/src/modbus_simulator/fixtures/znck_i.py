"""Deterministic ZNCK-I raw-register fixture for dynamic transaction tests.

This is deliberately a raw Modbus fixture.  The values are stable test data;
the simulator does not assign vendor or engineering meanings to them.
"""
import copy

from pymodbus.constants import ExcCodes

from ..config import validate_device
from ..datastore import Datastore

UNIT_ID = 1
QUERY_INDEX_ADDRESS = 8120
DETAIL_START_ADDRESS = 8121
DETAIL_END_ADDRESS = 8137
DETAIL_QUANTITY = DETAIL_END_ADDRESS - DETAIL_START_ADDRESS + 1
FAULT_CODE_ADDRESS = 8166

# The first detail word is intentionally non-zero only after query index 0 is
# written.  The remaining words are fixed raw values for event-key assertions.
DEFAULT_DETAIL_REGISTERS = (
    42, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    2026, 9, 16, 10, 30, 42, 1,
)


def znck_i_device_config():
    """Return a fresh validated Unit 1 device configuration."""
    registers = [
        {
            'name': 'query_index',
            'description': 'raw query index',
            'area': 'holding_register',
            'address': QUERY_INDEX_ADDRESS,
            'type': 'uint16',
            'value': 0,
        },
    ]
    registers.extend({
        'name': f'detail_{address}',
        'description': 'raw dynamic detail word',
        'area': 'holding_register',
        'address': address,
        'type': 'uint16',
        'value': 0,
    } for address in range(DETAIL_START_ADDRESS, DETAIL_END_ADDRESS + 1))
    registers.append({
        'name': 'fault_code',
        'description': 'raw static fault-code word',
        'area': 'holding_register',
        'address': FAULT_CODE_ADDRESS,
        'type': 'uint16',
        'value': 0,
    })
    return validate_device({
        'name': 'ZNCK-I fixture',
        'fixture': 'znck_i',
        'slave_id': UNIT_ID,
        'registers': registers,
    })


class ZnckIFixture(Datastore):
    """Unit 1 datastore with the ZNCK-I index-0 refresh behavior.

    Before FC16/FC06 writes query index 0, the detail block is zeroed.  A
    successful write refreshes 8121..8137 with fixed raw values.  All client
    requests, successful writes, and Modbus exceptions are available through
    ``journal`` inherited from :class:`Datastore`.
    """

    def __init__(self, *, journal=None, config=None, detail_values=None):
        if config is None:
            config = znck_i_device_config()
        else:
            config = copy.deepcopy(config)
            validate_device(config)
        detail_values = (DEFAULT_DETAIL_REGISTERS if detail_values is None
                         else tuple(detail_values))
        if len(detail_values) != DETAIL_QUANTITY or any(
                type(value) is not int or not 0 <= value <= 65535
                for value in detail_values):
            raise ValueError(f'detail_values 必须是 {DETAIL_QUANTITY} 个 uint16')
        self.detail_values = tuple(detail_values)
        self.query_indices = []
        super().__init__([config], journal=journal)

    async def async_setValues(self, device_id, func_code, address, values):
        values = tuple(values)
        is_query_write = (device_id == UNIT_ID and
                          func_code in (6, 16) and
                          address == QUERY_INDEX_ADDRESS)
        if is_query_write and values != (0,):
            self.record_request(device_id, func_code, address, len(values), values)
            self.record_exception(device_id, func_code, address, len(values),
                                  ExcCodes.ILLEGAL_VALUE)
            return ExcCodes.ILLEGAL_VALUE

        result = await super().async_setValues(device_id, func_code, address, values)
        if result is None and is_query_write:
            await self.core.async_setValues(UNIT_ID, 3, DETAIL_START_ADDRESS,
                                             list(self.detail_values))
            self.query_indices.append(values[0])
        return result

    async def set_fault_code(self, value):
        """Set the static fault-code word through a normal Modbus write."""
        if type(value) is not int or not 0 <= value <= 65535:
            raise ValueError('fault code 必须为 0..65535 整数')
        return await self.async_setValues(UNIT_ID, 6, FAULT_CODE_ADDRESS, [value])

    async def read_fault_code(self):
        """Read the static fault-code word through FC03 semantics."""
        return await self.async_getValues(UNIT_ID, 3, FAULT_CODE_ADDRESS, 1)

    async def read_details(self):
        """Read the complete raw dynamic detail block through FC03 semantics."""
        return await self.async_getValues(UNIT_ID, 3, DETAIL_START_ADDRESS,
                                          DETAIL_QUANTITY)

    async def reset_details(self):
        """Reset the detail response without adding a client request record."""
        result = await self.core.async_setValues(UNIT_ID, 3, DETAIL_START_ADDRESS,
                                                 [0] * DETAIL_QUANTITY)
        if result:
            raise RuntimeError(f'cannot reset ZNCK-I detail fixture: {result}')
        self.query_indices.clear()
