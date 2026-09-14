"""Build PyModbus device blocks; no transport-specific register logic."""
from pymodbus.simulator import SimData, SimDevice, DataType
from .register import AREAS, encode
from .simulation import Simulation


class Device:
    def __init__(self, config):
        self.config = config
        self.id = config['slave_id']
        self.simulations = [Simulation(reg) for reg in config['registers']]
        self.addresses = {area: set() for area in AREAS}
        blocks = []
        for area in AREAS:
            bits = area in AREAS[:2]
            entries = []
            for reg in config['registers']:
                if reg['area'] != area:
                    continue
                for offset, value in enumerate(encode(reg)):
                    address = reg['address'] + offset
                    self.addresses[area].add(address)
                    entries.append(SimData(address, values=value,
                                           datatype=DataType.BITS if bits else DataType.UINT16))
            # SimDevice 3.15 requires nonempty blocks. The context rejects all
            # unconfigured addresses, including these allocation placeholders.
            blocks.append(entries or [SimData(0, values=False if bits else 0,
                                              datatype=DataType.BITS if bits else DataType.INVALID)])
        self.model = SimDevice(self.id, tuple(blocks))
