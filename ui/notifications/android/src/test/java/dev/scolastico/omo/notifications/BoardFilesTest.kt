package dev.scolastico.omo.notifications

import android.app.Activity
import android.content.ClipData
import android.content.Intent
import android.net.Uri
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import androidx.activity.result.ActivityResultRegistry
import androidx.activity.result.contract.ActivityResultContract
import androidx.core.app.ActivityOptionsCompat
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk=[35])
class BoardFilesTest {
    private class Registry:ActivityResultRegistry() {
        var request=0
        lateinit var intent:Intent
        override fun <I,O> onLaunch(code:Int,contract:ActivityResultContract<I,O>,input:I,options:ActivityOptionsCompat?) {request=code;intent=input as Intent}
    }
    private fun params(multiple:Boolean=false)=object:WebChromeClient.FileChooserParams() {
        override fun getMode()=if(multiple) MODE_OPEN_MULTIPLE else MODE_OPEN
        override fun getAcceptTypes()=arrayOf("image/png","image/jpeg")
        override fun isCaptureEnabled()=false
        override fun getTitle():CharSequence?=null
        override fun getFilenameHint():String?=null
        override fun createIntent()=Intent(Intent.ACTION_GET_CONTENT).setType("image/*")
    }
    @Test fun picker_delivers_selected_image_and_zip_uris_and_preserves_multiple_selection() {
        val registry=Registry();val files=BoardFiles(registry)
        var result:Array<Uri>?=null
        files.choose(ValueCallback {result=it},params(true))
        assertTrue(registry.intent.getBooleanExtra(Intent.EXTRA_ALLOW_MULTIPLE,false))
        val first=Uri.parse("content://picker/image.png")
        val second=Uri.parse("content://picker/settings.zip")
        val clip=ClipData.newRawUri("selected",first);clip.addItem(ClipData.Item(second))
        registry.dispatchResult(registry.request,Activity.RESULT_OK,Intent().apply {clipData=clip})
        assertArrayEquals(arrayOf(first,second),result)
        files.close()
    }
    @Test fun replacement_and_close_complete_each_pending_callback_once_with_cancellation() {
        val registry=Registry();val files=BoardFiles(registry)
        val answers=mutableListOf<Array<Uri>?>()
        files.choose(ValueCallback {answers.add(it)},params())
        files.choose(ValueCallback {answers.add(it)},params())
        assertEquals(1,answers.size);assertNull(answers[0])
        files.close()
        assertEquals(2,answers.size);assertNull(answers[1])
    }
    @Test fun a_replaced_pickers_late_result_never_reaches_the_new_callback() {
        val registry=Registry();val files=BoardFiles(registry)
        val answers=mutableListOf<Array<Uri>?>()
        files.choose(ValueCallback {answers.add(it)},params())
        val first=registry.request
        files.choose(ValueCallback {answers.add(it)},params())
        val second=registry.request
        registry.dispatchResult(first,Activity.RESULT_OK,Intent().setData(Uri.parse("content://picker/old")))
        assertEquals(1,answers.size)
        registry.dispatchResult(second,Activity.RESULT_OK,Intent().setData(Uri.parse("content://picker/new")))
        assertEquals(2,answers.size)
        assertArrayEquals(arrayOf(Uri.parse("content://picker/new")),answers[1])
        files.close()
    }
}
