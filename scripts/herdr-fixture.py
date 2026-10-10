#!/usr/bin/env python3
"""Own a disposable Herdr server/client and resize its real terminal through a PTY."""
import fcntl
import json
import os
from pathlib import Path
import pty
import select
import shlex
import signal
import socket
import struct
import subprocess
import sys
import termios
import time


def environment(work):
    env = {k: v for k, v in os.environ.items() if not k.startswith('HERDR_')}
    env.update(HERDR_CONFIG_PATH=str(work / 'config.toml'),
               HERDR_SOCKET_PATH=str(work / 'herdr.sock'),
               XDG_CONFIG_HOME=str(work / 'config'), XDG_DATA_HOME=str(work / 'data'),
               XDG_STATE_HOME=str(work / 'state'), TERM='xterm-256color')
    return env


def call(work, *args):
    return subprocess.run(['herdr', *args], env=environment(work), capture_output=True,
                          text=True, timeout=20)


def request(work, payload):
    with socket.socket(socket.AF_UNIX) as conn:
        conn.settimeout(20)
        conn.connect(str(work / 'control.sock'))
        conn.sendall(json.dumps(payload).encode() + b'\n')
        data = b''
        while b'\n' not in data:
            chunk = conn.recv(65536)
            if not chunk:
                raise RuntimeError('fixture controller disconnected')
            data += chunk
        result = json.loads(data)
        if 'error' in result:
            raise RuntimeError(result['error'])
        return result


def serve(work, width, height):
    env = environment(work)
    server = client = None
    master = slave = None
    def terminate(*_):
        raise KeyboardInterrupt
    signal.signal(signal.SIGTERM, terminate)
    signal.signal(signal.SIGINT, terminate)
    try:
        server = subprocess.Popen(['herdr', 'server'], env=env,
                                  stdout=sys.stdout, stderr=sys.stderr)
        deadline = time.monotonic() + 10
        while True:
            status = call(work, 'status', '--json')
            if status.returncode == 0 and json.loads(status.stdout)['server']['running']:
                break
            if server.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError('isolated Herdr server failed to start')
            time.sleep(.1)
        master, slave = pty.openpty()
        def resize(w, h):
            # Hidden sidebar leaves a chrome row; Herdr reserves a right-edge column.
            fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', h + 1, w + 1, 0, 0))
            if client is not None:
                client.send_signal(signal.SIGWINCH)
        resize(width, height)
        def controlling_terminal():
            os.setsid()
            fcntl.ioctl(slave, termios.TIOCSCTTY, 0)
        client = subprocess.Popen(['herdr'], env=env, stdin=slave, stdout=slave,
                                  stderr=slave, preexec_fn=controlling_terminal)
        with socket.socket(socket.AF_UNIX) as controller:
            controller.bind(str(work / 'control.sock'))
            controller.listen()
            running = True
            while running:
                if client.poll() is not None or server.poll() is not None:
                    raise RuntimeError('isolated Herdr process exited')
                ready, _, _ = select.select([master, controller], [], [], .1)
                if master in ready:
                    os.read(master, 65536)  # continuously drain the rendering client
                if controller in ready:
                    with controller.accept()[0] as conn:
                        conn.settimeout(10)
                        data = b''
                        while b'\n' not in data:
                            chunk = conn.recv(4096)
                            if not chunk:
                                raise RuntimeError('fixture controller request disconnected')
                            data += chunk
                        try:
                            payload = json.loads(data)
                            if payload['action'] == 'resize':
                                resize(int(payload['width']), int(payload['height']))
                            elif payload['action'] == 'stop':
                                running = False
                            elif payload['action'] != 'ping':
                                raise ValueError('unknown action')
                            conn.sendall(b'{"ok":true}\n')
                        except Exception as error:
                            conn.sendall(json.dumps({'error': str(error)}).encode() + b'\n')
    except KeyboardInterrupt:
        pass
    finally:
        # This environment always targets the socket under our temporary directory.
        if server is not None:
            try:
                call(work, 'server', 'stop')
            except Exception:
                pass
        for proc in (client, server):
            if proc is not None and proc.poll() is None:
                proc.terminate()
                try:
                    proc.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    proc.wait()
        for fd in (master, slave):
            if fd is not None:
                os.close(fd)
        (work / 'control.sock').unlink(missing_ok=True)


def main():
    action, directory, *args = sys.argv[1:]
    work = Path(directory).resolve()
    if action == 'serve':
        serve(work, int(args[0]), int(args[1]))
    elif action == 'start':
        if work.exists():
            raise RuntimeError('fixture server directory already exists')
        work.mkdir(mode=0o700, parents=True)
        (work / 'config.toml').write_text('''onboarding = false
[terminal]
default_shell = "/bin/bash"
shell_mode = "non_login"
[ui]
sidebar_start_collapsed = true
sidebar_collapsed_mode = "hidden"
[experimental]
allow_nested = true
''')
        with (work / 'fixture.log').open('w') as log:
            proc = subprocess.Popen([sys.executable, __file__, 'serve', str(work), *args],
                                    stdout=log, stderr=log, start_new_session=True)
        (work / 'owner.pid').write_text(str(proc.pid))
        deadline = time.monotonic() + 15
        while True:
            try:
                request(work, {'action': 'ping'})
                break
            except (OSError, RuntimeError):
                if proc.poll() is not None or time.monotonic() > deadline:
                    proc.terminate()
                    proc.wait(timeout=5)
                    raise RuntimeError(f'Herdr fixture startup failed; inspect {work / "fixture.log"}')
                time.sleep(.1)
    elif action == 'resize':
        width, height = map(int, args)
        if not (20 <= width <= 500 and 10 <= height <= 200):
            raise ValueError('size must be 20..500 columns and 10..200 rows')
        request(work, {'action': 'resize', 'width': width, 'height': height})
    elif action == 'stop':
        if (work / 'control.sock').exists():
            request(work, {'action': 'stop'})
            deadline = time.monotonic() + 10
            while (work / 'control.sock').exists():
                if time.monotonic() > deadline:
                    raise RuntimeError('fixture cleanup timed out')
                time.sleep(.1)
    elif action == 'env':
        env = environment(work)
        for key in ('HERDR_CONFIG_PATH', 'HERDR_SOCKET_PATH', 'XDG_CONFIG_HOME', 'XDG_DATA_HOME', 'XDG_STATE_HOME'):
            print(f'export {key}={shlex.quote(env[key])}')
    else:
        raise ValueError('unknown action')

if __name__ == '__main__':
    main()
