"""Lazy clock-based simulation, without per-register background tasks."""
import random
import time


class Simulation:
    def __init__(self, register):
        self.register = register
        self.value = register['value']
        self.last = time.monotonic()

    def advance(self, now=None):
        spec = self.register.get('simulation', {})
        mode = spec.get('type', 'fixed')
        now = time.monotonic() if now is None else now
        interval = spec.get('interval', 1)
        ticks = int((now - self.last) / interval)
        if mode == 'fixed' or ticks <= 0:
            return False
        self.last += ticks * interval
        lo, hi = spec['min'], spec['max']
        if mode == 'random':
            scale = self.register.get('scale', 1)
            if self.register['type'] in ('float32', 'float64'):
                self.value = random.uniform(lo, hi)
            else:
                self.value = random.randint(round(lo / scale), round(hi / scale)) * scale
        else:
            step = spec.get('step', 1) * (1 if mode == 'increment' else -1)
            self.value = min(hi, max(lo, self.value + ticks * step))
        return True
