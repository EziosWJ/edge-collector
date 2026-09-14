import unittest
from modbus_simulator.register import encode
from modbus_simulator.simulation import Simulation


class RegisterTest(unittest.TestCase):
    def test_types_and_orders(self):
        for byte, word, expected in [('big', 'big', [0x1234, 0x5678]),
                                     ('little', 'big', [0x3412, 0x7856]),
                                     ('big', 'little', [0x5678, 0x1234]),
                                     ('little', 'little', [0x7856, 0x3412])]:
            self.assertEqual(encode(dict(type='uint32', value=0x12345678,
                                         byte_order=byte, word_order=word)), expected)
        for kind, value, expected in [('bool', True, [True]), ('int16', -2, [65534]),
                                      ('int32', -2, [65535, 65534]),
                                      ('float32', 1.0, [0x3f80, 0]),
                                      ('float64', 1.0, [0x3ff0, 0, 0, 0])]:
            self.assertEqual(encode(dict(type=kind, value=value)), expected)
        self.assertEqual(encode(dict(type='uint16', value=12.5, scale=0.1)), [125])

    def test_simulation(self):
        for mode, expected in [('fixed', 5), ('increment', 9), ('decrement', 1)]:
            sim = Simulation(dict(type='uint16', value=5,
                                  simulation=dict(type=mode, interval=1, step=2, min=0, max=10)))
            sim.advance(sim.last + 2)
            self.assertEqual(sim.value, expected)
            sim.advance(sim.last + 100)
            self.assertEqual(sim.value, {'fixed': 5, 'increment': 10, 'decrement': 0}[mode])
        sim = Simulation(dict(type='uint16', value=5, scale=0.1,
                              simulation=dict(type='random', interval=1, min=2, max=8)))
        for _ in range(30):
            sim.advance(sim.last + 1)
            self.assertTrue(2 <= sim.value <= 8)
            encode(sim.register, sim.value)
