#!/usr/bin/env python3
"""Opt-in Linux native local lifecycle smoke; no real omo server is started.

Run with /usr/bin/python3 under dbus-run-session. Requires PyGObject/Atspi,
dbus-python, python-xlib, Xvfb and the native app's GTK/WebKit dependencies.
Using --host additionally requires bubblewrap with read-only overlay support.
The executable double is installed only in the isolated HOME/.local/bin.
Its private manager starts a board/control HTTP fixture in this process.
All waits observe exact state; deadlines are failure/liveness guards only.
"""

import argparse
import http.server
import json
import os
import pathlib
import queue
import select
import shutil
import signal
import subprocess
import sys
import tempfile
import threading
import time
import urllib.request
import uuid

ROOT = pathlib.Path(__file__).resolve().parents[2]
CHANGED = threading.Event()
TOKEN = "a" * 64


def wait_for(description, condition, app=None):
    deadline = time.monotonic() + 60
    while True:
        CHANGED.clear()
        result = condition()
        if result:
            return result
        if app is not None and app.poll() is not None:
            raise AssertionError(f"App exited ({app.returncode}) before {description}")
        if time.monotonic() >= deadline:
            raise AssertionError(f"Timed out waiting for {description}")
        CHANGED.wait(0.025)


def tray_watcher():
    """A real private-session StatusNotifier watcher, without a desktop shell."""
    import dbus
    import dbus.service
    from dbus.mainloop.glib import DBusGMainLoop
    from gi.repository import GLib

    DBusGMainLoop(set_as_default=True)
    bus = dbus.SessionBus()
    name = dbus.service.BusName("org.kde.StatusNotifierWatcher", bus)

    class Watcher(dbus.service.Object):
        def __init__(self):
            super().__init__(name, "/StatusNotifierWatcher")
            self.items = []

        @dbus.service.method("org.kde.StatusNotifierWatcher", in_signature="s", sender_keyword="sender")
        def RegisterStatusNotifierItem(self, service, sender=None):
            item = str(sender) + str(service) if str(service).startswith("/") else str(service) + "/StatusNotifierItem"
            self.items.append(item)
            self.StatusNotifierItemRegistered(item)

        @dbus.service.method("org.kde.StatusNotifierWatcher", in_signature="s")
        def RegisterStatusNotifierHost(self, service):
            pass

        @dbus.service.signal("org.kde.StatusNotifierWatcher", signature="s")
        def StatusNotifierItemRegistered(self, item):
            pass

        @dbus.service.method("org.freedesktop.DBus.Properties", in_signature="ss", out_signature="v")
        def Get(self, interface, prop):
            return self.GetAll(interface)[prop]

        @dbus.service.method("org.freedesktop.DBus.Properties", in_signature="s", out_signature="a{sv}")
        def GetAll(self, interface):
            return {"RegisteredStatusNotifierItems": dbus.Array(self.items, signature="s"),
                    "IsStatusNotifierHostRegistered": dbus.Boolean(True),
                    "ProtocolVersion": dbus.Int32(0)}

    watcher = Watcher()
    print("WATCHER_READY", flush=True)
    GLib.MainLoop().run()


