package dev.scolastico.omo.notifications

import android.app.Activity
import android.content.Intent
import android.net.Uri
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import androidx.activity.result.ActivityResultLauncher
import androidx.activity.result.ActivityResultRegistry
import androidx.activity.result.contract.ActivityResultContracts

/** Native chooser used by attachments and settings import, without a JS bridge. */
class BoardFiles(private val registry:ActivityResultRegistry) {
    private data class Selection(val key:String,val callback:ValueCallback<Array<Uri>>,val launcher:ActivityResultLauncher<Intent>)
    private var pending:Selection?=null
    private fun cancel() {
        val old=pending
        pending=null
        old?.launcher?.unregister()
        old?.callback?.onReceiveValue(null)
    }
    fun choose(callback:ValueCallback<Array<Uri>>,params:WebChromeClient.FileChooserParams):Boolean {
        cancel()
        // A late result from a cancelled chooser retains its own registry key.
        // It cannot be mistaken for the current board/input's selection.
        val key="omo-board-files-${java.util.UUID.randomUUID()}"
        val launcher=registry.register(key,ActivityResultContracts.StartActivityForResult()) {result->
            val selected=pending?.takeIf {it.key==key} ?: return@register
            pending=null
            selected.launcher.unregister()
            val data=result.data
            val uris=if(result.resultCode==Activity.RESULT_OK && data!=null) {
                val clip=data.clipData
                if(clip!=null) (0 until clip.itemCount).map {clip.getItemAt(it).uri}.toTypedArray()
                else data.data?.let {arrayOf(it)}
            } else null
            selected.callback.onReceiveValue(uris)
        }
        pending=Selection(key,callback,launcher)
        return try {
            val intent=params.createIntent()
            intent.putExtra(Intent.EXTRA_ALLOW_MULTIPLE,params.mode==WebChromeClient.FileChooserParams.MODE_OPEN_MULTIPLE)
            launcher.launch(intent)
            true
        } catch(e:Exception) {
            cancel()
            true
        }
    }
    fun close() = cancel()
}
