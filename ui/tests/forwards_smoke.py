#!/usr/bin/env python3
"""Opt-in Linux native forward probe; real server transport, no CLI/engine/home.

Build the existing standalone server test binary, then run under dbus-run-session.
All pass conditions observe controls, HTTP/WS data, process exit or released sockets.
"""

import argparse
import base64
import hashlib
import http.server
import json
import os
from pathlib import Path
import queue
import shutil
import signal
import select
import socket
import subprocess
import tempfile
import threading
import time
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[2]
EVENTS = queue.Queue()
CONTINUE = threading.Event()
HOLD = threading.Event()
PAGE = b'<!doctype html><title>Forward native fixture</title><textarea aria-label="App input"></textarea><button onclick="window.open(\'/child\')">App popup</button><script src="/root.js"></script>'
SCRIPT = b"""
const report = value => fetch('/evidence', {method:'POST',body:JSON.stringify({...value,path:location.pathname,origin:location.origin})});
report({loaded:true,token:localStorage.getItem('omo.token'),native:typeof __OMO_NATIVE_UI__,wrapped:typeof window.__TAURI__,ipc:typeof window.__TAURI_INTERNALS__?.ipc});
// Main-window raw IPC checks the native ACL. Related WebKit child IPC replies
// are not delivered; the compiled native ACL test covers every child label and origin.
if (location.pathname !== '/child') Promise.all(['get_setup','preview_forward','expose_forward','stop_forward'].map(command=>Promise.resolve().then(()=>window.__TAURI_INTERNALS__.invoke(command,{id:'ignored',port:'1234'})).then(()=>({granted:true}),error=>({error:String(error)})))).then(denied=>report({denied}));
fetch('/asset').then(r=>r.text()).then(asset=>report({asset}));
const stream=new EventSource('/stream'); stream.onmessage=e=>{report({stream:e.data});if(e.data==='second')stream.close();};
const ws=new WebSocket(location.origin.replace('http:','ws:')+'/socket');ws.onopen=()=>ws.send('hello');ws.onmessage=e=>{report({socket:e.data});ws.close();};
document.addEventListener('keydown',e=>{if(e.ctrlKey&&e.shiftKey&&e.key.toLowerCase()==='p'){e.preventDefault();report({shortcut:true});}});
"""


