"""Own a native PTY and a stable alias, with per-alias process locking."""
import fcntl
import os
from pathlib import Path
import pty
import tty


class Pty:
    def __init__(self, alias):
        self.alias = Path(alias)
        self.master = self.slave = self.lock = None
        self.slave_name = None

    def open(self):
        try:
            self.lock = os.open(str(self.alias) + '.lock', os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
            fcntl.flock(self.lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            if self.alias.exists() and not self.alias.is_symlink():
                raise ValueError(f'拒绝覆盖普通文件: {self.alias}')
            self.master, self.slave = pty.openpty()
            tty.setraw(self.slave)
            os.set_blocking(self.master, False)
            self.slave_name = os.ttyname(self.slave)
            temporary = self.alias.with_name(self.alias.name + f'.{os.getpid()}.tmp')
            try:
                temporary.symlink_to(self.slave_name)
                temporary.replace(self.alias)
            finally:
                if temporary.is_symlink():
                    temporary.unlink()
            return self
        except BaseException:
            self.close()
            raise

    def close(self):
        if self.slave_name and self.alias.is_symlink() and os.readlink(self.alias) == self.slave_name:
            self.alias.unlink()
        for attr in ('master', 'slave', 'lock'):
            fd = getattr(self, attr)
            if fd is not None:
                os.close(fd)
                setattr(self, attr, None)
        # Keep the lock inode: removing it allows races with other processes.
