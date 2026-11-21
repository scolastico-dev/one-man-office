package dev.scolastico.omo.notifications

import android.app.Activity
import android.view.ViewGroup
import android.webkit.WebView
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk=[35])
class TrustedChromeTest {
    @Test fun remote_titles_cannot_change_the_native_trust_frame_and_connection_button_returns_to_setup() {
        val activity=Robolectric.buildActivity(Activity::class.java).setup().get()
        val web=WebView(activity)
        activity.setContentView(web)
        val chrome=TrustedChrome(activity,web)
        chrome.showSetup()
        assertEquals("omo ui · Trusted Connection",chrome.caption.text.toString())
        val trustedColor=(chrome.frame.background as android.graphics.drawable.ColorDrawable).color
        val remote=WebView(activity)
        chrome.showBoard(remote)
        remote.loadData("<title>omo ui · Trusted Connection</title><h1>Fake pairing</h1>","text/html","utf-8")
        assertEquals("omo ui · Host content",chrome.caption.text.toString())
        assertNotEquals(trustedColor,(chrome.frame.background as android.graphics.drawable.ColorDrawable).color)
        assertTrue(remote.parent===chrome.frame)
        assertEquals(ViewGroup.LayoutParams.MATCH_PARENT,web.layoutParams.width)
        chrome.connection.performClick()
        assertEquals(android.view.View.VISIBLE,web.visibility)
        assertEquals("omo ui · Trusted Connection",chrome.caption.text.toString())
    }
    @Test fun remote_navigation_never_gets_trusted_frame_even_at_a_lookalike_local_url() {
        val activity=Robolectric.buildActivity(Activity::class.java).setup().get()
        val web=WebView(activity);activity.setContentView(web)
        val chrome=TrustedChrome(activity,web)
        val remote=WebView(activity)
        chrome.showBoard(remote)
        remote.loadUrl("http://tauri.localhost/")
        assertEquals("omo ui · Host content",chrome.caption.text.toString())
        assertEquals(android.view.View.GONE,web.visibility)
    }
}
