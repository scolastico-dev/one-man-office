package dev.scolastico.omo.notifications

import android.app.Activity
import android.content.Intent
import android.webkit.WebView
import androidx.webkit.ScriptHandler
import androidx.webkit.WebViewCompat
import androidx.webkit.WebViewFeature
import app.tauri.annotation.Command
import app.tauri.annotation.InvokeArg
import app.tauri.annotation.TauriPlugin
import app.tauri.plugin.Channel
import app.tauri.plugin.Invoke
import app.tauri.plugin.JSObject
import app.tauri.plugin.Plugin
import java.util.concurrent.Executors
import org.json.JSONObject

@InvokeArg class ActivationArgs {lateinit var handler:Channel}
@TauriPlugin
class PollingPlugin(private val activity:Activity):Plugin(activity) {
    private val io=Executors.newSingleThreadExecutor()
    private var webView:WebView?=null
    private var bridge:ScriptHandler?=null
    private var activation:Channel?=null
    private var pending:Intent?=null
    override fun load(webView:WebView) {
        this.webView=webView
        Channels.create(activity)
        pending=activity.intent
        dispatch()
    }
    override fun onNewIntent(intent:Intent) {pending=intent;dispatch()}
    private fun dispatch() {
        val channel=activation ?: return
        val intent=pending ?: return
        pending=null
        io.execute {
            try {RuntimePolling.engine(activity).activation(intent)?.let {channel.send(JSObject(it.toString()))}}
            catch(e:Exception) {/* No credentials, stale work or invalid launch: stay on setup. */}
        }
    }
    private fun async(invoke:Invoke, action:(PollingEngine)->JSONObject) {
        io.execute {
            try {invoke.resolve(JSObject(action(RuntimePolling.engine(activity)).toString()))}
            catch(e:Exception) {invoke.reject("Android notification operation failed. Check permissions, saved credentials and notification channels in Android Settings.")}
        }
    }
    @Command fun configure(invoke:Invoke) {
        val args=JSONObject(invoke.getArgs().toString())
        async(invoke) {it.configure(args);it.status()}
    }
    @Command fun disconnect(invoke:Invoke) {
        bridge?.remove();bridge=null
        async(invoke) {it.configure(null);it.status()}
    }
    @Command fun status(invoke:Invoke) = async(invoke) {it.status()}
    @Command fun recover(invoke:Invoke) {
        val replay=invoke.getArgs().getBoolean("replay")
        async(invoke) {it.recover(replay);it.status()}
    }
    @Command fun registerActivation(invoke:Invoke) {
        activation=invoke.parseArgs(ActivationArgs::class.java).handler
        dispatch()
        invoke.resolve()
    }
    @Command fun setBridge(invoke:Invoke) {
        val view=webView ?: return invoke.reject("Board WebView is unavailable.")
        if(!WebViewFeature.isFeatureSupported(WebViewFeature.DOCUMENT_START_SCRIPT)) {
            invoke.reject("Update Android System WebView to support native board notifications.");return
        }
        val args=invoke.getArgs()
        bridge?.remove()
        bridge=WebViewCompat.addDocumentStartJavaScript(view,args.getString("script"),setOf(args.getString("origin")))
        invoke.resolve()
    }
}