class Fixture:
    """Only the external server is doubled; the app's local manager stays real."""
    def __init__(self, home, label):
        self.home = home
        self.label = label
        self.loaded = threading.Event()
        self.stopped = threading.Event()
        self.requests = []
        self.start_error = None
        self.invalid_auth = False
        self.closed = False
        self.lock = threading.Lock()
        self.control_token = uuid.uuid4().hex + uuid.uuid4().hex[:16]
        fixture = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def reply(self, status, value=b"", kind="application/json"):
                body = value if isinstance(value, bytes) else json.dumps(value).encode()
                self.send_response(status)
                self.send_header("Content-Type", kind)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def control(self):
                assert self.headers.get("Authorization") == "Bearer " + fixture.control_token
                assert "Origin" not in self.headers
                assert "Sec-Fetch-Site" not in self.headers
                with fixture.lock:
                    fixture.requests.append(self.path)

            def do_GET(self):
                if self.path == "/status":
                    self.control()
                    self.reply(410 if fixture.stopped.is_set() else 204)
                elif self.path == "/api/server":
                    self.reply(200, {})
                elif self.path == "/fixture.js":
                    self.reply(200, b"fetch('/loaded', {method:'POST'});", "text/javascript")
                else:
                    self.reply(200, ("<!doctype html><title>" + fixture.label + "</title><h1>Local lifecycle fixture</h1><script src='/fixture.js'></script>").encode(), "text/html")

            def do_POST(self):
                self.rfile.read(int(self.headers.get("Content-Length", "0")))
                if self.path == "/stop":
                    self.control()
                    path = fixture.home / "run/runtime.json"
                    if path.exists() and json.loads(path.read_text()).get("token") == fixture.control_token:
                        path.unlink()
                    fixture.stopped.set()
                    self.reply(202)
                elif self.path == "/api/ui/capabilities":
                    self.reply(200, {"features": ["ui.capabilities.v1"]})
                elif self.path == "/loaded":
                    fixture.loaded.set()
                    self.reply(200, {})
                else:
                    self.reply(404, {})
                CHANGED.set()

        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever)
        self.thread.start()
        self.origin = f"http://127.0.0.1:{self.server.server_port}/"
        self.runtime = {"endpoint": f"127.0.0.1:{self.server.server_port}",
                        "token": self.control_token, "url": self.origin + "#" + TOKEN,
                        "launch_id": str(uuid.uuid4())}

    def publish(self):
        path = self.home / "run/runtime.json"
        path.parent.mkdir(parents=True, exist_ok=True)
        staged = path.with_suffix(".staged")
        staged.write_text(json.dumps(self.runtime))
        staged.chmod(0o600)
        staged.replace(path)

    def ready(self):
        assert self.start_error is None, self.start_error
        return self.loaded.is_set()

    def alive(self):
        request = urllib.request.Request(self.origin + "status", headers={"Authorization": "Bearer " + self.control_token})
        with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(request) as reply:
            assert reply.status == 204

    def close(self):
        if not self.closed:
            self.server.shutdown()
            self.server.server_close()
            self.thread.join()
            self.closed = True
            CHANGED.set()


