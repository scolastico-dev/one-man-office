#!/usr/bin/env python3
"""Opt-in Linux native smoke: isolated X server, D-Bus service and remote page.

Requires python-gobject, dbus-python, python-xlib, Xvfb and xdotool. Run inside
`dbus-run-session -- python3 ui/tests/native_smoke.py`. No real omo home is used.
"""

import argparse
import base64
from contextlib import closing
import http.server
import json
import os
import pathlib
import queue
import re
import urllib.request
import shutil
import signal
import socket
import sys
import subprocess
import tempfile
import threading
import urllib.parse
import uuid

TOKEN = "a" * 64
AUTH_KIND = "token"


def authorized(headers):
    expected = (
        ("Bearer " + TOKEN)
        if AUTH_KIND == "token"
        else ("Basic " + base64.b64encode(("user:" + TOKEN).encode()).decode())
    )
    return AUTH_KIND == "none" or headers.get("Authorization") == expected


EVENTS = queue.Queue()
FEED = threading.Event()
ROOT = pathlib.Path(__file__).resolve().parents[2]
PAGE = b"""<!doctype html><title>Native remote fixture</title><body><h1>Remote board fixture</h1><textarea autofocus></textarea><button id="editor">Open editor</button><script src="/fixture.js"></script>"""
SCRIPT = b"""const report=(event)=>fetch('/evidence',{method:'POST',body:JSON.stringify(event)});
window.__TAURI__.core.invoke('get_setup').then(()=>report({privilege:'GRANTED'}),()=>report({privilege:'denied'}));
document.addEventListener('keydown',event=>{if(event.ctrlKey&&event.shiftKey&&event.key.toLowerCase()==='p'){event.preventDefault();report({commandPalette:true,page:location.pathname});}});
document.addEventListener('keydown',event=>{if(event.ctrlKey&&event.key.toLowerCase()==='q'){event.preventDefault();report({quitKeyForwarded:true});}});
document.querySelector('#editor').onclick=()=>{const child=window.open('','_blank');child.location.href=location.origin+'/editor';};
report({loaded:location.pathname,token:authMode==='token'?localStorage.getItem('omo.token')==='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa':localStorage.getItem('omo.token')===null,native:window.__OMO_NATIVE_UI__?.notifications===true,notificationAPI:typeof Notification});
"""


class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def send(self, status, value, kind="application/json"):
        data = value if isinstance(value, bytes) else json.dumps(value).encode()
        self.send_response(status)
        self.send_header("Content-Type", kind)
        if status == 401:
            self.send_header("WWW-Authenticate", 'Basic realm="omo", charset="UTF-8"')
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if (AUTH_KIND == "basic" or self.path.startswith("/api/")) and not authorized(self.headers):
            self.send(401, {})
            return
        if self.path == "/api/server":
            self.send(200, {})
        elif self.path.startswith("/api/ui/notifications"):
            query = urllib.parse.parse_qs(urllib.parse.urlsplit(self.path).query)
            if query["cursor"] == ["9223372036854775807"]:
                self.send(409, {"error": {"details": {"reset_cursor": "0", "latest_cursor": "0"}}})
            else:
                notices = (
                    [
                        {
                            "id": "event:1",
                            "kind": "question",
                            "title": "Native smoke notice",
                            "body": "Open ticket seven",
                            "destination": "/7",
                        }
                    ]
                    if FEED.is_set() and query["cursor"] == ["0"]
                    else []
                )
                self.send(
                    200,
                    {
                        "notifications": notices,
                        "next_cursor": "1" if FEED.is_set() else "0",
                        "has_more": False,
                    },
                )
        elif self.path == "/fixture.js":
            self.send(
                200,
                b"const authMode=" + json.dumps(AUTH_KIND).encode() + b";" + SCRIPT,
                "text/javascript",
            )
        else:
            self.send(200, PAGE, "text/html")

    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
        if self.path == "/api/ui/capabilities":
            assert authorized(self.headers)
            assert "Origin" not in self.headers
            FLAGS = json.loads(body)["features"]
            assert "ui.notifications.system.v1" in FLAGS
            self.send(200, {"features": ["ui.capabilities.v1", "ui.notifications.poll.v1"]})
        elif self.path == "/evidence":
            EVENTS.put(json.loads(body))
            self.send(200, {})
        else:
            self.send(404, {})


