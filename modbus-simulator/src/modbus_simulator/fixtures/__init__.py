"""Reusable device fixtures for simulator and acceptance tests."""

from .znck_i import (
    DETAIL_END_ADDRESS,
    DETAIL_QUANTITY,
    DETAIL_START_ADDRESS,
    FAULT_CODE_ADDRESS,
    QUERY_INDEX_ADDRESS,
    UNIT_ID,
    DEFAULT_DETAIL_REGISTERS,
    ZnckIFixture,
    znck_i_device_config,
)

__all__ = [
    'DEFAULT_DETAIL_REGISTERS',
    'DETAIL_END_ADDRESS',
    'DETAIL_QUANTITY',
    'DETAIL_START_ADDRESS',
    'FAULT_CODE_ADDRESS',
    'QUERY_INDEX_ADDRESS',
    'UNIT_ID',
    'ZnckIFixture',
    'znck_i_device_config',
]