def run(args):
    import dbus
    import gi
    gi.require_version("Atspi", "2.0")
    from gi.repository import Atspi, GLib
    from Xlib import X, display, protocol

    if not os.environ.get("DBUS_SESSION_BUS_ADDRESS"):
        raise RuntimeError("Run this opt-in test inside dbus-run-session")
    base = ROOT / "bin"
    base.mkdir(exist_ok=True)
    temp = pathlib.Path(tempfile.mkdtemp(prefix="ui-local-smoke-", dir=base))
    processes = []
    fixtures = []
    logs = []
    manager = None
    manager_thread = None
    bus = None
    xvfb = None
    display_read = display_write = None
    try:
        env = {k: v for k, v in os.environ.items() if not k.upper().startswith("OMO_")}
        for key in ["HOME", "OMO_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR", "TMPDIR"]:
            path = temp / key
            path.mkdir(mode=0o700)
            env[key] = str(path)
        env.update(GTK_USE_PORTAL="0", GDK_BACKEND="x11", NO_AT_BRIDGE="0")
        # A real omo executable on the developer's PATH must never be discovered.
        empty_path = temp / "empty-path"
        empty_path.mkdir()
        env["PATH"] = str(empty_path)
        app_command = [args.app]
        if args.host:
            host = pathlib.Path(args.host).resolve()
            env["LD_LIBRARY_PATH"] = str(host / "usr/lib") + ":" + env.get("LD_LIBRARY_PATH", "")
            env["WEBKIT_EXEC_PATH"] = str(host / "usr/lib/webkit2gtk-4.1")
            # Release WebKit builds use absolute helper paths and ignore
            # WEBKIT_EXEC_PATH. Overlay only this process's library directory;
            # keep test state writable and never install into the host /usr.
            bubblewrap = shutil.which("bwrap")
            assert bubblewrap, "--host requires bubblewrap"
            app_command = [bubblewrap, "--ro-bind", "/", "/", "--bind", str(temp), str(temp),
                           "--dev-bind", "/dev", "/dev", "--overlay-src", "/usr/lib",
                           "--overlay-src", str(host / "usr/lib"), "--ro-overlay", "/usr/lib",
                           "--die-with-parent", "--", args.app]

        def launch(command, case_env=env):
            log = (temp / f"process-{len(processes)}.log").open("w+")
            logs.append(log)
            child = subprocess.Popen(command, env=case_env, stdout=log, stderr=log, start_new_session=True)
            processes.append(child)
            return child

        display_read, display_write = os.pipe()
        xvfb = subprocess.Popen([args.xvfb, "-displayfd", str(display_write), "-screen", "0", "1280x900x24", "-nolisten", "tcp"],
                                env=env, pass_fds=(display_write,), stdout=subprocess.DEVNULL,
                                stderr=subprocess.DEVNULL, start_new_session=True)
        processes.append(xvfb)
        os.close(display_write)
        display_write = None
        with os.fdopen(display_read) as ready:
            number = ready.readline().strip()
        display_read = None
        assert number, "Xvfb exited before publishing its display"
        env["DISPLAY"] = ":" + number
        os.environ["DISPLAY"] = env["DISPLAY"]
        os.environ["NO_AT_BRIDGE"] = "0"
        session_bus = dbus.SessionBus()
        daemon = dbus.Interface(session_bus.get_object("org.freedesktop.DBus", "/org/freedesktop/DBus"), "org.freedesktop.DBus")
        bus_pid = int(daemon.GetConnectionUnixProcessID("org.freedesktop.DBus"))
        siblings = pathlib.Path(f"/proc/{os.getppid()}/task/{os.getppid()}/children").read_text().split()
        bus_args = pathlib.Path(f"/proc/{bus_pid}/cmdline").read_bytes().split(b"\0")
        assert str(bus_pid) in siblings and b"--session" in bus_args and b"--print-address" in bus_args, "Use a fresh dbus-run-session; the desktop session bus is refused"
        bus = session_bus
        # Activation helpers inherit the bus daemon's original environment.
        # Give accessibility/portal services the same isolated home and display.
        daemon.UpdateActivationEnvironment({key: env[key] for key in [
            "HOME", "OMO_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME",
            "XDG_STATE_HOME", "XDG_RUNTIME_DIR", "TMPDIR", "DISPLAY", "GTK_USE_PORTAL",
            "GDK_BACKEND", "NO_AT_BRIDGE",
        ]})
        watcher = launch([sys.executable, str(pathlib.Path(__file__).resolve()), "--tray-watcher"])
        wait_for("StatusNotifier watcher registration", lambda: bus.name_has_owner("org.kde.StatusNotifierWatcher"), watcher)
        watcher_properties = dbus.Interface(bus.get_object("org.kde.StatusNotifierWatcher", "/StatusNotifierWatcher"), "org.freedesktop.DBus.Properties")

        def tray_items():
            return [str(item) for item in watcher_properties.Get("org.kde.StatusNotifierWatcher", "RegisteredStatusNotifierItems")]

        def menu(item, label):
            service, path = item.split("/", 1)
            properties = dbus.Interface(bus.get_object(service, "/" + path), "org.freedesktop.DBus.Properties")
            menu_path = str(properties.Get("org.kde.StatusNotifierItem", "Menu"))
            proxy = dbus.Interface(bus.get_object(service, menu_path), "com.canonical.dbusmenu")
            _, layout = proxy.GetLayout(0, -1, [])

            def entries(node):
                yield node
                for child in node[2]:
                    yield from entries(child)

            entry = next(node for node in entries(layout) if node[1].get("label") == label)
            proxy.Event(entry[0], "clicked", dbus.Int32(0, variant_level=1), dbus.UInt32(0))

        def tree(node):
            try:
                yield node
                children = [node.get_child_at_index(i) for i in range(node.get_child_count())]
            except GLib.GError:
                return
            for child in children:
                if child is not None:
                    yield from tree(child)

        def accessible_action(app, name, role=Atspi.Role.PUSH_BUTTON):
            def find():
                for node in tree(Atspi.get_desktop(0)):
                    try:
                        states = node.get_state_set()
                        if (node.get_name() == name and node.get_role() == role
                                and states.contains(Atspi.StateType.ENABLED) and states.contains(Atspi.StateType.SHOWING)):
                            return node
                    except GLib.GError:
                        pass
                return None
            return wait_for("accessible enabled " + name + " action", find, app)

        def connection_error(app):
            def find():
                for node in tree(Atspi.get_desktop(0)):
                    try:
                        # WebKit maps role=alert to Atspi NOTIFICATION and
                        # exposes its message through Text, without a name.
                        if (node.get_role() in [Atspi.Role.ALERT, Atspi.Role.NOTIFICATION]
                                and node.get_state_set().contains(Atspi.StateType.SHOWING)):
                            text = node.get_text_iface()
                            if text and Atspi.Text.get_text(text, 0, -1):
                                return node
                    except GLib.GError:
                        pass
                return None
            return wait_for("visible connection preparation error", find, app)

        screen = display.Display(env["DISPLAY"])

        def windows(title, visible=None, board=True):
            found = []
            for window in screen.screen().root.query_tree().children:
                try:
                    name = window.get_wm_name()
                    target = title + " · omo ui" if board else title
                    if name == target and (visible is None or (window.get_attributes().map_state == X.IsViewable) == visible):
                        found.append(window)
                except Exception:
                    continue
            return found

        def close_window(window):
            screen.send_event(window, protocol.event.ClientMessage(window=window,
                client_type=screen.intern_atom("WM_PROTOCOLS"),
                data=(32, [screen.intern_atom("WM_DELETE_WINDOW"), X.CurrentTime, 0, 0, 0])), event_mask=0)
            screen.sync()

        pending = queue.Queue()

        class Manager(http.server.BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_POST(self):
                assert self.path == "/start"
                fixture = pending.get_nowait()
                payload = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                assert payload == {"home": str(fixture.home)}
                if list(fixture.home.iterdir()):
                    fixture.start_error = "Native startup populated the fresh home before the CLI could initialize its layout"
                    self.send_error(409, fixture.start_error)
                    CHANGED.set()
                    return
                if fixture.invalid_auth:
                    # Valid YAML, but the resulting Basic secret contains a
                    # control character and fails native auth preparation.
                    (fixture.home / "server.yaml").write_text('auth: basic\nbasic_auth: "user:pass\\tword"\n')
                fixture.publish()
                body = json.dumps({**fixture.runtime, "owned": True}).encode()
                self.send_response(200)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

        manager = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Manager)
        manager_thread = threading.Thread(target=manager.serve_forever)
        manager_thread.start()
        executable = pathlib.Path(env["HOME"]) / ".local/bin/omo"
        executable.parent.mkdir(parents=True)
        source = ("#!/usr/bin/python3\nimport json, os, sys, urllib.request\n"
                  "assert sys.argv[1:] == ['ui-local', 'start'], sys.argv\n"
                  "assert {key for key in os.environ if key.upper().startswith('OMO_')} == {'OMO_HOME'}\n"
                  f"request = urllib.request.Request('http://127.0.0.1:{manager.server_port}/start', data=json.dumps({{'home': os.environ['OMO_HOME']}}).encode())\n"
                  "with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(request) as reply: print(reply.read().decode())\n")
        results = []
        for name, mode, attached, stop, binary in [
            ("attached-no-binary", "local", True, False, False),
            ("attached-with-binary", "local", True, True, True),
            ("owned-tray", "local", False, False, True),
            ("owned-stop-with-window", "local", False, True, True),
            ("owned-replacement", "local", False, True, True),
            ("owned-disconnect", "local", False, False, True),
            ("owned-auth-preparation-error", "local", False, False, True),
            ("direct", "direct", True, True, False),
            ("owned-crash-default-close", "local", False, False, True),
            ("owned-crash-stop-with-window", "local", False, True, True),
            ("owned-crash-explicit-exit", "local", False, False, True),
        ]:
            print(f"Checking {name}.", flush=True)
            case_env = env.copy()
            for key in ["OMO_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME"]:
                path = temp / name / key
                path.mkdir(parents=True)
                case_env[key] = str(path)
            fixture = Fixture(pathlib.Path(case_env["OMO_HOME"]), "Local smoke " + name)
            fixtures.append(fixture)
            if name == "owned-auth-preparation-error":
                fixture.invalid_auth = True
                fixture.runtime["url"] = fixture.origin
            if attached:
                fixture.publish()
            else:
                pending.put(fixture)
            if binary:
                executable.write_text(source)
                executable.chmod(0o755)
            elif executable.exists():
                executable.unlink()
            config_dir = pathlib.Path(case_env["XDG_CONFIG_HOME"]) / "dev.scolastico.omo.ui"
            config_dir.mkdir()
            (config_dir / "config.json").write_text(json.dumps({
                "id": str(uuid.uuid4()), "mode": mode, "origin": fixture.origin,
                "auth": "none", "username": "", "notifications": False,
                "remember": False, "stop_with_window": stop,
            }))
            before = tray_items()
            app = launch(app_command, case_env)
            assert accessible_action(app, "Connect").get_action_iface().do_action(0)
            if fixture.invalid_auth:
                connection_error(app)
                item = wait_for("owned tray after connection preparation error", lambda: next((i for i in tray_items() if i not in before), None), app)
                assert not fixture.loaded.is_set(), "Invalid local authentication opened a board"
                fixture.alive()
                menu(item, "Exit omo and stop local server")
                assert app.wait(timeout=60) == 0
                assert fixture.stopped.is_set(), "Failed connection discarded owned server cleanup"
                assert fixture.requests.count("/stop") == 1
                assert not (fixture.home / "run/runtime.json").exists()
                results.append({"case": name, "result": "passed"})
                fixture.close()
                fixtures.remove(fixture)
                continue
            wait_for("remote board fixture load", fixture.ready, app)
            board = wait_for("visible board window", lambda: windows(fixture.label, True), app)[0]
            if not attached:
                item = wait_for("owned tray registration", lambda: next((i for i in tray_items() if i not in before), None), app)
            if name.startswith("owned-crash-"):
                path = fixture.home / "run/runtime.json"
                stale_runtime = path.read_bytes()
                # A crashed server cannot remove its metadata. End its actual
                # listener and join the serving worker before requesting exit.
                fixture.close()
                assert not fixture.thread.is_alive()
                if name == "owned-crash-explicit-exit":
                    # A dead server legitimately loses its tray on refresh.
                    # Native Exit invokes the same Quit path without racing
                    # tray removal; live tray Exit is covered above.
                    assert accessible_action(app, "omo ui", Atspi.Role.MENU).get_action_iface().do_action(0)
                    assert accessible_action(app, "Exit omo", Atspi.Role.MENU_ITEM).get_action_iface().do_action(0)
                else:
                    close_window(board)
                assert app.wait(timeout=60) == 0
                assert not fixture.stopped.is_set(), "Crashed server was sent a stop request"
                assert fixture.requests.count("/stop") == 0
                assert path.read_bytes() == stale_runtime, "Exit rewrote stale runtime metadata"
                results.append({"case": name, "result": "passed"})
                fixtures.remove(fixture)
                continue
            replacement = None
            if name == "owned-replacement":
                replacement = Fixture(fixture.home, "Replacement fixture")
                fixtures.append(replacement)
                replacement.publish()
            if name == "owned-disconnect":
                assert accessible_action(app, "Configure omo ui connection").get_action_iface().do_action(0)
                assert accessible_action(app, "Disconnect").get_action_iface().do_action(0)
                wait_for("Disconnect stops the captured owned server", fixture.stopped.is_set, app)
                wait_for("Disconnect removes the board window", lambda: not windows(fixture.label), app)
                setup = wait_for("visible disconnected Connection window", lambda: windows("omo ui · Connection", True, False), app)[0]
                close_window(setup)
            else:
                close_window(board)
            if not attached and not stop and name != "owned-disconnect":
                wait_for("owned window hidden after WM_DELETE_WINDOW", lambda: windows(fixture.label, False), app)
                fixture.alive()
                assert not fixture.stopped.is_set()
                menu(item, "Open omo")
                wait_for("tray Open restores board window", lambda: windows(fixture.label, True), app)
                fixture.alive()
                menu(item, "Exit omo and stop local server")
            assert app.wait(timeout=60) == 0
            if attached:
                fixture.alive()
                assert not fixture.stopped.is_set(), "Closing an attached/direct board stopped its independent server"
                assert tray_items() == before, "An attached/direct connection created an owned tray"
                assert fixture.requests.count("/stop") == 0
            else:
                assert fixture.stopped.is_set(), "Owned exit did not stop the captured server"
                assert fixture.requests.count("/stop") == 1
                if replacement:
                    replacement.alive()
                    assert not replacement.stopped.is_set(), "Captured stop reached the replacement server"
                    assert replacement.requests.count("/stop") == 0
                    assert json.loads((fixture.home / "run/runtime.json").read_text()) == replacement.runtime
                    replacement.close()
                    fixtures.remove(replacement)
                else:
                    assert not (fixture.home / "run/runtime.json").exists()
            results.append({"case": name, "result": "passed"})
            fixture.close()
            fixtures.remove(fixture)
        screen.close()
        print(json.dumps({"platform": "linux", "checks": results, "result": "passed"}, indent=2))
    except Exception:
        for log in logs:
            log.flush()
            log.seek(0)
            contents = log.read()
            if contents:
                print(contents[-6000:], file=sys.stderr)
        raise
    finally:
        # Session activation starts accessibility/portal helpers outside our
        # Popen process groups. Capture only the services this private bus owns,
        # including their children, so their FUSE mounts end before rmtree.
        activated = set()
        if bus:
            for service in ["org.a11y.Bus", "org.freedesktop.portal.Desktop",
                            "org.freedesktop.portal.Documents", "org.freedesktop.impl.portal.PermissionStore",
                            "org.freedesktop.impl.portal.desktop.gtk"]:
                try:
                    if bus.name_has_owner(service):
                        activated.add(int(dbus.Interface(bus.get_object("org.freedesktop.DBus", "/org/freedesktop/DBus"),
                                                        "org.freedesktop.DBus").GetConnectionUnixProcessID(service)))
                except dbus.DBusException:
                    pass

        def descendants(pid):
            try:
                children = pathlib.Path(f"/proc/{pid}/task/{pid}/children").read_text().split()
            except FileNotFoundError:
                return
            for child in children:
                child = int(child)
                activated.add(child)
                descendants(child)

        for pid in list(activated):
            descendants(pid)
        for child in reversed(processes):
            if child is xvfb:
                continue
            if child.poll() is None:
                os.killpg(child.pid, signal.SIGTERM)
                try:
                    child.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    os.killpg(child.pid, signal.SIGKILL)
                    child.wait()
        pidfds = []
        try:
            for pid in activated:
                try:
                    fd = os.pidfd_open(pid)
                    pidfds.append((pid, fd))
                    signal.pidfd_send_signal(fd, signal.SIGTERM)
                except ProcessLookupError:
                    pass
            for pid, fd in pidfds:
                if not select.select([fd], [], [], 10)[0]:
                    signal.pidfd_send_signal(fd, signal.SIGKILL)
                    assert select.select([fd], [], [], 10)[0], f"Activation helper {pid} did not exit"
        finally:
            for _, fd in pidfds:
                os.close(fd)
        document_mount = pathlib.Path(env["XDG_RUNTIME_DIR"]) / "doc"
        if document_mount.is_mount():
            subprocess.run([shutil.which("fusermount3") or "/usr/bin/fusermount3", "-u", str(document_mount)], check=True)
        if xvfb and xvfb.poll() is None:
            os.killpg(xvfb.pid, signal.SIGTERM)
            xvfb.wait(timeout=10)
        for fixture in fixtures:
            fixture.close()
        if manager:
            manager.shutdown()
            manager.server_close()
            manager_thread.join()
        for fd in [display_read, display_write]:
            if fd is not None:
                os.close(fd)
        for log in logs:
            log.close()
        shutil.rmtree(temp)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--app", default=str(ROOT / "ui/target/debug/omo-ui"))
    parser.add_argument("--xvfb", default=shutil.which("Xvfb") or "Xvfb")
    parser.add_argument("--host", help="Optional extracted native dependency prefix, e.g. bin/ui-host")
    parser.add_argument("--tray-watcher", action="store_true", help=argparse.SUPPRESS)
    args = parser.parse_args()
    if args.tray_watcher:
        tray_watcher()
    else:
        run(args)


if __name__ == "__main__":
    main()