def main():
    global TOKEN, AUTH_KIND
    parser = argparse.ArgumentParser()
    parser.add_argument("--board-fixture")
    parser.add_argument("--publisher")
    parser.add_argument("--screenshot")
    parser.add_argument("--auth", choices=["token", "basic", "none"], default="token")
    parser.add_argument("--xvfb", default="Xvfb")
    parser.add_argument("--xdotool", default="xdotool")
    parser.add_argument("--app", default=str(ROOT / "ui/target/debug/omo-ui"))
    args = parser.parse_args()
    AUTH_KIND = args.auth
    if AUTH_KIND == "basic":
        TOKEN = "p:ass"
    # Atspi stays on this thread; D-Bus notifications run in a separate process.
    import gi

    gi.require_version("Atspi", "2.0")
    from gi.repository import GLib, Atspi
    from Xlib import X, display, protocol

    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    server_thread = threading.Thread(target=server.serve_forever)
    server_thread.start()
    app = xvfb = None
    display_read = display_write = None
    helpers = []
    readers = []
    stop_url = None
    temp = pathlib.Path(tempfile.mkdtemp(prefix="ui-native-smoke-", dir=ROOT / "bin"))
    try:
        env = {k: v for k, v in os.environ.items() if not k.upper().startswith("OMO_")}
        for key in ["HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "TMPDIR"]:
            env[key] = str(temp / key)
            pathlib.Path(env[key]).mkdir()
        env["GTK_USE_PORTAL"] = "0"
        env["GDK_BACKEND"] = "x11"
        env["WEBKIT_EXEC_PATH"] = str(ROOT / "bin/ui-host/usr/lib/webkit2gtk-4.1")
        display_read, display_write = os.pipe()
        xvfb = subprocess.Popen(
            [
                args.xvfb,
                "-displayfd",
                str(display_write),
                "-screen",
                "0",
                "1280x900x24",
                "-nolisten",
                "tcp",
            ],
            env=env,
            pass_fds=(display_write,),
            stdout=subprocess.DEVNULL,
            stderr=subprocess.PIPE,
            start_new_session=True,
        )
        os.close(display_write)
        display_write = None
        with os.fdopen(display_read) as ready:
            env["DISPLAY"] = ":" + ready.readline().strip()
        display_read = None
        board_origin = f"http://127.0.0.1:{server.server_port}/"
        publisher_origin = None

        def launch(command, extra=None, pass_fds=()):
            child_env = env.copy()
            child_env.update(extra or {})
            child = subprocess.Popen(
                command,
                env=child_env,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                text=True,
                start_new_session=True,
                pass_fds=pass_fds,
            )
            helpers.append(child)
            lines = queue.Queue()
            log_path = temp / f"helper-{len(helpers)}.log"

            def read():
                with log_path.open("w") as output:
                    for line in child.stdout:
                        output.write(line)
                        output.flush()
                        lines.put(line)
                lines.put(None)

            reader = threading.Thread(target=read)
            reader.start()
            readers.append(reader)
            return child, lines

        _, notification_lines = launch(
            [
                sys.executable,
                str(ROOT / "ui/tests/notification_service.py"),
                f"http://127.0.0.1:{server.server_port}/evidence",
            ]
        )
        assert notification_lines.get(timeout=45) == "NOTIFICATIONS_READY\n"
        if args.board_fixture:
            assert args.publisher, "Actual board smoke also requires a pinned publisher executable"
            workspace = temp / "workspace"
            workspace.mkdir()
            (workspace / "example.txt").write_text("native editor smoke\n")
            listener = socket.socket()
            listener.bind(("127.0.0.1", 0))
            listener.listen()
            publisher_port = listener.getsockname()[1]
            for scope in ["User", "Machine"]:
                settings_dir = temp / "editor-user" / scope
                settings_dir.mkdir(parents=True, exist_ok=True)
                (settings_dir / "settings.json").write_text(
                    json.dumps(
                        {
                            "git.enabled": False,
                            "update.mode": "none",
                            "chat.disableAIFeatures": True,
                            "telemetry.telemetryLevel": "off",
                        }
                    )
                )
            _, lines = launch(
                [
                    args.publisher,
                    "--socket-fd",
                    str(listener.fileno()),
                    "--trusted-origins",
                    f"127.0.0.1:{publisher_port}",
                    "--auth",
                    "none",
                    "--disable-workspace-trust",
                    "--disable-telemetry",
                    "--disable-update-check",
                    "--enable-remote-storage",
                    "--log",
                    "debug",
                    "--user-data-dir",
                    str(temp / "editor-user"),
                    "--extensions-dir",
                    str(temp / "editor-extensions"),
                    str(workspace),
                ],
                pass_fds=(listener.fileno(),),
            )
            listener.close()
            while publisher_origin is None:
                line = lines.get(timeout=60)
                if line is None:
                    raise RuntimeError("Publisher exited before listening")
                match = re.search(r"HTTP server listening on (http://127\.0\.0\.1:[0-9]+)", line)
                if match:
                    publisher_origin = match.group(1)
            _, lines = launch(
                [args.board_fixture, "-test.run=^TestProxySubdomainBrowserFixture$"],
                {"PROXY_BROWSER_FIXTURE": "1"},
            )
            while True:
                line = lines.get(timeout=60)
                if line is None:
                    raise RuntimeError("Board fixture exited before readiness")
                if line.startswith("PROXY_BROWSER_FIXTURES "):
                    fixture = next(
                        f for f in json.loads(line.split(" ", 1)[1]) if f["variant"] == "http"
                    )
                    board_origin = fixture["board"] + "/"
                    TOKEN = fixture["token"]
                    AUTH_KIND = "token"
                    stop_url = fixture["stop"]
                    break
        config = pathlib.Path(env["XDG_CONFIG_HOME"]) / "dev.scolastico.omo.ui"
        config.mkdir()
        (config / "config.json").write_text(
            json.dumps(
                {
                    "id": str(uuid.uuid4()),
                    "mode": "direct",
                    "origin": board_origin,
                    "auth": AUTH_KIND,
                    "username": "user" if AUTH_KIND == "basic" else "",
                    "notifications": True,
                    "remember": False,
                }
            )
        )
        log = open(temp / "app.log", "w+")
        app = subprocess.Popen([args.app], env=env, stdout=log, stderr=log, start_new_session=True)

        def watch_app():
            EVENTS.put({"appExited": app.wait()})

        watcher = threading.Thread(target=watch_app)
        watcher.start()
        readers.append(watcher)

        def x(*cmd):
            return subprocess.check_output(
                [args.xdotool, *cmd], env=env, text=True, timeout=45
            ).strip()

        setup = x("search", "--sync", "--name", "omo ui · Connection").splitlines()[0]
        x("windowfocus", "--sync", setup)
        # Wait for the actual accessible form and enabled Connect action, never a guessed delay.
        changed = threading.Event()

        # Avoid PyGObject's unsafe asynchronous Atspi callback lifetime; inspect exact states synchronously.
        def tree(node):
            # A document replacement invalidates accessible IDs already returned by its old tree.
            try:
                yield node
                children = [node.get_child_at_index(i) for i in range(node.get_child_count())]
            except GLib.GError:
                return
            for child in children:
                if child is not None:
                    yield from tree(child)

        def matches(node, predicate):
            try:
                return predicate(node)
            except GLib.GError:
                return False

        try:
            while True:
                changed.clear()
                nodes = list(tree(Atspi.get_desktop(0)))
                buttons = [
                    n
                    for n in nodes
                    if matches(
                        n,
                        lambda n: n.get_name() == "Connect"
                        and n.get_role() == Atspi.Role.PUSH_BUTTON
                        and n.get_state_set().contains(Atspi.StateType.ENABLED),
                    )
                ]
                secrets = [
                    n
                    for n in nodes
                    if matches(
                        n,
                        lambda n: n.get_name()
                        == ("Password" if AUTH_KIND == "basic" else "Login token")
                        and n.get_role() == Atspi.Role.PASSWORD_TEXT,
                    )
                ]
                if buttons and (secrets or AUTH_KIND == "none"):
                    if AUTH_KIND != "none":
                        assert secrets[0].get_component_iface().grab_focus()
                        x("type", "--clearmodifiers", TOKEN)
                        while secrets[0].get_text_iface().get_character_count() != len(TOKEN):
                            changed.clear()
                            if secrets[0].get_text_iface().get_character_count() == len(TOKEN):
                                break
                            if app.poll() is not None:
                                raise RuntimeError("Native app exited during input")
                            changed.wait(0.02)
                    buttons[0].get_action_iface().do_action(0)
                    break
                if app.poll() is not None:
                    raise RuntimeError("Native app exited before setup")
                changed.wait(0.02)
        finally:
            pass
        evidence = []

        def wait_for(predicate):
            while True:
                item = EVENTS.get(timeout=45)
                evidence.append(item)
                if "appExited" in item:
                    raise RuntimeError("Native app exited before the expected page event")
                if predicate(item):
                    return item

        def find(predicate):
            while True:
                for node in tree(Atspi.get_desktop(0)):
                    if matches(
                        node,
                        lambda n: "The workbench failed to connect to the server"
                        in n.get_name(),
                    ):
                        raise RuntimeError("Editor WebSocket connection failed")
                    if matches(node, predicate):
                        return node
                if app.poll() is not None:
                    raise RuntimeError("Native app exited while waiting for an accessible control")
                changed.wait(0.02)

        if args.board_fixture:
            find(lambda n: n.get_name().startswith("Planning"))
            print("Actual board loaded.", flush=True)
            assert publisher_origin
            request = urllib.request.Request(
                board_origin + "api/proxy/forwards",
                data=json.dumps(
                    {
                        "host": "127.0.0.1",
                        "port": int(urllib.parse.urlsplit(publisher_origin).port),
                        "name": "Native editor smoke",
                        "strip_prefix": True,
                    }
                ).encode(),
                headers={"Authorization": "Bearer " + TOKEN, "Content-Type": "application/json"},
            )
            with urllib.request.urlopen(request) as response:
                assert response.status == 201
            forwards = find(
                lambda n: n.get_name() == "Forwards" and n.get_action_iface() is not None
            )
            forwards.get_action_iface().do_action(0)
            opening = find(
                lambda n: n.get_name() == "Open Native editor smoke in new tab"
                and n.get_role() == Atspi.Role.PUSH_BUTTON
            )
            opening.get_action_iface().do_action(0)
            print("Opened editor through board forward.", flush=True)
            find(lambda n: n.get_name().startswith("Explorer"))
            editor = x("search", "--sync", "--name", "code-server").splitlines()[0]
            x("windowfocus", "--sync", editor)
            x("key", "ctrl+shift+p")
            palette = find(
                lambda n: "Type the name of a command to run" in n.get_name()
                and n.get_role() == Atspi.Role.ENTRY
                and n.get_state_set().contains(Atspi.StateType.SHOWING)
            )
            # WebKit exposes VS Code's active-descendant focus on its selected list item.
            # Verify the input is usable by typing and an actual copy/clear/paste cycle.
            x("type", "--clearmodifiers", "Preferences: Open Settings")

            def text_is(value):
                return (
                    lambda n: (
                        "Type the name of a command to run" in n.get_name()
                        or "Search files by name" in n.get_name()
                    )
                    and n.get_role() == Atspi.Role.ENTRY
                    and Atspi.Text.get_text(n.get_text_iface(), 0, -1) == value
                )

            expected = ">Preferences: Open Settings"
            find(text_is(expected))
            x("key", "ctrl+a", "ctrl+c", "BackSpace")
            find(text_is(""))
            x("key", "ctrl+v")
            find(text_is(expected))
            if args.screenshot:
                subprocess.run(
                    ["magick", "import", "-window", "root", args.screenshot], env=env, check=True
                )
            print(
                json.dumps(
                    {
                        "platform": "linux-x86_64",
                        "actualBoard": True,
                        "proxyOpen": True,
                        "publisher": "code-server 4.145.2",
                        "commandPalette": True,
                        "typingAndClipboard": True,
                        "result": "passed",
                    },
                    indent=2,
                )
            )
            return
        loaded = wait_for(lambda e: e.get("loaded") == "/")
        assert (
            loaded["token"] and loaded["native"] and loaded["notificationAPI"] == "undefined"
        ), loaded
        wait_for(lambda e: e.get("privilege") == "denied")
        board = x("search", "--sync", "--name", "Native remote fixture").splitlines()[0]
        x("windowfocus", "--sync", board)
        x("key", "ctrl+shift+p")
        wait_for(lambda e: e.get("commandPalette") and e.get("page") == "/")
        x("key", "ctrl+q")
        wait_for(lambda e: e.get("quitKeyForwarded"))
        FEED.set()
        wait_for(lambda e: e.get("notification") == "Native smoke notice")
        subprocess.run(
            [
                "gdbus",
                "call",
                "--session",
                "--dest",
                "org.freedesktop.Notifications",
                "--object-path",
                "/org/freedesktop/Notifications",
                "--method",
                "dev.scolastico.omo.NativeSmoke.ActivateDefault",
            ],
            env=env,
            check=True,
            stdout=subprocess.DEVNULL,
        )
        wait_for(lambda e: e.get("loaded") == "/7")
        x("windowfocus", "--sync", board)
        x("key", "Tab", "Return")
        wait_for(lambda e: e.get("loaded") == "/editor")
        # The related child remains in the native process, with no command privileges.
        windows = x("search", "--name", "Native remote fixture").splitlines()
        assert len(windows) == 2, windows
        child = next(w for w in windows if w != board)
        x("windowfocus", "--sync", child)
        x("key", "ctrl+shift+p")
        wait_for(lambda e: e.get("commandPalette") and e.get("page") == "/editor")
        # Both connection entry points must survive repeatedly closing their window.
        for entry in ["board button", "native menu", "board button"]:
            print(f"Reopening Connection through {entry}.", flush=True)
            x("windowfocus", "--sync", board)
            if entry == "native menu":
                find(
                    lambda n: n.get_name() == "omo ui"
                    and n.get_role() == Atspi.Role.MENU
                ).get_action_iface().do_action(0)
                find(
                    lambda n: n.get_name() == "Connection…"
                    and n.get_role() == Atspi.Role.MENU_ITEM
                    and n.get_state_set().contains(Atspi.StateType.SHOWING)
                ).get_action_iface().do_action(0)
            else:
                find(
                    lambda n: n.get_name() == "Configure omo ui connection"
                    and n.get_role() == Atspi.Role.PUSH_BUTTON
                    and n.get_state_set().contains(Atspi.StateType.SHOWING)
                ).get_action_iface().do_action(0)
            connection = x(
                "search", "--onlyvisible", "--sync", "--name", "^omo ui · Connection$"
            ).splitlines()[0]
            find(
                lambda n: n.get_name() == "Connect"
                and n.get_role() == Atspi.Role.PUSH_BUTTON
                and n.get_state_set().contains(Atspi.StateType.SHOWING)
                and n.get_state_set().contains(Atspi.StateType.ENABLED)
            )
            find(
                lambda n: n.get_name() == "Disconnect"
                and n.get_role() == Atspi.Role.PUSH_BUTTON
                and n.get_state_set().contains(Atspi.StateType.SHOWING)
                and n.get_state_set().contains(Atspi.StateType.ENABLED)
            )
            address = find(
                lambda n: n.get_name() == "Board address"
                and n.get_role() == Atspi.Role.ENTRY
                and n.get_state_set().contains(Atspi.StateType.SHOWING)
            )
            assert Atspi.Text.get_text(address.get_text_iface(), 0, -1) == board_origin
            # WM_DELETE_WINDOW is the same close request used by a titlebar button.
            # Send it directly: bare Xvfb has no window manager to translate EWMH requests.
            with closing(display.Display(env["DISPLAY"])) as screen:
                screen.send_event(
                    int(connection),
                    protocol.event.ClientMessage(
                        window=int(connection),
                        client_type=screen.intern_atom("WM_PROTOCOLS"),
                        data=(
                            32,
                            [screen.intern_atom("WM_DELETE_WINDOW"), X.CurrentTime, 0, 0, 0],
                        ),
                    ),
                    event_mask=0,
                )
                screen.sync()
            while subprocess.run(
                [
                    args.xdotool,
                    "search",
                    "--onlyvisible",
                    "--name",
                    "^omo ui · Connection$",
                ],
                env=env,
                stdout=subprocess.DEVNULL,
                check=False,
            ).returncode == 0:
                if app.poll() is not None:
                    raise RuntimeError("Closing Connection exited the connected app")
                changed.wait(0.02)
        evidence.append({"connectionReopenedAfterClose": True})
        print(
            json.dumps(
                {
                    "platform": "linux-x86_64",
                    "auth": AUTH_KIND,
                    "evidence": evidence,
                    "nativeWindows": len(windows),
                    "result": "passed",
                },
                indent=2,
            )
        )
    except Exception:
        if "log" in locals():
            log.flush()
            log.seek(0)
            print(log.read()[-6000:])
        raise
    finally:
        if stop_url:
            try:
                urllib.request.urlopen(stop_url).close()
            except OSError:
                pass
        for process in [app, *helpers, xvfb]:
            if process and process.poll() is None:
                os.killpg(process.pid, signal.SIGTERM)
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait()
        for reader in readers:
            reader.join()
        for fd in [display_read, display_write]:
            if fd is not None:
                os.close(fd)
        if "log" in locals():
            log.close()
        server.shutdown()
        server.server_close()
        server_thread.join()
        shutil.rmtree(temp)


if __name__ == "__main__":
    main()
