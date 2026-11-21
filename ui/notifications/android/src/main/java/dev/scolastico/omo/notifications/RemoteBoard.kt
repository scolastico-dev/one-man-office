package dev.scolastico.omo.notifications

import android.app.Activity
import android.net.Uri
import android.webkit.HttpAuthHandler
import android.webkit.WebChromeClient
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.webkit.WebViewCompat
import androidx.webkit.WebViewFeature
import org.json.JSONObject

/** A separate WebView without Tauri/JavaScript native command interfaces. */
class RemoteBoard(private val activity:Activity,private val chrome:TrustedChrome,args:JSONObject) {
    val view=WebView(activity)
    private val files=BoardFiles((activity as androidx.activity.ComponentActivity).activityResultRegistry)
    init {
        require(WebViewFeature.isFeatureSupported(WebViewFeature.DOCUMENT_START_SCRIPT))
        view.settings.javaScriptEnabled=true
        view.settings.domStorageEnabled=true
        val origin=args.getString("origin")
        val auth=args.getString("auth")
        val user=args.getString("username")
        val secret=args.getString("secret")
        WebViewCompat.addDocumentStartJavaScript(view,args.getString("script"),setOf(origin))
        view.webViewClient=object:WebViewClient() {
            override fun shouldOverrideUrlLoading(view:WebView,request:WebResourceRequest):Boolean {
                val url=request.url
                if(url.toString()=="omo-ui:configure") {chrome.showSetup();return true}
                return url.scheme !in listOf("http","https") || url.host=="tauri.localhost"
            }
            override fun onReceivedHttpAuthRequest(view:WebView,handler:HttpAuthHandler,host:String,realm:String) {
                if(auth=="basic" && boardChallengeAllowed(origin,view.url ?: "",host,realm) && handler.useHttpAuthUsernamePassword()) {
                    handler.proceed(user,secret)
                } else handler.cancel()
            }
        }
        // Default WebChromeClient refuses custom fullscreen views: the frame remains visible.
        view.webChromeClient=object:WebChromeClient() {
            override fun onShowFileChooser(view:WebView,callback:android.webkit.ValueCallback<Array<Uri>>,params:FileChooserParams)=files.choose(callback,params)
        }
        chrome.showBoard(view)
        view.loadUrl(args.getString("url"))
    }
    fun close() {files.close();view.stopLoading()}
    fun navigate(url:String) {
        require(Uri.parse(url).scheme in listOf("http","https"))
        chrome.showBoard(view)
        view.loadUrl(url)
    }
}