class Upstream(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *args):
        pass

    def send(self, data, kind="text/html"):
        self.send_response(200)
        self.send_header("Content-Type", kind)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        assert not self.headers.get("Authorization"), (
            "board credentials entered app request"
        )
        if self.path == "/root.js":
            self.send(SCRIPT, "text/javascript")
        elif self.path == "/asset":
            self.send(b"root asset", "text/plain")
        elif self.path in ["/stream", "/held"]:
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Connection", "close")
            self.end_headers()
            self.close_connection = True
            self.wfile.write(b"data: first\n\n")
            self.wfile.flush()
            (HOLD if self.path == "/held" else CONTINUE).wait()
            try:
                self.wfile.write(b"data: second\n\n")
                self.wfile.flush()
            except (BrokenPipeError, ConnectionResetError):
                pass
        elif self.path == "/socket":
            key = self.headers["Sec-WebSocket-Key"]
            accept = base64.b64encode(
                hashlib.sha1(
                    (key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").encode()
                ).digest()
            ).decode()
            self.send_response(101)
            self.send_header("Upgrade", "websocket")
            self.send_header("Connection", "Upgrade")
            self.send_header("Sec-WebSocket-Accept", accept)
            self.end_headers()
            frame = self.rfile.read(2)
            assert frame[0] == 129
            length = frame[1] & 127
            assert length < 126 and frame[1] & 128
            mask = self.rfile.read(4)
            data = self.rfile.read(length)
            data = bytes(b ^ mask[i % 4] for i, b in enumerate(data))
            assert data == b"hello"
            self.wfile.write(bytes([129, len(data)]) + data)
            self.wfile.flush()
            close = self.rfile.read(2)
            if close:
                assert close[0] & 15 == 8
                self.rfile.read(4 + (close[1] & 127))
                self.wfile.write(b"\x88\x00")
                self.wfile.flush()
            self.close_connection = True
        else:
            self.send(PAGE)

    def do_POST(self):
        assert self.path == "/evidence"
        EVENTS.put(json.loads(self.rfile.read(int(self.headers["Content-Length"]))))
        self.send(b"{}", "application/json")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--fixture", required=True)
    parser.add_argument("--app", default=str(ROOT / "ui/target/debug/omo-ui"))
    parser.add_argument(
        "--host", help="Extracted native dependencies, used through a read-only overlay"
    )
    args = parser.parse_args()
    import gi

    gi.require_version("Atspi", "2.0")
    from gi.repository import Atspi, GLib

    children = []
    threads = []
    logs = []
    fds = []
    stop_url = None
    bus = None
    temp = Path(tempfile.mkdtemp(prefix="forward-native-", dir=ROOT / "bin"))
    runtime = Path(tempfile.mkdtemp(prefix="omo-ui-runtime-"))
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Upstream)
    server_thread = threading.Thread(target=server.serve_forever)
    server_thread.start()
    try:
        env = {k: v for k, v in os.environ.items() if not k.upper().startswith("OMO_")}
        for key in [
            "HOME",
            "XDG_CONFIG_HOME",
            "XDG_DATA_HOME",
            "XDG_CACHE_HOME",
            "XDG_RUNTIME_DIR",
            "TMPDIR",
            "TMP",
            "TEMP",
            "GOTMPDIR",
        ]:
            env[key] = str(runtime if key == "XDG_RUNTIME_DIR" else temp / key)
            if key != "XDG_RUNTIME_DIR":
                Path(env[key]).mkdir()
        env.update(
            GTK_USE_PORTAL="0",
            GDK_BACKEND="x11",
            NO_AT_BRIDGE="0",
            GSETTINGS_BACKEND="memory",
        )
        if args.host:
            env["LD_LIBRARY_PATH"] = str(Path(args.host).resolve() / "usr/lib")

        def launch(command, extra=None, pass_fds=()):
            log = open(temp / f"child-{len(children)}.log", "w+")
            logs.append(log)
            child = subprocess.Popen(
                command,
                env={**env, **(extra or {})},
                stdout=log,
                stderr=log,
                start_new_session=True,
                pass_fds=pass_fds,
            )
            children.append(child)
            return child, log

        # No shell/launcher owns the app: Bubblewrap execs it, preserving process ownership.
        def host(command):
            if not args.host:
                return command
            return [
                "bwrap",
                "--ro-bind",
                "/",
                "/",
                "--dev-bind",
                "/dev",
                "/dev",
                "--bind",
                str(temp),
                str(temp),
                "--overlay-src",
                "/usr/lib",
                "--overlay-src",
                str(Path(args.host).resolve() / "usr/lib"),
                "--ro-overlay",
                "/usr/lib",
                "--die-with-parent",
                "--",
                *command,
            ]

        read_fd, write_fd = os.pipe()
        fds.extend([read_fd, write_fd])
        xvfb_bin = (
            str(Path(args.host).resolve() / "usr/bin/Xvfb") if args.host else "Xvfb"
        )
        xvfb, _ = launch(
            [
                xvfb_bin,
                "-displayfd",
                str(write_fd),
                "-screen",
                "0",
                "1280x1000x24",
                "-nolisten",
                "tcp",
            ],
            pass_fds=(write_fd,),
        )
        os.close(write_fd)
        fds.remove(write_fd)
        with os.fdopen(read_fd) as ready:
            env["DISPLAY"] = ":" + ready.readline().strip()
        fds.remove(read_fd)
        assert env["DISPLAY"] != ":", "Xvfb exited before readiness"
        os.environ.update(
            {
                key: env[key]
                for key in [
                    "DISPLAY",
                    "HOME",
                    "XDG_CONFIG_HOME",
                    "XDG_DATA_HOME",
                    "XDG_CACHE_HOME",
                    "XDG_RUNTIME_DIR",
                    "TMPDIR",
                    "NO_AT_BRIDGE",
                ]
            }
        )
        import dbus

        bus = dbus.SessionBus()
        daemon = dbus.Interface(
            bus.get_object("org.freedesktop.DBus", "/org/freedesktop/DBus"),
            "org.freedesktop.DBus",
        )
        bus_pid = int(daemon.GetConnectionUnixProcessID("org.freedesktop.DBus"))
        assert (
            str(bus_pid)
            in Path(f"/proc/{os.getppid()}/task/{os.getppid()}/children")
            .read_text()
            .split()
        ), "Use a private dbus-run-session"
        daemon.UpdateActivationEnvironment(
            {
                key: env[key]
                for key in [
                    "DISPLAY",
                    "HOME",
                    "XDG_CONFIG_HOME",
                    "XDG_DATA_HOME",
                    "XDG_CACHE_HOME",
                    "XDG_RUNTIME_DIR",
                    "TMPDIR",
                    "NO_AT_BRIDGE",
                ]
            }
        )
        fixture_log = queue.Queue()
        fixture = subprocess.Popen(
            [args.fixture, "-test.run=^TestProxySubdomainBrowserFixture$"],
            env={**env, "PROXY_BROWSER_FIXTURE": "1"},
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            start_new_session=True,
        )
        children.append(fixture)

        def read_fixture():
            for line in fixture.stdout:
                fixture_log.put(line)
            fixture_log.put(None)

        thread = threading.Thread(target=read_fixture)
        thread.start()
        threads.append(thread)
        while True:
            line = fixture_log.get(timeout=60)
            if line is None:
                raise AssertionError("fixture exited before readiness")
            if line.startswith("PROXY_BROWSER_FIXTURES "):
                record = next(
                    f
                    for f in json.loads(line.split(" ", 1)[1])
                    if f["variant"] == "http"
                )
                break
        origin = record["board"]
        stop_url = record["stop"]
        token = record["token"]

        def api(path, method="GET", body=None):
            request = urllib.request.Request(
                origin + path,
                method=method,
                data=json.dumps(body).encode() if body is not None else None,
                headers={
                    "Authorization": "Bearer " + token,
                    "Content-Type": "application/json",
                },
            )
            with urllib.request.urlopen(request) as reply:
                return json.load(reply) if reply.status != 204 else None

        for forward in api("/api/proxy/forwards")["forwards"]:
            api("/api/proxy/forwards/" + forward["id"], "DELETE")
        forward = api(
            "/api/proxy/forwards",
            "POST",
            {
                "port": server.server_port,
                "name": "Native preview probe",
                "start_path": "/start?q=1",
            },
        )
        config = Path(env["XDG_CONFIG_HOME"]) / "dev.scolastico.omo.ui"
        config.mkdir()
        (config / "config.json").write_text(
            json.dumps(
                {
                    "id": str(uuid.uuid4()),
                    "mode": "direct",
                    "origin": origin,
                    "auth": "token",
                    "username": "",
                    "notifications": False,
                    "remember": False,
                }
            )
        )
        app, app_log = launch(host([str(Path(args.app).resolve())]))
        xdotool_bin = (
            str(Path(args.host).resolve() / "usr/bin/xdotool")
            if args.host
            else "xdotool"
        )

        def x(*command):
            return subprocess.check_output(
                [xdotool_bin, *command], env=env, text=True, timeout=60
            ).strip()

        changed = threading.Event()

        def tree(node):
            try:
                yield node
                for i in range(node.get_child_count()):
                    child = node.get_child_at_index(i)
                    if child is not None:
                        yield from tree(child)
            except GLib.GError:
                return

        def find(predicate):
            deadline = time.monotonic() + 60
            while True:
                for node in tree(Atspi.get_desktop(0)):
                    try:
                        if predicate(node):
                            return node
                    except GLib.GError:
                        pass
                if app.poll() is not None:
                    raise AssertionError("native app exited")
                if time.monotonic() > deadline:
                    raise AssertionError("accessible control not found")
                changed.wait(0.025)

        def close_window(window):
            from Xlib import X, display, protocol

            screen = display.Display(env["DISPLAY"])
            try:
                screen.send_event(
                    int(window),
                    protocol.event.ClientMessage(
                        window=int(window),
                        client_type=screen.intern_atom("WM_PROTOCOLS"),
                        data=(
                            32,
                            [
                                screen.intern_atom("WM_DELETE_WINDOW"),
                                X.CurrentTime,
                                0,
                                0,
                                0,
                            ],
                        ),
                    ),
                    event_mask=0,
                )
                screen.sync()
            finally:
                screen.close()

        def contains(node, text):
            if text in node.get_name():
                return True
            interface = node.get_text_iface()
            return interface is not None and text in Atspi.Text.get_text(
                interface, 0, -1
            )

        def button(name):
            return find(
                lambda n: (
                    n.get_role() == Atspi.Role.PUSH_BUTTON
                    and n.get_name() == name
                    and n.get_state_set().contains(Atspi.StateType.VISIBLE)
                    and n.get_state_set().contains(Atspi.StateType.ENABLED)
                )
            )

        setup = x("search", "--sync", "--name", "^omo ui · Connection$").splitlines()[0]
        x("windowfocus", "--sync", setup)
        secret = find(
            lambda n: (
                n.get_name() == "Login token"
                and n.get_role() == Atspi.Role.PASSWORD_TEXT
            )
        )
        secret.get_component_iface().grab_focus()
        x("type", "--clearmodifiers", token)
        find(
            lambda n: (
                n.get_name() == "Login token"
                and n.get_role() == Atspi.Role.PASSWORD_TEXT
                and n.get_text_iface().get_character_count() == len(token)
            )
        )
        print("Connecting native app.", flush=True)
        button("Connect").get_action_iface().do_action(0)
        button("Configure omo ui connection").get_action_iface().do_action(0)
        button("Refresh apps and status").get_action_iface().do_action(0)
        print("Opening native preview.", flush=True)
        button("Open preview").get_action_iface().do_action(0)
        evidence = []

        def event(key, value, path="/start"):
            deadline = time.monotonic() + 60
            while True:
                for item in evidence:
                    if (
                        key in item
                        and (value is None or item.get(key) == value)
                        and item.get("path") == path
                    ):
                        return item
                try:
                    item = EVENTS.get(timeout=max(0.001, deadline - time.monotonic()))
                except queue.Empty:
                    raise AssertionError(
                        f"Missing {key}={value!r} at {path}; observed {evidence!r}"
                    )
                evidence.append(item)

        loaded = event("loaded", True)
        print("Preview document loaded.", flush=True)
        assert (
            loaded["origin"] == "http://native-preview-probe-" + forward["id"] + ".omo"
        ), loaded
        assert (
            loaded["token"] is None
            and loaded["native"] == "undefined"
            and loaded["wrapped"] == "undefined"
        ), loaded
        denials = event("denied", None)
        assert all(
            "not allowed" in result.get("error", "").lower()
            for result in denials["denied"]
        ), denials
        event("asset", "root asset")
        event("stream", "first")
        event("socket", "hello")
        CONTINUE.set()
        event("stream", "second")
        preview = x(
            "search", "--sync", "--name", "Forward native fixture"
        ).splitlines()[0]
        x("windowfocus", "--sync", preview)
        x("key", "ctrl+shift+p")
        event("shortcut", True)
        print("Checking shortcuts and clipboard.", flush=True)
        text = find(
            lambda n: n.get_name() == "App input" and n.get_role() == Atspi.Role.ENTRY
        )
        text.get_component_iface().grab_focus()
        x("type", "--clearmodifiers", "clipboard probe")
        x("key", "ctrl+a", "ctrl+c", "BackSpace", "ctrl+v")
        find(
            lambda n: (
                n.get_name() == "App input"
                and n.get_role() == Atspi.Role.ENTRY
                and Atspi.Text.get_text(n.get_text_iface(), 0, -1) == "clipboard probe"
            )
        )
        button("App popup").get_action_iface().do_action(0)
        child_loaded = event("loaded", True, "/child")
        print("Child bridge:", child_loaded, flush=True)
        event("socket", "hello", "/child")
        assert (
            child_loaded["token"] is None
            and child_loaded["wrapped"] == "undefined"
            and child_loaded["native"] == "undefined"
        ), child_loaded
        # The family stays live until its last related view closes.
        windows = x("search", "--name", "Forward native fixture").splitlines()
        child = next(window for window in windows if window != preview)
        close_window(preview)
        x("windowfocus", "--sync", child)
        x("key", "ctrl+shift+p")
        event("shortcut", True, "/child")
        close_window(child)
        find(lambda n: contains(n, "No previews or local exposures are active."))
        button("Open preview").get_action_iface().do_action(0)
        while True:
            fresh = EVENTS.get(timeout=60)
            if fresh.get("loaded") and fresh.get("path") == "/start":
                break
        connection = x(
            "search", "--sync", "--name", "^omo ui · Connection$"
        ).splitlines()[0]
        x("windowfocus", "--sync", connection)
        occupied = socket.socket()
        occupied.bind(("127.0.0.1", 0))
        occupied.listen()
        port = occupied.getsockname()[1]

        def expose(value):
            field = find(
                lambda n: (
                    n.get_name() == "Local port"
                    and n.get_role() in [Atspi.Role.ENTRY, Atspi.Role.SPIN_BUTTON]
                )
            )
            assert field.get_component_iface().grab_focus()
            x("key", "ctrl+a")
            x("type", "--clearmodifiers", str(value))
            find(
                lambda n: (
                    n.get_name() == "Local port"
                    and n.get_role() in [Atspi.Role.ENTRY, Atspi.Role.SPIN_BUTTON]
                    and Atspi.Text.get_text(n.get_text_iface(), 0, -1) == str(value)
                )
            )
            print("Exposing local port.", flush=True)
            button("Expose on this computer").get_action_iface().do_action(0)

        expose(port)
        find(lambda n: contains(n, "Choose an available local port"))
        occupied.close()
        expose(port)
        find(lambda n: contains(n, f"127.0.0.1:{port}") and contains(n, "Listening"))
        with urllib.request.urlopen(f"http://127.0.0.1:{port}/asset") as reply:
            assert reply.read() == b"root asset"
        button("End exposure").get_action_iface().do_action(0)
        # Only the preview remains after this local exposure ends.
        find(lambda n: contains(n, "1 active"))
        # Keep a second exposure actively streaming across reconfiguration/disconnect.
        expose(port)
        find(lambda n: contains(n, f"127.0.0.1:{port}") and contains(n, "Listening"))

        def held_stream():
            stream = socket.create_connection(("127.0.0.1", port))
            stream.sendall(
                f"GET /held HTTP/1.1\r\nHost: 127.0.0.1:{port}\r\n\r\n".encode()
            )
            data = b""
            while b"data: first\n\n" not in data:
                data += stream.recv(4096)
            return stream

        stream = held_stream()
        button("Connect").get_action_iface().do_action(0)
        button("Configure omo ui connection")
        assert stream.recv(4096) == b"", "Reconfiguration retained an active stream"
        stream.close()
        with socket.socket() as rebound:
            rebound.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            rebound.bind(("127.0.0.1", port))
        button("Configure omo ui connection").get_action_iface().do_action(0)
        button("Refresh apps and status").get_action_iface().do_action(0)
        expose(port)
        find(lambda n: contains(n, f"127.0.0.1:{port}") and contains(n, "Listening"))
        stream = held_stream()
        button("Open preview").get_action_iface().do_action(0)
        # Observe a newly loaded preview rather than reusing its earlier event.
        while True:
            fresh = EVENTS.get(timeout=60)
            if fresh.get("loaded") and fresh.get("path") == "/start":
                break
        x("windowfocus", "--sync", connection)
        button("Disconnect").get_action_iface().do_action(0)
        find(lambda n: contains(n, "Choose your board to connect."))
        assert stream.recv(4096) == b"", "Disconnect retained an active stream"
        stream.close()
        with socket.socket() as rebound:
            rebound.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            rebound.bind(("127.0.0.1", port))
        print("Checking external connection exit cleanup.", flush=True)
        secret = find(
            lambda n: (
                n.get_name() == "Login token"
                and n.get_role() == Atspi.Role.PASSWORD_TEXT
            )
        )
        secret.get_component_iface().grab_focus()
        x("type", "--clearmodifiers", token)
        find(
            lambda n: (
                n.get_name() == "Login token"
                and n.get_role() == Atspi.Role.PASSWORD_TEXT
                and n.get_text_iface().get_character_count() == len(token)
            )
        )
        button("Connect").get_action_iface().do_action(0)
        button("Configure omo ui connection").get_action_iface().do_action(0)
        button("Refresh apps and status").get_action_iface().do_action(0)
        expose(port)
        find(lambda n: contains(n, f"127.0.0.1:{port}") and contains(n, "Listening"))
        stream = held_stream()
        native_windows = x("search", "--onlyvisible", "--name", "omo ui").splitlines()
        board = next(window for window in native_windows if window != connection)
        close_window(board)
        assert app.wait(timeout=60) == 0
        assert stream.recv(4096) == b"", "Exit retained an active stream"
        stream.close()
        with socket.socket() as rebound:
            rebound.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            rebound.bind(("127.0.0.1", port))
        assert fixture.poll() is None
        assert "forwards" in api("/api/proxy/forwards"), "External server was stopped"
        print(
            json.dumps(
                {
                    "platform": "Linux WebKitGTK",
                    "actualVirtualOrigin": loaded["origin"],
                    "rootAsset": True,
                    "streaming": True,
                    "websocket": True,
                    "nativePermissionsDenied": True,
                    "shortcutAndClipboard": True,
                    "relatedPopup": True,
                    "lastPreviewClose": True,
                    "occupiedPort": True,
                    "localPortHTTP": True,
                    "reconfigureCleanup": True,
                    "disconnectCleanup": True,
                    "externalExitCleanup": True,
                    "result": "passed",
                },
                indent=2,
            )
        )
    except Exception:
        for log in logs:
            log.flush()
            log.seek(0)
            print(log.read()[-4000:])
        raise
    finally:
        CONTINUE.set()
        HOLD.set()
        if stop_url:
            try:
                urllib.request.urlopen(stop_url).close()
            except OSError:
                pass
        activated = set()
        if bus:
            for service in [
                "org.a11y.Bus",
                "org.freedesktop.portal.Desktop",
                "org.freedesktop.portal.Documents",
                "org.freedesktop.impl.portal.PermissionStore",
                "org.freedesktop.impl.portal.desktop.gtk",
            ]:
                try:
                    if bus.name_has_owner(service):
                        activated.add(int(daemon.GetConnectionUnixProcessID(service)))
                except dbus.DBusException:
                    pass

        def descendants(pid):
            try:
                child_pids = (
                    Path(f"/proc/{pid}/task/{pid}/children").read_text().split()
                )
            except FileNotFoundError:
                return
            for pid in child_pids:
                activated.add(int(pid))
                descendants(int(pid))

        for pid in list(activated):
            descendants(pid)
        for child in reversed(children):
            if child.poll() is None:
                os.killpg(child.pid, signal.SIGTERM)
                try:
                    child.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    os.killpg(child.pid, signal.SIGKILL)
                    child.wait()
        for pid in activated:
            try:
                fd = os.pidfd_open(pid)
                try:
                    signal.pidfd_send_signal(fd, signal.SIGTERM)
                    if not select.select([fd], [], [], 10)[0]:
                        signal.pidfd_send_signal(fd, signal.SIGKILL)
                        assert select.select([fd], [], [], 10)[0], (
                            "Private activation helper did not exit"
                        )
                finally:
                    os.close(fd)
            except ProcessLookupError:
                pass
        document_mount = Path(env["XDG_RUNTIME_DIR"]) / "doc"
        if document_mount.is_mount():
            subprocess.run(["fusermount3", "-u", str(document_mount)], check=True)
        for thread in threads:
            thread.join()
        for fd in fds:
            os.close(fd)
        for log in logs:
            log.close()
        server.shutdown()
        server.server_close()
        server_thread.join()
        shutil.rmtree(temp)
        shutil.rmtree(runtime)


if __name__ == "__main__":
    main()
