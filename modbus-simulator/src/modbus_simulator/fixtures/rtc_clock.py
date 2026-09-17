"""Running raw RTC fixture for Starlark clock-synchronization tests."""
import copy
import time

from pymodbus.constants import ExcCodes

from ..config import validate_device
from ..datastore import Datastore

TIME_START_ADDRESS = 100
HOUR_ADDRESS = 100
MINUTE_ADDRESS = 101
SECOND_ADDRESS = 102
TIME_QUANTITY = 3
SECONDS_PER_DAY = 24 * 60 * 60


def _seconds_of_day(hour, minute, second):
    if type(hour) is not int or not 0 <= hour <= 23:
        raise ValueError('hour 必须为 0..23 整数')
    if type(minute) is not int or not 0 <= minute <= 59:
        raise ValueError('minute 必须为 0..59 整数')
    if type(second) is not int or not 0 <= second <= 59:
        raise ValueError('second 必须为 0..59 整数')
    return hour * 3600 + minute * 60 + second


def _split_seconds(value):
    value %= SECONDS_PER_DAY
    return value // 3600, (value % 3600) // 60, value % 60


def _initial_time(config):
    values = {}
    for register in config['registers']:
        if (register.get('area') == 'holding_register' and
                register.get('address') in (HOUR_ADDRESS, MINUTE_ADDRESS,
                                             SECOND_ADDRESS)):
            values[register['address']] = register.get('value')
    if set(values) != {HOUR_ADDRESS, MINUTE_ADDRESS, SECOND_ADDRESS}:
        raise ValueError('rtc_clock fixture 必须配置 holding register 100/101/102')
    return values[HOUR_ADDRESS], values[MINUTE_ADDRESS], values[SECOND_ADDRESS]


class RtcClockFixture(Datastore):
    """Three-register clock that keeps running after an FC16 synchronization.

    Holding registers 100/101/102 represent hour/minute/second. Only one
    complete FC16 write starting at 100 is accepted for clock adjustment;
    partial writes are rejected with ILLEGAL_VALUE. Reads refresh the three raw
    registers from the elapsed monotonic time before serving the request.
    """

    def __init__(self, *, journal=None, config, clock=None):
        config = copy.deepcopy(config)
        validate_device(config)
        hour, minute, second = _initial_time(config)
        self.unit_id = config['slave_id']
        self._clock = clock or time.monotonic
        self._base_seconds = _seconds_of_day(hour, minute, second)
        self._base_tick = float(self._clock())
        super().__init__([config], journal=journal)

    def _current_values(self):
        elapsed = max(0, int(float(self._clock()) - self._base_tick))
        return _split_seconds(self._base_seconds + elapsed)

    async def _refresh_registers(self):
        await self.core.async_setValues(
            self.unit_id, 3, TIME_START_ADDRESS, list(self._current_values()))

    @staticmethod
    def _touches_time(address, count):
        end = address + count
        return address < TIME_START_ADDRESS + TIME_QUANTITY and end > TIME_START_ADDRESS

    async def async_getValues(self, device_id, func_code, address, count=1):
        if (device_id == self.unit_id and func_code == 3 and
                self._touches_time(address, count)):
            await self._refresh_registers()
        return await super().async_getValues(device_id, func_code, address, count)

    async def async_setValues(self, device_id, func_code, address, values):
        values = tuple(values)
        if (device_id == self.unit_id and func_code in (6, 16) and
                self._touches_time(address, len(values))):
            valid_shape = (func_code == 16 and address == TIME_START_ADDRESS and
                           len(values) == TIME_QUANTITY)
            if not valid_shape:
                self.record_request(device_id, func_code, address, len(values), values)
                self.record_exception(device_id, func_code, address, len(values),
                                      ExcCodes.ILLEGAL_VALUE)
                return ExcCodes.ILLEGAL_VALUE
            try:
                new_seconds = _seconds_of_day(*values)
            except ValueError:
                self.record_request(device_id, func_code, address, len(values), values)
                self.record_exception(device_id, func_code, address, len(values),
                                      ExcCodes.ILLEGAL_VALUE)
                return ExcCodes.ILLEGAL_VALUE

            result = await super().async_setValues(device_id, func_code, address, values)
            if result is None:
                self._base_seconds = new_seconds
                self._base_tick = float(self._clock())
            return result

        return await super().async_setValues(device_id, func_code, address, values)

    async def read_time(self):
        """Read hour/minute/second using normal FC03 semantics."""
        return await self.async_getValues(self.unit_id, 3, TIME_START_ADDRESS,
                                          TIME_QUANTITY)
