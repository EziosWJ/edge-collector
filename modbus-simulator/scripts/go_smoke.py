"""Run against an already started simulator with unchanged default YAML."""
from pathlib import Path
import os
import shutil
import subprocess
import tempfile

root = Path(__file__).resolve().parents[2]
backend = root / 'edge-collector-api'
module = 'github.com/EziosWJ/edge-collector/edge-collector-api'
with tempfile.TemporaryDirectory(prefix='modbus-go-smoke-') as directory:
    work = Path(directory)
    original = (backend / 'go.mod').read_text()
    original = original.replace(f'module {module}', f'module {module}/simulatorcheck', 1)
    (work / 'go.mod').write_text(original + f'\nrequire {module} v0.0.0\nreplace {module} => "{backend}"\n')
    shutil.copy(backend / 'go.sum', work / 'go.sum')
    shutil.copy(Path(__file__).with_suffix('.go'), work / 'main.go')
    env = dict(os.environ)
    env.setdefault('GOCACHE', '/tmp/modbus-go-build')
    subprocess.run(['go', 'run', '-mod=mod', '.'], cwd=work, env=env, check=True, timeout=180)
