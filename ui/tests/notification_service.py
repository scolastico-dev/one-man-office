#!/usr/bin/env python3
"""Single-threaded notification service for the native smoke's private D-Bus."""

import json
import sys
import urllib.request
import dbus
import dbus.service
from dbus.mainloop.glib import DBusGMainLoop
from gi.repository import GLib

DBusGMainLoop(set_as_default=True)
bus = dbus.SessionBus()
name = dbus.service.BusName("org.freedesktop.Notifications", bus)


class Notifications(dbus.service.Object):
    @dbus.service.method("org.freedesktop.Notifications", in_signature="", out_signature="as")
    def GetCapabilities(self):
        return ["actions", "body"]

    @dbus.service.method("org.freedesktop.Notifications", in_signature="", out_signature="ssss")
    def GetServerInformation(self):
        return ("Native smoke", "omo", "1", "1.2")

    @dbus.service.method(
        "org.freedesktop.Notifications", in_signature="susssasa{sv}i", out_signature="u"
    )
    def Notify(self, app, replace, icon, title, body, actions, hints, expire):
        request = urllib.request.Request(
            sys.argv[1], data=json.dumps({"notification": str(title), "body": str(body)}).encode()
        )
        with urllib.request.urlopen(request) as response:
            response.read()
        return dbus.UInt32(1)

    @dbus.service.signal("org.freedesktop.Notifications", signature="us")
    def ActionInvoked(self, id, action):
        pass

    @dbus.service.method("dev.scolastico.omo.NativeSmoke", in_signature="", out_signature="")
    def ActivateDefault(self):
        self.ActionInvoked(dbus.UInt32(1), "default")

    @dbus.service.method("org.freedesktop.Notifications", in_signature="u", out_signature="")
    def CloseNotification(self, id):
        pass


notifications = Notifications(bus, "/org/freedesktop/Notifications")
print("NOTIFICATIONS_READY", flush=True)
GLib.MainLoop().run()
