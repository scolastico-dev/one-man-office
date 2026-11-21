package dev.scolastico.omo.notifications

import android.net.Uri
import java.net.InetAddress
import java.util.Locale

/** Native challenge scope. Android Uri retains IPv6 brackets; Chromium omits them. */
internal fun boardChallengeAllowed(origin:String,document:String,host:String,realm:String):Boolean {
    if(realm!="omo") return false
    val current=Uri.parse(document)
    val configured=Uri.parse(origin)
    val expected=challengeHost(configured.host) ?: return false
    return current.scheme==configured.scheme && current.port==configured.port &&
        challengeHost(current.host)==expected && challengeHost(host)==expected
}

private fun challengeHost(raw:String?):String? {
    val host=raw?.trim('[',']')?.takeIf {it.isNotEmpty()} ?: return null
    if(!host.contains(':')) return "host:${host.lowercase(Locale.ROOT)}"
    // Only numeric IPv6 literals reach InetAddress: hostname comparison never
    // performs DNS or makes authentication depend on the network.
    if(!host.all {it in '0'..'9' || it.lowercaseChar() in 'a'..'f' || it==':' || it=='.'}) return null
    return try {
        "ipv6:"+InetAddress.getByName(host).address.joinToString(":") {(it.toInt() and 255).toString(16)}
    } catch(e:Exception) {null}
}
